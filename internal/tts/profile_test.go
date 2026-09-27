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

func TestElevenLabsProfileAudioTags(t *testing.T) {
	const tagged = "[warmly] Я понимаю. [thoughtful] Что сейчас труднее всего?"
	const plain = "Я понимаю. Что сейчас труднее всего?"
	for _, tc := range []struct {
		model string
		tags  bool
	}{
		{model: "eleven_v3_conversational", tags: true},
		{model: "eleven_v3", tags: true},
		{model: "eleven_flash_v2_5", tags: false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			var out bytes.Buffer
			logger := log.New(&out, "", 0)
			synth, dialect := New("elevenlabs", Config{ElevenModel: tc.model, StyleTags: "auto"}, logger)
			if dialect.Supported() != tc.tags {
				t.Fatalf("audio tag support: got %v, want %v", dialect.Supported(), tc.tags)
			}
			backend := &captureSynth{}
			synth.(*styled).backend = backend
			if _, _, err := synth.Synthesize(tagged); err != nil {
				t.Fatal(err)
			}
			want := plain
			if tc.tags {
				want = tagged
				if guide := dialect.GuideFor("ru"); !strings.Contains(guide, "по-русски") || !strings.Contains(guide, "[warmly]") {
					t.Fatalf("missing Russian audio tag guidance: %q", guide)
				}
			}
			if backend.got != want {
				t.Fatalf("synthesis input: got %q, want %q", backend.got, want)
			}
			if got := PlainText(tagged); got != plain {
				t.Fatalf("transcript contains markup: %q", got)
			}
		})
	}
}
