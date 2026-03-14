package tts

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

type PiperSynth struct {
	cfg    Config
	logger *log.Logger
}

func (p *PiperSynth) Synthesize(text string) (string, []string, error) {
	start := time.Now()

	outFile := fmt.Sprintf("/tmp/tts_%s_%d.wav", p.cfg.SessionID, time.Now().UnixNano())

	cmd := exec.Command("piper", "--model", p.cfg.PiperModel, "--output_file", outFile)
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("piper error: %w, output: %s", err, string(out))
	}

	out8k := outFile[:len(outFile)-4] + "_8k.wav"

	ffCmd := exec.Command("ffmpeg", "-y", "-i", outFile, "-ar", "8000", "-ac", "1", "-acodec", "pcm_s16le", "-f", "wav", out8k)
	if out, err := ffCmd.CombinedOutput(); err != nil {
		return "", []string{outFile}, fmt.Errorf("ffmpeg error: %w, output: %s", err, string(out))
	}

	p.logger.Printf("Piper TTS took %v", time.Since(start))
	return out8k, []string{outFile, out8k}, nil
}
