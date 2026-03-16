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

const polzaSTTEndpoint = "https://polza.ai/api/v1/audio/transcriptions"

type PolzaTranscriber struct {
	apiKey string
	model  string
	logger *log.Logger
}

func NewPolzaTranscriber(apiKey, model string, logger *log.Logger) *PolzaTranscriber {
	if model == "" {
		model = "openai/gpt-4o-transcribe"
	}
	return &PolzaTranscriber{apiKey: apiKey, model: model, logger: logger}
}

func (p *PolzaTranscriber) Transcribe(wavPath string) (string, error) {
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

	writer.WriteField("model", p.model)
	writer.WriteField("language", "de")
	writer.WriteField("response_format", "json")
	writer.Close()

	req, err := http.NewRequest("POST", polzaSTTEndpoint, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("polza stt request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("polza stt HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	p.logger.Printf("Polza STT took %v", time.Since(start))
	return result.Text, nil
}
