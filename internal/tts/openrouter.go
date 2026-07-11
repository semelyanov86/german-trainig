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

	format := o.cfg.OpenRouterTTSFormat
	if format == "" {
		format = "mp3"
	}

	payload := fmt.Sprintf(`{"model":"%s","input":%q,"voice":"%s","response_format":"%s"}`,
		o.cfg.OpenRouterTTSModel, text, o.cfg.OpenRouterTTSVoice, format)

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

	base := fmt.Sprintf("/tmp/tts_%s_%d", o.cfg.SessionID, time.Now().UnixNano())
	ct := resp.Header.Get("Content-Type")

	// The endpoint returns one of three shapes depending on the model:
	//   * JSON {"audio": <base64|url>}                  (polza-style aggregators)
	//   * a container audio file with a header (mp3/…)  (OpenAI-style TTS)
	//   * raw headerless PCM, e.g. Gemini TTS returns
	//     Content-Type: audio/pcm;rate=24000;channels=1
	// Resolve each to a source file plus the ffmpeg input args needed to
	// decode it, then resample to 8kHz mono WAV for Asterisk.
	var srcFile string
	var ffInputArgs []string

	switch {
	case strings.Contains(ct, "application/json"):
		var result struct {
			Audio string `json:"audio"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", nil, fmt.Errorf("parse response: %w", err)
		}
		if result.Audio == "" {
			return "", nil, fmt.Errorf("openrouter tts: empty audio in response")
		}
		srcFile = base + ".audio"
		if strings.HasPrefix(result.Audio, "http://") || strings.HasPrefix(result.Audio, "https://") {
			audioResp, err := http.Get(result.Audio)
			if err != nil {
				return "", nil, fmt.Errorf("download audio: %w", err)
			}
			defer audioResp.Body.Close()
			if audioResp.StatusCode != 200 {
				return "", nil, fmt.Errorf("download audio HTTP %d", audioResp.StatusCode)
			}
			out, err := os.Create(srcFile)
			if err != nil {
				return "", nil, fmt.Errorf("create audio file: %w", err)
			}
			if _, err := io.Copy(out, audioResp.Body); err != nil {
				out.Close()
				return "", []string{srcFile}, fmt.Errorf("write audio: %w", err)
			}
			out.Close()
		} else {
			decoded, err := base64.StdEncoding.DecodeString(result.Audio)
			if err != nil {
				return "", nil, fmt.Errorf("decode base64 audio: %w", err)
			}
			if err := os.WriteFile(srcFile, decoded, 0644); err != nil {
				return "", nil, fmt.Errorf("write audio: %w", err)
			}
		}

	case strings.Contains(ct, "pcm") || strings.Contains(ct, "l16"):
		// Headerless signed 16-bit little-endian PCM. Rate/channels come
		// from the content type (e.g. audio/pcm;rate=24000;channels=1).
		rate, channels := pcmParams(ct)
		srcFile = base + ".pcm"
		if err := os.WriteFile(srcFile, respBody, 0644); err != nil {
			return "", nil, fmt.Errorf("write pcm: %w", err)
		}
		ffInputArgs = []string{"-f", "s16le", "-ar", rate, "-ac", channels}

	default:
		// Container audio with a header (mp3/wav/ogg); ffmpeg autodetects.
		srcFile = base + ".audio"
		if err := os.WriteFile(srcFile, respBody, 0644); err != nil {
			return "", nil, fmt.Errorf("write audio: %w", err)
		}
	}

	out8k := base + "_8k.wav"

	args := append([]string{"-y"}, ffInputArgs...)
	args = append(args, "-i", srcFile, "-ar", "8000", "-ac", "1", "-acodec", "pcm_s16le", "-f", "wav", out8k)
	ffCmd := exec.Command("ffmpeg", args...)
	if ffOut, err := ffCmd.CombinedOutput(); err != nil {
		return "", []string{srcFile}, fmt.Errorf("ffmpeg error: %w, output: %s", err, string(ffOut))
	}

	o.logger.Printf("OpenRouter TTS took %v", time.Since(start))
	return out8k, []string{srcFile, out8k}, nil
}

// pcmParams extracts the sample rate and channel count from a PCM content
// type such as "audio/pcm;rate=24000;channels=1", falling back to the Gemini
// TTS defaults (24kHz mono) when a parameter is absent.
func pcmParams(contentType string) (rate, channels string) {
	rate, channels = "24000", "1"
	for _, part := range strings.Split(contentType, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "rate":
			rate = strings.TrimSpace(kv[1])
		case "channels":
			channels = strings.TrimSpace(kv[1])
		}
	}
	return rate, channels
}
