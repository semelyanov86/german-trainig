package stt

import (
	"errors"
	"fmt"
	"log"
)

// fallbackTranscriber sends the audio to a second engine when the first one
// fails. The self-hosted endpoint is a single host with a single GPU and no
// provider-side model chain behind it, so it needs the same kind of safety net
// the dialog model got after the 2026-08-19 outage — only simpler, because there
// is nothing to negotiate here: an engine either answered or it did not.
type fallbackTranscriber struct {
	primary      Transcriber
	fallback     Transcriber
	primaryName  string
	fallbackName string
	logger       *log.Logger
}

func (f *fallbackTranscriber) Transcribe(wavPath string) (string, error) {
	text, err := f.primary.Transcribe(wavPath)
	if err == nil {
		// The empty transcript is included on purpose: silence is an answer, and
		// asking a hosted model to transcribe that same silence is precisely how
		// the 2026-08-19 call filled up with invented phrases.
		return text, nil
	}
	f.logger.Printf("WARN %s STT failed (%v), falling back to %s", f.primaryName, err, f.fallbackName)

	text, fallbackErr := f.fallback.Transcribe(wavPath)
	if fallbackErr != nil {
		// Both messages, because during an outage the interesting question is
		// whether the two engines failed for the same reason.
		return "", fmt.Errorf("%s stt failed (%v), %s fallback: %w",
			f.primaryName, err, f.fallbackName, fallbackErr)
	}
	f.logger.Printf("%s STT answered instead of %s", f.fallbackName, f.primaryName)
	return text, nil
}

// misconfigured stands in for an engine that cannot be built at all. Failing
// every turn keeps the mistake in the log while the fallback engine, if there is
// one, carries the call; silently substituting a different engine would hide a
// broken config behind a working call.
type misconfigured struct {
	reason string
}

func (m misconfigured) Transcribe(string) (string, error) {
	return "", errors.New(m.reason)
}
