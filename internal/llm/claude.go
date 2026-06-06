package llm

import (
	"fmt"
	"log"
	"os"
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

	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.workDir
	cmd.Env = append(os.Environ(), "HOME=/root")

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			c.logger.Printf("claude stderr: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("claude error: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
