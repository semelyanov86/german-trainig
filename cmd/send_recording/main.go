package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"german-trainer/internal/config"
)

const envFile = "/etc/german-trainer/.env"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: send_recording <path/to/recording.wav>")
		os.Exit(1)
	}
	wavPath := os.Args[1]

	cfg, err := config.Load(envFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	if cfg.WebhookBaseURL == "" {
		fmt.Fprintln(os.Stderr, "WEBHOOK_URL not set in config")
		os.Exit(1)
	}
	if cfg.NotifyWebhookToken == "" {
		fmt.Fprintln(os.Stderr, "NOTIFY_WEBHOOK_TOKEN not set in config")
		os.Exit(1)
	}

	// Convert WAV -> OGG/Opus
	oggPath, err := convertToOgg(wavPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "convert: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(oggPath)

	// Send to webhook
	endpoint := strings.TrimRight(cfg.WebhookBaseURL, "/") + "/webhooks/voice"
	if err := sendVoice(endpoint, cfg.NotifyWebhookToken, oggPath); err != nil {
		fmt.Fprintf(os.Stderr, "send: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("OK")
}

func convertToOgg(wavPath string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(wavPath), filepath.Ext(wavPath))
	oggPath := filepath.Join(os.TempDir(), base+".ogg")

	cmd := exec.Command("ffmpeg", "-y", "-i", wavPath,
		"-c:a", "libopus", "-b:a", "64k", oggPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg: %w\n%s", err, out)
	}
	return oggPath, nil
}

func sendVoice(endpoint, token, oggPath string) error {
	f, err := os.Open(oggPath)
	if err != nil {
		return fmt.Errorf("open ogg: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	fw, err := mw.CreateFormFile("voice", filepath.Base(oggPath))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err = io.Copy(fw, f); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
