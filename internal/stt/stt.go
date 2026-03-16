package stt

import "log"

type Transcriber interface {
	Transcribe(wavPath string) (string, error)
}

type Config struct {
	GroqAPIKey    string
	PolzaAPIKey   string
	PolzaSTTModel string
}

func New(engine string, cfg Config, logger *log.Logger) Transcriber {
	switch engine {
	case "polza":
		return NewPolzaTranscriber(cfg.PolzaAPIKey, cfg.PolzaSTTModel, logger)
	default:
		return NewGroqTranscriber(cfg.GroqAPIKey, logger)
	}
}
