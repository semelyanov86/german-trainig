package llm

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"german-trainer/internal/skill"
)

type Claude struct {
	bin       string
	model     string
	workDir   string
	skillFile string
	logger    *log.Logger
}

func NewClaude(bin, model, workDir, skillFile string, logger *log.Logger) *Claude {
	return &Claude{bin: bin, model: model, workDir: workDir, skillFile: skillFile, logger: logger}
}

func (c *Claude) Call(history, userMessage string) (string, error) {
	var prompt string
	if history != "" {
		prompt = fmt.Sprintf("/german_tutor_skill\n\nGesprächsverlauf:\n%s\n\nLetzte Nachricht des Nutzers: %s\n\nAntworte nur auf die letzte Nachricht.", history, userMessage)
	} else {
		prompt = fmt.Sprintf("/german_tutor_skill\n\n%s", userMessage)
	}

	args := []string{"-p", prompt, "--output-format", "text"}
	if c.skillFile != "" {
		raw, err := os.ReadFile(c.skillFile)
		if err == nil {
			args = append(args, "--system-prompt", skill.ExtractContent(string(raw)))
		} else {
			c.logger.Printf("WARN: cannot read skill file %s: %v", c.skillFile, err)
		}
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
