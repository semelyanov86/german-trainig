package stt

import "strings"

// customSpec describes an endpoint given entirely by config: URL, key, model,
// language and timeout. Nothing about it is compiled in, so switching to another
// whisper host — or to any OpenAI/groq-compatible service — is a config edit.
//
// In production this is a whisper.cpp + ggml-large-v3 server with silero VAD in
// front of the decoder. Measured against openrouter on seven real call segments
// (2026-08-20) it ties on speed (1.6-5.5s vs 1.7-4.9s) and wins where it counts:
// silence transcribes to "", instead of the invented Korean and Hindi phrases
// that turned the 2026-08-19 call into a spin of fake turns.
func customSpec(cfg Config) sttSpec {
	spec := sttSpec{
		name:     "Custom",
		endpoint: strings.TrimSpace(cfg.CustomSTTURL),
		apiKey:   strings.TrimSpace(cfg.CustomSTTAPIKey),
		model:    strings.TrimSpace(cfg.CustomSTTModel),
		language: strings.TrimSpace(cfg.CustomSTTLanguage),
		timeout:  cfg.CustomSTTTimeout, // 0 falls back to defaultTimeout
		// One quick retry: unlike the hosted engines, this endpoint is a single
		// host with a single GPU and no provider-side model chain to absorb a
		// blip, so ~300ms is worth spending before handing the turn to another
		// engine entirely.
		retryOnce: true,
	}
	// "auto" means "let the endpoint decide", which is expressed by omitting the
	// field: the self-hosted server answers 400 to language=auto.
	if strings.EqualFold(spec.language, "auto") {
		spec.language = ""
	}
	return spec
}
