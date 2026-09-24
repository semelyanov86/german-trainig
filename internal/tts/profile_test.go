package tts

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestGuideLanguage(t *testing.T) {
	d := grokDialect()
	if d.GuideFor("de") != d.Guide {
		t.Fatal("German guide changed")
	}
	ru := d.GuideFor("ru")
	if !strings.Contains(ru, "по-русски") || strings.Contains(ru, "Deine Antwort") {
		t.Fatalf("Russian guide is not localized: %q", ru)
	}
	if got := d.Sanitize("[laugh] Привет [unknown]!"); got != "[laugh] Привет!" {
		t.Fatalf("shared sanitizer: %q", got)
	}
}

type captureSynth struct{ got string }

func (s *captureSynth) Synthesize(text string) (string, []string, error) {
	s.got = text
	return "audio.wav", nil, nil
}

func TestPrivateStyleCleanupDoesNotLogSpeech(t *testing.T) {
	var out bytes.Buffer
	backend := &captureSynth{}
	s := styled{backend: backend, dialect: Dialect{Name: "off"}, logger: log.New(&out, "", 0), logUtterances: false}
	if _, _, err := s.Synthesize("[unknown] личный разговор"); err != nil {
		t.Fatal(err)
	}
	if backend.got != "личный разговор" || strings.Contains(out.String(), "личный разговор") {
		t.Fatalf("style cleanup leaked speech: backend=%q log=%q", backend.got, out.String())
	}
}
