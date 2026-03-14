package tts

import "log"

type Synthesizer interface {
	Synthesize(text string) (wavPath string, tempFiles []string, err error)
}

type Config struct {
	SessionID     string
	ElevenAPIKey  string
	ElevenVoiceID string
	ElevenModel   string
	PiperModel    string
}

func New(engine string, cfg Config, logger *log.Logger) Synthesizer {
	if engine == "elevenlabs" {
		return &ElevenLabsSynth{cfg: cfg, logger: logger}
	}
	return &PiperSynth{cfg: cfg, logger: logger}
}
