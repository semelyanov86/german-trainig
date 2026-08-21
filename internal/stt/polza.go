package stt

import "time"

const polzaSTTEndpoint = "https://polza.ai/api/v1/audio/transcriptions"

// polzaSpec describes polza.ai's OpenAI-compatible transcription endpoint.
func polzaSpec(cfg Config) sttSpec {
	model := cfg.PolzaSTTModel
	if model == "" {
		model = "openai/gpt-4o-transcribe"
	}
	return sttSpec{
		name:     "Polza",
		endpoint: polzaSTTEndpoint,
		apiKey:   cfg.PolzaAPIKey,
		model:    model,
		language: "de",
		timeout:  60 * time.Second,
	}
}
