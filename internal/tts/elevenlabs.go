package tts

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type ElevenLabsSynth struct {
	cfg    Config
	logger *log.Logger
}

func (e *ElevenLabsSynth) Synthesize(text string) (string, []string, error) {
	start := time.Now()

	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", e.cfg.ElevenVoiceID)
	payload := fmt.Sprintf(`{"text":%q,"model_id":"%s"}`, text, e.cfg.ElevenModel)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("xi-api-key", e.cfg.ElevenAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("elevenlabs request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("elevenlabs HTTP %d: %s", resp.StatusCode, string(body))
	}

	mp3File := fmt.Sprintf("/tmp/tts_%s_%d.mp3", e.cfg.SessionID, time.Now().UnixNano())
	out, err := os.Create(mp3File)
	if err != nil {
		return "", nil, fmt.Errorf("create mp3: %w", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return "", nil, fmt.Errorf("write mp3: %w", err)
	}
	out.Close()

	out8k := mp3File[:len(mp3File)-4] + "_8k.wav"

	ffCmd := exec.Command("ffmpeg", "-y", "-i", mp3File, "-ar", "8000", "-ac", "1", "-acodec", "pcm_s16le", "-f", "wav", out8k)
	if ffOut, err := ffCmd.CombinedOutput(); err != nil {
		return "", []string{mp3File}, fmt.Errorf("ffmpeg error: %w, output: %s", err, string(ffOut))
	}

	e.logger.Printf("ElevenLabs TTS took %v", time.Since(start))
	return out8k, []string{mp3File, out8k}, nil
}
