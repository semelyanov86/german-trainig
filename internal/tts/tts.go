package tts

import (
	"fmt"
	"log"
	"strings"
)

type Synthesizer interface {
	Synthesize(text string) (wavPath string, tempFiles []string, err error)
}

type Config struct {
	SessionID           string
	ElevenAPIKey        string
	ElevenVoiceID       string
	ElevenModel         string
	OpenAIAPIKey        string
	OpenAIModel         string
	OpenAIVoice         string
	PiperModel          string
	PolzaAPIKey         string
	PolzaTTSModel       string
	PolzaTTSVoice       string
	OpenRouterAPIKey    string
	OpenRouterTTSModel  string
	OpenRouterTTSVoice  string
	OpenRouterTTSFormat string
	StyleTags           string
	LogUtterances       bool
}

// New builds the synthesizer for an engine together with the expression-tag
// dialect its model understands. The dialect is returned because it is needed
// in two places: the caller appends its Guide to the tutor system prompt so the
// model writes the right markup, and the synthesizer filters every reply
// through it so markup the engine cannot read is never spoken out loud.
func New(engine string, cfg Config, logger *log.Logger) (Synthesizer, Dialect) {
	dialect := DialectFor(engine, cfg, logger)
	return &styled{backend: newBackend(engine, cfg, logger), dialect: dialect, logger: logger, logUtterances: cfg.LogUtterances}, dialect
}

func newBackend(engine string, cfg Config, logger *log.Logger) Synthesizer {
	switch engine {
	case "elevenlabs":
		return &ElevenLabsSynth{cfg: cfg, logger: logger}
	case "openai":
		return &OpenAISynth{cfg: cfg, logger: logger}
	case "polza":
		return &PolzaSynth{cfg: cfg, logger: logger}
	case "openrouter":
		return &OpenRouterSynth{cfg: cfg, logger: logger}
	default:
		return &PiperSynth{cfg: cfg, logger: logger}
	}
}

// styled filters the reply through the dialect before handing it to the
// backend. It sits in front of every engine, not only the ones with tags: the
// model writes markdown and stage directions of its own accord (`*wirklich*`,
// `[lacht]`) however plainly the prompt forbids them, and every one of those
// reaches the caller as spoken punctuation.
type styled struct {
	backend       Synthesizer
	dialect       Dialect
	logger        *log.Logger
	logUtterances bool
}

func (s *styled) Synthesize(text string) (string, []string, error) {
	clean := s.dialect.Sanitize(text)
	if clean != strings.TrimSpace(text) {
		if s.logUtterances {
			s.logger.Printf("TTS: cleaned style markup (%s): %q -> %q", s.dialect.Name, text, clean)
		} else {
			s.logger.Printf("TTS: cleaned style markup (%s)", s.dialect.Name)
		}
	}
	if clean == "" {
		return "", nil, fmt.Errorf("tts: nothing left to speak")
	}
	return s.backend.Synthesize(clean)
}
