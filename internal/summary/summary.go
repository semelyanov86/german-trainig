package summary

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"german-trainer/internal/skill"
)

type Summarizer struct {
	claudeBin    string
	workDir      string
	skillFile    string
	webhookURL   string
	webhookToken string
	logger       *log.Logger
}

func New(claudeBin, workDir, skillFile, webhookURL, webhookToken string, logger *log.Logger) *Summarizer {
	return &Summarizer{
		claudeBin:    claudeBin,
		workDir:      workDir,
		skillFile:    skillFile,
		webhookURL:   webhookURL,
		webhookToken: webhookToken,
		logger:       logger,
	}
}

func (s *Summarizer) Run(historyContent string) error {
	if strings.TrimSpace(historyContent) == "" {
		s.logger.Println("Summary: empty history, skipping")
		return nil
	}

	s.logger.Println("Summary: generating post-call analysis...")
	report, err := s.callClaude(historyContent)
	if err != nil {
		return fmt.Errorf("claude summary call: %w", err)
	}
	s.logger.Printf("Summary: generated %d chars", len(report))

	if s.webhookURL == "" {
		s.logger.Println("Summary: no webhook URL configured, skipping send")
		return nil
	}

	return s.sendWebhook(report)
}

func (s *Summarizer) callClaude(history string) (string, error) {
	prompt := fmt.Sprintf("/german-summary\n\nВот транскрипт разговора:\n\n%s", history)

	args := []string{"-p", prompt, "--output-format", "text"}
	if s.skillFile != "" {
		raw, err := os.ReadFile(s.skillFile)
		if err == nil {
			args = append(args, "--system-prompt", skill.ExtractContent(string(raw)))
		} else {
			s.logger.Printf("WARN: cannot read summary skill file %s: %v", s.skillFile, err)
		}
	}

	cmd := exec.Command(s.claudeBin, args...)
	cmd.Dir = s.workDir
	cmd.Env = append(os.Environ(), "HOME=/root")

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			s.logger.Printf("claude summary stderr: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("claude error: %w", err)
	}
	return stripMarkdown(strings.TrimSpace(string(output))), nil
}

func stripMarkdown(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		// Remove heading markers (#, ##, ###)
		trimmed := strings.TrimLeft(line, "#")
		if trimmed != line {
			trimmed = strings.TrimSpace(trimmed)
		}
		// Skip horizontal rules
		stripped := strings.TrimSpace(trimmed)
		if stripped == "---" || stripped == "***" || stripped == "===" {
			continue
		}
		// Remove bold/italic markers
		trimmed = strings.ReplaceAll(trimmed, "**", "")
		trimmed = strings.ReplaceAll(trimmed, "__", "")
		trimmed = strings.ReplaceAll(trimmed, "*", "")
		// Skip table separator rows (|---|---|)
		if strings.Contains(trimmed, "|") && strings.Contains(trimmed, "---") {
			continue
		}
		// Clean table pipes
		if strings.Contains(trimmed, "|") {
			trimmed = strings.ReplaceAll(trimmed, " | ", " — ")
			trimmed = strings.TrimPrefix(trimmed, "| ")
			trimmed = strings.TrimSuffix(trimmed, " |")
			trimmed = strings.TrimPrefix(trimmed, "|")
			trimmed = strings.TrimSuffix(trimmed, "|")
		}
		lines = append(lines, trimmed)
	}
	return strings.Join(lines, "\n")
}

func (s *Summarizer) sendWebhook(report string) error {
	s.logger.Printf("Summary: sending to %s", s.webhookURL)

	req, err := http.NewRequest(http.MethodPost, s.webhookURL, strings.NewReader(report))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+s.webhookToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	s.logger.Printf("Summary: webhook sent, status %d", resp.StatusCode)
	return nil
}
