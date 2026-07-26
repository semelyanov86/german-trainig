package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// claudeProvider runs the Claude Code CLI as a subprocess. Kept as a
// switchable fallback backend. The system prompt is supplied by the
// application (via --system-prompt) rather than a server-side skill.
type claudeProvider struct {
	bin               string
	model             string
	workDir           string
	maxOutputTokens   int
	maxThinkingTokens int
	effort            string
	logger            *log.Logger
}

func newClaude(s Spec, logger *log.Logger) *claudeProvider {
	return &claudeProvider{
		bin:               s.ClaudeBin,
		model:             s.ClaudeModel,
		workDir:           s.WorkDir,
		maxOutputTokens:   s.ClaudeMaxOutputTokens,
		maxThinkingTokens: s.ClaudeMaxThinkingTokens,
		effort:            s.ClaudeEffort,
		logger:            logger,
	}
}

// claudeEvent is the subset of a `--output-format stream-json` event we read.
type claudeEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func (c *claudeProvider) Complete(system string, messages []Message) (string, error) {
	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Content)
	}

	// stream-json rather than text: a reply that hits the output-token cap is
	// finished off in a follow-up assistant message, and `--output-format text`
	// prints only that last piece — which silently reduced a 40k-char analysis
	// to its tail. Every assistant message is visible in the stream, so the
	// full reply can be reassembled.
	args := []string{"-p", b.String(), "--output-format", "stream-json", "--verbose"}
	if system != "" {
		args = append(args, "--system-prompt", system)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	if s := c.settingsJSON(); s != "" {
		args = append(args, "--settings", s)
	}

	// The AGI process runs as the asterisk user, which cannot read sergey's
	// Claude subscription credentials (~/.claude). Run the claude CLI as sergey
	// via sudo (-H sets HOME to sergey's home so the CLI finds its login).
	// A narrow rule in /etc/sudoers.d/german-trainer permits this NOPASSWD.
	sudoArgs := append([]string{"-n", "-u", "sergey", "-H", c.bin}, args...)
	cmd := exec.Command("sudo", sudoArgs...)
	cmd.Dir = "/tmp" // sergey-readable cwd; HISTORY_DIR is asterisk-only

	// Output() keeps stderr out of stdout so the JSON stream stays parseable.
	output, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			c.logger.Printf("claude stderr: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("claude error: %w", err)
	}
	return c.parseStream(output)
}

// settingsJSON builds a per-invocation --settings payload. Passing it on the
// command line keeps these limits scoped to the AGI runs: sudo strips the
// environment, and the CLI user's own settings must stay untouched.
func (c *claudeProvider) settingsJSON() string {
	env := map[string]string{}
	if c.maxOutputTokens > 0 {
		env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = strconv.Itoa(c.maxOutputTokens)
	}
	if c.maxThinkingTokens > 0 {
		env["MAX_THINKING_TOKENS"] = strconv.Itoa(c.maxThinkingTokens)
	}
	settings := map[string]any{}
	if len(env) > 0 {
		settings["env"] = env
	}
	if c.effort != "" {
		settings["effortLevel"] = c.effort
	}
	if len(settings) == 0 {
		return ""
	}
	data, err := json.Marshal(settings)
	if err != nil {
		c.logger.Printf("claude: cannot build settings: %v", err)
		return ""
	}
	return string(data)
}

// parseStream concatenates the text of every assistant message in the event
// stream. Unparseable lines are skipped so a stray non-JSON line from the CLI
// cannot cost us the whole reply.
func (c *claudeProvider) parseStream(output []byte) (string, error) {
	var parts []string
	var failure string
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev claudeEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, blk := range ev.Message.Content {
				if blk.Type == "text" && blk.Text != "" {
					parts = append(parts, blk.Text)
				}
			}
		case "result":
			if ev.IsError {
				failure = ev.Subtype
			}
		}
	}
	if failure != "" {
		return "", fmt.Errorf("claude result error: %s", failure)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("claude returned no text (%d bytes of stream)", len(output))
	}
	if len(parts) > 1 {
		c.logger.Printf("claude: reply came in %d messages, stitched", len(parts))
	}
	return stitch(parts), nil
}

// stitch reassembles a reply that was split across assistant messages.
func stitch(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		if resumesOnNewLine(out, p) {
			out += "\n"
		}
		out += p
	}
	return out
}

// resumesOnNewLine reports whether the line break between two halves of a
// split reply was lost. The cap can cut anywhere: mid-word the halves join
// directly, but a new heading or numbered item means the break itself was
// never emitted.
func resumesOnNewLine(prev, next string) bool {
	if prev == "" || next == "" || strings.HasSuffix(prev, "\n") || strings.HasPrefix(next, "\n") {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(prev)
	first, _ := utf8.DecodeRuneInString(next)
	if !unicode.IsLetter(last) && last != '.' && last != ')' && last != ':' {
		return false
	}
	return unicode.IsUpper(first) || unicode.IsDigit(first)
}
