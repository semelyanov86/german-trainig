package summary

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"german-trainer/internal/llm"
)

type Summarizer struct {
	provider     llm.Provider
	systemPrompt string
	webhookURL   string
	webhookToken string
	logger       *log.Logger
	prefix       string
	logContent   bool
}

func New(provider llm.Provider, systemPrompt, webhookURL, webhookToken string, logger *log.Logger) *Summarizer {
	return NewWithPolicy(provider, systemPrompt, webhookURL, webhookToken, "Вот транскрипт разговора:\n\n", true, logger)
}

func NewWithPolicy(provider llm.Provider, systemPrompt, webhookURL, webhookToken, prefix string, logContent bool, logger *log.Logger) *Summarizer {
	return &Summarizer{
		provider:     provider,
		systemPrompt: systemPrompt,
		webhookURL:   webhookURL,
		webhookToken: webhookToken,
		logger:       logger,
		prefix:       prefix,
		logContent:   logContent,
	}
}

func (s *Summarizer) Run(historyContent string) error {
	if strings.TrimSpace(historyContent) == "" {
		s.logger.Println("Summary: empty history, skipping")
		return nil
	}

	s.logger.Println("Summary: generating post-call analysis...")
	report, err := s.generate(historyContent)
	if err != nil {
		return fmt.Errorf("summary generation: %w", err)
	}
	s.logger.Printf("Summary: generated %d chars", len(report))
	if warn := checkReport(report); warn != "" {
		if s.logContent {
			s.logger.Printf("Summary: WARNING %s", warn)
		} else {
			s.logger.Println("Summary: WARNING report may be truncated")
		}
	}

	if s.webhookURL == "" {
		s.logger.Println("Summary: no webhook URL configured, skipping send")
		return nil
	}

	return s.sendWebhook(report)
}

func (s *Summarizer) generate(history string) (string, error) {
	user := s.prefix + history
	report, err := s.provider.Complete(s.systemPrompt, []llm.Message{{Role: llm.RoleUser, Content: user}})
	if err != nil {
		return "", err
	}
	return stripMarkdown(strings.TrimSpace(report)), nil
}

// minReportBytes: a complete six-section analysis runs 30–60k bytes, so
// anything close to a few thousand means the model reply never made it here in
// full.
const minReportBytes = 5000

// checkReport returns a description of why the report looks cut off, or "" when
// it looks whole. A truncated report used to be sent to the webhook without a
// trace in the log, which made the loss invisible until the report was read.
func checkReport(report string) string {
	if len(report) < minReportBytes {
		return fmt.Sprintf("report looks truncated: %d bytes (expected at least %d)", len(report), minReportBytes)
	}
	last, _ := utf8.DecodeLastRuneInString(report)
	if !strings.ContainsRune(".!?)»\"", last) {
		return fmt.Sprintf("report does not end on a sentence, tail=%q", lastRunes(report, 60))
	}
	return ""
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
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
	if s.logContent {
		s.logger.Printf("Summary: sending to %s", s.webhookURL)
	} else {
		s.logger.Println("Summary: sending webhook")
	}

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
