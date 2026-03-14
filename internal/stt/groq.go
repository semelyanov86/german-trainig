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

const (
	groqEndpoint = "https://api.groq.com/openai/v1/audio/transcriptions"
	groqModel    = "whisper-large-v3"
)

type GroqTranscriber struct {
	apiKey string
	logger *log.Logger
}

func NewGroqTranscriber(apiKey string, logger *log.Logger) *GroqTranscriber {
	return &GroqTranscriber{apiKey: apiKey, logger: logger}
}

func (g *GroqTranscriber) Transcribe(wavPath string) (string, error) {
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

	writer.WriteField("model", groqModel)
	writer.WriteField("language", "de")
	writer.WriteField("response_format", "json")
	writer.Close()

	req, err := http.NewRequest("POST", groqEndpoint, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("groq request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("groq HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	g.logger.Printf("Groq STT took %v", time.Since(start))
	return result.Text, nil
}
