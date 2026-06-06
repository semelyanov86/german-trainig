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

const polzaChatEndpoint = "https://polza.ai/api/v1/chat/completions"

// polzaProvider talks to polza.ai's OpenAI-compatible chat completions API.
type polzaProvider struct {
	apiKey      string
	model       string
	temperature string
	reasoning   string
	maxTokens   int
	logger      *log.Logger
}

func newPolza(s Spec, logger *log.Logger) *polzaProvider {
	return &polzaProvider{
		apiKey:      s.PolzaAPIKey,
		model:       s.Model,
		temperature: s.Temperature,
		reasoning:   s.Reasoning,
		maxTokens:   s.MaxTokens,
		logger:      logger,
	}
}

type polzaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (p *polzaProvider) Complete(system string, messages []Message) (string, error) {
	start := time.Now()

	msgs := make([]polzaMessage, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, polzaMessage{Role: RoleSystem, Content: system})
	}
	for _, m := range messages {
		msgs = append(msgs, polzaMessage{Role: m.Role, Content: m.Content})
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

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", polzaChatEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("polza chat request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("polza chat HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int     `json:"total_tokens"`
			CostRub     float64 `json:"cost_rub"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w (body: %s)", err, string(respBody))
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("polza chat: no choices in response: %s", string(respBody))
	}

	content := strings.TrimSpace(result.Choices[0].Message.Content)
	p.logger.Printf("Polza LLM (%s) took %v, tokens=%d cost=%.4f RUB",
		p.model, time.Since(start), result.Usage.TotalTokens, result.Usage.CostRub)
	return content, nil
}
