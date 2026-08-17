package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// chatBackend describes one OpenAI-compatible chat provider. polza.ai and
// openrouter.ai speak the same dialect, so they differ only in these fields.
type chatBackend struct {
	name         string // label used in log lines
	endpoint     string // full chat completions URL
	apiKey       string
	includeUsage bool // ask the API to report cost (OpenRouter needs the opt-in)
}

// openAIChatProvider talks to any OpenAI-compatible /chat/completions endpoint.
type openAIChatProvider struct {
	backend     chatBackend
	model       string
	temperature string
	reasoning   string
	maxTokens   int
	logger      *log.Logger
}

func newOpenAIChat(b chatBackend, s Spec, logger *log.Logger) *openAIChatProvider {
	return &openAIChatProvider{
		backend:     b,
		model:       s.Model,
		temperature: s.Temperature,
		reasoning:   s.Reasoning,
		maxTokens:   s.MaxTokens,
		logger:      logger,
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (p *openAIChatProvider) Complete(system string, messages []Message) (string, error) {
	start := time.Now()

	msgs := make([]chatMessage, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, chatMessage{Role: RoleSystem, Content: system})
	}
	for _, m := range messages {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Content})
	}

	body := map[string]interface{}{
		"model":    p.model,
		"messages": msgs,
	}
	if p.maxTokens > 0 {
		body["max_tokens"] = p.maxTokens
	}
	if p.temperature != "" {
		if t, err := strconv.ParseFloat(p.temperature, 64); err == nil {
			body["temperature"] = t
		}
	}
	if p.reasoning != "" {
		// OpenRouter-style reasoning control; supported by reasoning models.
		body["reasoning"] = map[string]string{"effort": p.reasoning}
	}
	if p.backend.includeUsage {
		body["usage"] = map[string]bool{"include": true}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", p.backend.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.backend.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s chat request: %w", p.backend.name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s chat HTTP %d: %s", p.backend.name, resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int     `json:"total_tokens"`
			CostRub     float64 `json:"cost_rub"` // polza
			Cost        float64 `json:"cost"`     // openrouter, in USD credits
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w (body: %s)", err, string(respBody))
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("%s chat: no choices in response: %s", p.backend.name, string(respBody))
	}

	// Reasoning models put their thoughts in a separate field, so an empty
	// content here means the reply itself was lost (usually max_tokens spent on
	// reasoning) — report it instead of playing silence to the caller.
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("%s chat: empty content in response: %s", p.backend.name, string(respBody))
	}

	p.logger.Printf("%s LLM (%s) took %v, tokens=%d cost=%s",
		p.backend.name, p.model, time.Since(start), result.Usage.TotalTokens,
		formatCost(result.Usage.CostRub, result.Usage.Cost))
	return content, nil
}

// formatCost renders whichever cost field the backend reported.
func formatCost(rub, usd float64) string {
	switch {
	case rub > 0:
		return fmt.Sprintf("%.4f RUB", rub)
	case usd > 0:
		return fmt.Sprintf("%.6f USD", usd)
	default:
		return "n/a"
	}
}
