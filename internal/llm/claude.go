package llm

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// claudeProvider runs the Claude Code CLI as a subprocess. Kept as a
// switchable fallback backend. The system prompt is supplied by the
// application (via --system-prompt) rather than a server-side skill.
type claudeProvider struct {
	bin     string
	model   string
	workDir string
	logger  *log.Logger
}

func newClaude(s Spec, logger *log.Logger) *claudeProvider {
	return &claudeProvider{bin: s.ClaudeBin, model: s.ClaudeModel, workDir: s.WorkDir, logger: logger}
}

func (c *claudeProvider) Complete(system string, messages []Message) (string, error) {
	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Content)
	}

	args := []string{"-p", b.String(), "--output-format", "text"}
	if system != "" {
		args = append(args, "--system-prompt", system)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}

	// The AGI process runs as the asterisk user, which cannot read sergey's
	// Claude subscription credentials (~/.claude). Run the claude CLI as sergey
	// via sudo (-H sets HOME to sergey's home so the CLI finds its login).
	// A narrow rule in /etc/sudoers.d/german-trainer permits this NOPASSWD.
	sudoArgs := append([]string{"-n", "-u", "sergey", "-H", c.bin}, args...)
	cmd := exec.Command("sudo", sudoArgs...)
	cmd.Dir = "/tmp" // sergey-readable cwd; HISTORY_DIR is asterisk-only

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.logger.Printf("claude output: %s", strings.TrimSpace(string(output)))
		return "", fmt.Errorf("claude error: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
