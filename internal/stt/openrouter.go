package stt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const openRouterSTTEndpoint = "https://openrouter.ai/api/v1/audio/transcriptions"

// OpenRouterTranscriber talks to openrouter.ai's OpenAI-compatible
// audio/transcriptions endpoint. Same request/response shape as polza.
type OpenRouterTranscriber struct {
	apiKey string
	model  string
	logger *log.Logger
}

func NewOpenRouterTranscriber(apiKey, model string, logger *log.Logger) *OpenRouterTranscriber {
	if model == "" {
		model = "openai/gpt-4o-transcribe"
	}
	return &OpenRouterTranscriber{apiKey: apiKey, model: model, logger: logger}
}

func (o *OpenRouterTranscriber) Transcribe(wavPath string) (string, error) {
	start := time.Now()

	file, err := os.Open(wavPath)
	if err != nil {
		return "", fmt.Errorf("open audio: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filepath.Base(wavPath))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy file: %w", err)
	}

	writer.WriteField("model", o.model)
	writer.WriteField("language", "de")
	writer.WriteField("response_format", "json")
	writer.Close()

	req, err := http.NewRequest("POST", openRouterSTTEndpoint, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openrouter stt request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("openrouter stt HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	o.logger.Printf("OpenRouter STT took %v", time.Since(start))
	return result.Text, nil
}
