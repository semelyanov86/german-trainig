package tts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const polzaTTSEndpoint = "https://polza.ai/api/v1/audio/speech"

type PolzaSynth struct {
	cfg    Config
	logger *log.Logger
}

func (p *PolzaSynth) Synthesize(text string) (string, []string, error) {
	start := time.Now()

	payload := fmt.Sprintf(`{"model":"%s","input":%q,"voice":"%s","response_format":"mp3"}`,
		p.cfg.PolzaTTSModel, text, p.cfg.PolzaTTSVoice)

	req, err := http.NewRequest("POST", polzaTTSEndpoint, strings.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.PolzaAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("polza tts request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("polza tts HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, fmt.Errorf("parse response: %w", err)
	}

	if result.Audio == "" {
		return "", nil, fmt.Errorf("polza tts: empty audio in response")
	}

	mp3File := fmt.Sprintf("/tmp/tts_%s_%d.mp3", p.cfg.SessionID, time.Now().UnixNano())

	if strings.HasPrefix(result.Audio, "http://") || strings.HasPrefix(result.Audio, "https://") {
		// Audio field is a URL — download it
		audioResp, err := http.Get(result.Audio)
		if err != nil {
			return "", nil, fmt.Errorf("download audio: %w", err)
		}
		defer audioResp.Body.Close()

		if audioResp.StatusCode != 200 {
			return "", nil, fmt.Errorf("download audio HTTP %d", audioResp.StatusCode)
		}

		out, err := os.Create(mp3File)
		if err != nil {
			return "", nil, fmt.Errorf("create mp3: %w", err)
		}
		if _, err := io.Copy(out, audioResp.Body); err != nil {
			out.Close()
			return "", nil, fmt.Errorf("write mp3: %w", err)
		}
		out.Close()
	} else {
		// Audio field is base64-encoded data
		decoded, err := base64.StdEncoding.DecodeString(result.Audio)
		if err != nil {
			return "", nil, fmt.Errorf("decode base64 audio: %w", err)
		}
		if err := os.WriteFile(mp3File, decoded, 0644); err != nil {
			return "", nil, fmt.Errorf("write mp3: %w", err)
		}
	}

	out8k := mp3File[:len(mp3File)-4] + "_8k.wav"

	ffCmd := exec.Command("ffmpeg", "-y", "-i", mp3File, "-ar", "8000", "-ac", "1", "-acodec", "pcm_s16le", "-f", "wav", out8k)
	if ffOut, err := ffCmd.CombinedOutput(); err != nil {
		return "", []string{mp3File}, fmt.Errorf("ffmpeg error: %w, output: %s", err, string(ffOut))
	}

	p.logger.Printf("Polza TTS took %v", time.Since(start))
	return out8k, []string{mp3File, out8k}, nil
}
