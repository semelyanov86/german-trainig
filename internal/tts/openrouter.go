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

const openRouterTTSEndpoint = "https://openrouter.ai/api/v1/audio/speech"

type OpenRouterSynth struct {
	cfg    Config
	logger *log.Logger
}

func (o *OpenRouterSynth) Synthesize(text string) (string, []string, error) {
	start := time.Now()

	payload := fmt.Sprintf(`{"model":"%s","input":%q,"voice":"%s","response_format":"mp3"}`,
		o.cfg.OpenRouterTTSModel, text, o.cfg.OpenRouterTTSVoice)

	req, err := http.NewRequest("POST", openRouterTTSEndpoint, strings.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.cfg.OpenRouterAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("openrouter tts request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("openrouter tts HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	mp3File := fmt.Sprintf("/tmp/tts_%s_%d.mp3", o.cfg.SessionID, time.Now().UnixNano())

	// The endpoint may return either JSON {"audio": <base64|url>} (polza-style)
	// or raw audio bytes (OpenAI-style). Branch on the response content type.
	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		var result struct {
			Audio string `json:"audio"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", nil, fmt.Errorf("parse response: %w", err)
		}
		if result.Audio == "" {
			return "", nil, fmt.Errorf("openrouter tts: empty audio in response")
		}

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
	} else {
		// Raw audio bytes in the response body
		if err := os.WriteFile(mp3File, respBody, 0644); err != nil {
			return "", nil, fmt.Errorf("write mp3: %w", err)
		}
	}

	out8k := mp3File[:len(mp3File)-4] + "_8k.wav"

	ffCmd := exec.Command("ffmpeg", "-y", "-i", mp3File, "-ar", "8000", "-ac", "1", "-acodec", "pcm_s16le", "-f", "wav", out8k)
	if ffOut, err := ffCmd.CombinedOutput(); err != nil {
		return "", []string{mp3File}, fmt.Errorf("ffmpeg error: %w, output: %s", err, string(ffOut))
	}

	o.logger.Printf("OpenRouter TTS took %v", time.Since(start))
	return out8k, []string{mp3File, out8k}, nil
}
