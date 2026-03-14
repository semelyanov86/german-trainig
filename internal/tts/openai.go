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

type OpenAISynth struct {
	cfg    Config
	logger *log.Logger
}

func (o *OpenAISynth) Synthesize(text string) (string, []string, error) {
	start := time.Now()

	payload := fmt.Sprintf(`{"model":"%s","input":%q,"voice":"%s","response_format":"mp3"}`,
		o.cfg.OpenAIModel, text, o.cfg.OpenAIVoice)

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/audio/speech", strings.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.cfg.OpenAIAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("openai tts request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("openai tts HTTP %d: %s", resp.StatusCode, string(body))
	}

	mp3File := fmt.Sprintf("/tmp/tts_%s_%d.mp3", o.cfg.SessionID, time.Now().UnixNano())
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

	o.logger.Printf("OpenAI TTS took %v", time.Since(start))
	return out8k, []string{mp3File, out8k}, nil
}
