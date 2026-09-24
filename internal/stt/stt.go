package stt

import (
	"log"
	"strings"
	"time"
)

type Transcriber interface {
	Transcribe(wavPath string) (string, error)
}

type Config struct {
	Language           string // hosted engines; defaults to German for legacy callers
	GroqAPIKey         string
	PolzaAPIKey        string
	PolzaSTTModel      string
	OpenRouterAPIKey   string
	OpenRouterSTTModel string

	// The "custom" engine: any OpenAI-compatible audio/transcriptions endpoint,
	// described entirely by these fields so that moving to another server or
	// model needs no code change. See customSpec.
	CustomSTTURL      string
	CustomSTTAPIKey   string
	CustomSTTModel    string
	CustomSTTLanguage string
	CustomSTTTimeout  time.Duration

	// FallbackEngine answers when the primary engine fails; empty disables the
	// fallback. FallbackTimeout caps whichever engine serves in that role,
	// independent of the timeout that engine uses as a primary.
	FallbackEngine  string
	FallbackTimeout time.Duration
}

func language(cfg Config) string {
	if cfg.Language == "" {
		return "de"
	}
	if strings.EqualFold(cfg.Language, "auto") {
		return ""
	}
	return cfg.Language
}

// New builds the transcriber for the configured engine, wrapped in a fallback to
// a second engine when one is configured.
func New(engine string, cfg Config, logger *log.Logger) Transcriber {
	fallbackEngine := strings.TrimSpace(cfg.FallbackEngine)
	primary, _ := specFor(strings.TrimSpace(engine), cfg)
	fallback, known := specFor(fallbackEngine, cfg)

	switch {
	case fallbackEngine == "":
		// No fallback configured.
	case !known:
		// A typo must not quietly send the caller's audio to a provider nobody
		// chose — the point of the self-hosted engine is that the audio stays on
		// our own German server.
		logger.Printf("ERROR unknown STT_FALLBACK_ENGINE %q, fallback disabled", fallbackEngine)
	case fallback.name == primary.name:
		// Compared by resolved engine, not by config string: an unknown value
		// (and an unset STT_ENGINE) resolves to Groq, so "groq" as the fallback
		// of an unset primary would otherwise wrap an engine around itself.
		logger.Printf("WARN STT fallback engine resolves to the primary one (%s), fallback disabled", primary.name)
	default:
		return newFallback(primary, fallback, cfg, logger)
	}
	logger.Printf("STT engine: %s", primary.name)
	return build(primary, logger)
}

// newFallback pairs the primary engine with the one that answers when it fails.
func newFallback(primary, fallback sttSpec, cfg Config, logger *log.Logger) Transcriber {
	if cfg.FallbackTimeout > 0 {
		fallback.timeout = cfg.FallbackTimeout
	}
	// The fallback runs after the primary has already spent its budget, so it
	// never adds retries of its own.
	fallback.retryOnce = false

	logger.Printf("STT engine: %s (fallback: %s)", primary.name, fallback.name)
	return &fallbackTranscriber{
		primary:      build(primary, logger),
		fallback:     build(fallback, logger),
		primaryName:  primary.name,
		fallbackName: fallback.name,
		logger:       logger,
	}
}

// specFor maps an STT_ENGINE value to its endpoint description. The bool reports
// whether the value was actually recognised: an unknown *primary* engine selects
// Groq, as it always has, while an unknown fallback is refused by the caller.
func specFor(engine string, cfg Config) (sttSpec, bool) {
	switch engine {
	case "polza":
		return polzaSpec(cfg), true
	case "openrouter":
		return openRouterSpec(cfg), true
	case "custom":
		return customSpec(cfg), true
	case "groq":
		return groqSpec(cfg), true
	default:
		return groqSpec(cfg), false
	}
}

// build turns a spec into a Transcriber. Only the custom engine can end up
// without an endpoint, and that is a config error worth reporting rather than
// papering over.
func build(spec sttSpec, logger *log.Logger) Transcriber {
	if spec.endpoint == "" {
		logger.Printf("ERROR STT engine %s has no endpoint: CUSTOM_STT_URL is not set", spec.name)
		return misconfigured{reason: "CUSTOM_STT_URL is not set"}
	}
	return newHTTPTranscriber(spec, logger)
}
