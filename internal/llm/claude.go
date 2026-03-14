package llm

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

type Claude struct {
	bin     string
	model   string
	workDir string
	logger  *log.Logger
}

func NewClaude(bin, model, workDir string, logger *log.Logger) *Claude {
	return &Claude{bin: bin, model: model, workDir: workDir, logger: logger}
}

func (c *Claude) Call(systemPrompt, history, userMessage string) (string, error) {
	prompt := userMessage
	if history != "" {
		prompt = fmt.Sprintf("Gesprächsverlauf:\n%s\n\nLetzte Nachricht des Nutzers: %s\n\nAntworte nur auf die letzte Nachricht.", history, userMessage)
	}

	cmd := exec.Command(c.bin,
		"-p", prompt,
		"--model", c.model,
		"--system-prompt", systemPrompt,
		"--no-session-persistence",
	)
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
