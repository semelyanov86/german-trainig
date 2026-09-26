package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// codexProvider uses the authenticated CLI user's subscription, just like
// Claude. Each completion is independent; the application owns the history.
type codexProvider struct {
	bin       string
	runner    string
	model     string
	reasoning string
	timeout   time.Duration
	logger    *log.Logger
}

func newCodex(s Spec, logger *log.Logger) *codexProvider {
	bin := s.CodexBin
	if bin == "" {
		bin = "/usr/local/bin/codex"
	}
	model := s.Model
	if model == "" {
		model = "gpt-6-luna"
	}
	runner := s.CodexRunner
	if runner == "" {
		runner = "/usr/local/libexec/german-trainer-codex"
	}
	return &codexProvider{
		bin: bin, runner: runner, model: model, reasoning: s.Reasoning,
		timeout: s.Retry.withDefaults().Timeout, logger: logger,
	}
}

func (c *codexProvider) Complete(system string, messages []Message) (string, error) {
	// Both the system prompt and caller's transcript travel through stdin, not
	// process arguments. JSON retains roles and escapes transcript delimiters.
	type promptMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	prompt := struct {
		System   string          `json:"system"`
		Messages []promptMessage `json:"messages"`
	}{System: system}
	for _, m := range messages {
		prompt.Messages = append(prompt.Messages, promptMessage{m.Role, m.Content})
	}
	input, err := json.Marshal(prompt)
	if err != nil {
		return "", fmt.Errorf("codex prompt: %w", err)
	}
	args := []string{
		"-n", "-u", "sergey", "-H", c.runner,
		// The root-owned runner uses timeout as sergey, who can signal the CLI.
		// This also works with sudo-rs, which has no sudo -T support.
		strconv.FormatInt(int64((c.timeout+time.Second-1)/time.Second), 10),
		c.bin, "exec",
		"--ignore-user-config", "--skip-git-repo-check", "--ephemeral",
		"--sandbox", "read-only", "--color", "never", "--json",
		"--model", c.model,
		"--disable", "shell_tool", "--disable", "hooks",
		"--disable", "apps", "--disable", "multi_agent", "--disable", "memories",
		"-c", "web_search=\"disabled\"", "-c", "project_doc_max_bytes=0",
		"-c", "developer_instructions=\"You are a text-only completion backend for a phone conversation. The input is JSON with system and messages fields. Follow the system field as your instructions and produce the next assistant reply to messages, preserving their roles. Return only the requested reply, without commentary about your work. Do not use tools or inspect files.\"",
	}
	if c.reasoning != "" {
		// JSON string quoting is also valid TOML string quoting for CLI overrides.
		effort, _ := json.Marshal(c.reasoning)
		args = append(args, "-c", "model_reasoning_effort="+string(effort))
	}
	args = append(args, "-")
	cmd := exec.Command("sudo", args...)
	cmd.Dir = "/tmp" // the authenticated CLI user cannot enter HISTORY_DIR
	cmd.Stdin = bytes.NewReader(input)
	started := time.Now()
	output, err := cmd.Output()
	if err != nil {
		// The CLI can print prompts and partial replies to stderr. Keep them out
		// of diagnostics; the exit status and JSON error event identify failures.
		if _, streamErr := c.parseStream(output); streamErr != nil {
			return "", fmt.Errorf("codex error: %w (%v)", err, streamErr)
		}
		return "", fmt.Errorf("codex error: %w", err)
	}
	reply, err := c.parseStream(output)
	if err == nil {
		c.logger.Printf("Codex LLM (%s) took %v", c.model, time.Since(started))
	}
	return reply, err
}

// parseStream takes only the last agent message of a successfully completed
// turn. Progress, reasoning and tool events must never be spoken by TTS.
func (c *codexProvider) parseStream(output []byte) (string, error) {
	var reply string
	completed := false
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return "", fmt.Errorf("codex invalid JSON event: %w", err)
		}
		switch ev.Type {
		case "item.completed":
			if ev.Item.Type == "agent_message" {
				reply = strings.TrimSpace(ev.Item.Text)
			}
		case "turn.completed":
			completed = true
		case "turn.failed":
			return "", fmt.Errorf("codex turn failed: %.512s", ev.Error.Message)
			// Standalone error events can describe transient reconnects. The final
			// turn.failed/completed event determines whether the turn succeeded.
		}
	}
	if !completed || reply == "" {
		return "", fmt.Errorf("codex returned no completed reply (%d bytes of stream)", len(output))
	}
	return reply, nil
}
