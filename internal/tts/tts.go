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
	OpenAIAPIKey  string
	OpenAIModel   string
	OpenAIVoice   string
	PiperModel    string
	PolzaAPIKey   string
	PolzaTTSModel string
	PolzaTTSVoice string
}

func New(engine string, cfg Config, logger *log.Logger) Synthesizer {
	switch engine {
	case "elevenlabs":
		return &ElevenLabsSynth{cfg: cfg, logger: logger}
	case "openai":
		return &OpenAISynth{cfg: cfg, logger: logger}
	case "polza":
		return &PolzaSynth{cfg: cfg, logger: logger}
	default:
		return &PiperSynth{cfg: cfg, logger: logger}
	}
}
