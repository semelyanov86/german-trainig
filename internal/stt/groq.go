package stt

import "time"

const (
	groqEndpoint = "https://api.groq.com/openai/v1/audio/transcriptions"
	groqModel    = "whisper-large-v3"
)

// groqSpec describes Groq's hosted Whisper. Model and timeout are fixed: this
// backend predates the per-engine config keys and nothing selects them.
func groqSpec(cfg Config) sttSpec {
	return sttSpec{
		name:     "Groq",
		endpoint: groqEndpoint,
		apiKey:   cfg.GroqAPIKey,
		model:    groqModel,
		language: "de",
		timeout:  30 * time.Second,
	}
}
