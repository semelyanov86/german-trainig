package stt

import "time"

const openRouterSTTEndpoint = "https://openrouter.ai/api/v1/audio/transcriptions"

// openRouterSpec describes openrouter.ai's OpenAI-compatible transcription
// endpoint. Same request and response shape as polza.
func openRouterSpec(cfg Config) sttSpec {
	model := cfg.OpenRouterSTTModel
	if model == "" {
		model = "openai/gpt-4o-transcribe"
	}
	return sttSpec{
		name:     "OpenRouter",
		endpoint: openRouterSTTEndpoint,
		apiKey:   cfg.OpenRouterAPIKey,
		model:    model,
		language: language(cfg),
		timeout:  60 * time.Second,
	}
}
