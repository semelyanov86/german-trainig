package tts

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

type Synthesizer interface {
	Synthesize(text string) (wavPath string, tempFiles []string, err error)
}

type Config struct {
	SessionID           string
	TempDir             string
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
	YandexToken         string
	YandexAuthType      string
	YandexFolderID      string
	YandexTTSModel      string
	YandexTTSVoice      string
	YandexTTSRole       string
	StyleTags           string
	LogUtterances       bool

	// The custom engine (our Higgs server) and the fallback behind it.
	CallID         string // Asterisk uniqueid, names the call's place on the server
	CustomURL      string
	CustomToken    string
	CustomVoice    string
	CustomLanguage string
	FallbackEngine string
}

func (c Config) audioBase() string {
	dir := c.TempDir
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tts_%s_%d", c.SessionID, time.Now().UnixNano()))
}

// New builds the synthesizer for an engine together with the expression-tag
// dialect its model understands. The dialect is returned because it is needed
// in two places: the caller appends its Guide to the tutor system prompt so the
// model writes the right markup, and the synthesizer filters every reply
// through it so markup the engine cannot read is never spoken out loud.
func New(engine string, cfg Config, logger *log.Logger) (Synthesizer, Dialect) {
	return newStyled(engine, cfg, logger)
}

func newStyled(engine string, cfg Config, logger *log.Logger) (*styled, Dialect) {
	dialect := DialectFor(engine, cfg, logger)
	return &styled{backend: newBackend(engine, cfg, logger), dialect: dialect, logger: logger, logUtterances: cfg.LogUtterances}, dialect
}

// fileEngines are the engines newBackend builds; anything else becomes Piper.
var fileEngines = map[string]bool{
	"elevenlabs": true, "openai": true, "polza": true, "openrouter": true, "yandex": true, "piper": true,
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
	case "yandex":
		return &YandexSynth{cfg: cfg, logger: logger}
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
	clean, err := cleanReply(s.dialect, text, s.logger, s.logUtterances)
	if err != nil {
		return "", nil, err
	}
	return s.backend.Synthesize(clean)
}

// speak adapts a file engine to the Speaker contract: one finished file.
func (s *styled) speak(text string) (Audio, error) {
	path, files, err := s.Synthesize(text)
	if err != nil {
		return &fileAudio{files: files}, err
	}
	return &fileAudio{path: path, files: files}, nil
}

// errNothingToSpeak means the filter removed the whole reply. Another engine
// would have nothing to say either, so it never triggers the fallback.
var errNothingToSpeak = errors.New("tts: nothing left to speak")

// cleanReply filters a reply through the dialect before it reaches any engine.
func cleanReply(d Dialect, text string, logger *log.Logger, logUtterances bool) (string, error) {
	clean := d.Sanitize(text)
	if clean != strings.TrimSpace(text) {
		if logUtterances {
			logger.Printf("TTS: cleaned style markup (%s): %q -> %q", d.Name, text, clean)
		} else {
			logger.Printf("TTS: cleaned style markup (%s)", d.Name)
		}
	}
	if clean == "" {
		return "", errNothingToSpeak
	}
	return clean, nil
}
