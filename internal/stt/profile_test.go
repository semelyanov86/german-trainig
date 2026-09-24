package stt

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"german-trainer/internal/diaglog"
)

func TestProfileLanguageForEveryEngine(t *testing.T) {
	for _, lang := range []string{"de", "ru"} {
		cfg := Config{Language: lang, CustomSTTLanguage: lang, CustomSTTURL: "http://localhost/transcribe"}
		for _, engine := range []string{"groq", "polza", "openrouter", "custom"} {
			spec, ok := specFor(engine, cfg)
			if !ok || spec.language != lang {
				t.Errorf("%s language=%q, want %q", engine, spec.language, lang)
			}
		}
	}
	if customSpec(Config{CustomSTTLanguage: "auto"}).language != "" {
		t.Fatal("custom auto must omit language")
	}
	if language(Config{}) != "de" {
		t.Fatal("legacy hosted STT language changed")
	}
	if language(Config{Language: "auto"}) != "" {
		t.Fatal("hosted auto must omit language")
	}
}

func TestEmptyPrimaryTranscriptDoesNotCallFallback(t *testing.T) {
	primary := &stubTranscriber{text: ""}
	secondary := &stubTranscriber{text: "invented"}
	f := fallbackTranscriber{primary: primary, fallback: secondary, primaryName: "Custom", fallbackName: "OpenRouter", logger: log.New(io.Discard, "", 0)}
	text, err := f.Transcribe("unused")
	if err != nil || text != "" || secondary.calls != 0 {
		t.Fatalf("empty result reached hosted fallback: %q %v calls=%d", text, err, secondary.calls)
	}
}

type stubTranscriber struct {
	text  string
	err   error
	calls int
}

func (s *stubTranscriber) Transcribe(string) (string, error) { s.calls++; return s.text, s.err }

func TestPrivateFallbackLogNamesActualAnsweringEngine(t *testing.T) {
	var out bytes.Buffer
	primary := &stubTranscriber{err: errors.New("HTTP 503 token=secret transcript=личное")}
	secondary := &stubTranscriber{text: "личное"}
	f := fallbackTranscriber{
		primary: primary, fallback: secondary,
		primaryName: "Custom", fallbackName: "OpenRouter",
		logger: log.New(diaglog.Writer{Output: &out, ProfileID: "support"}, "", 0),
	}
	got, err := f.Transcribe("unused")
	if err != nil || got != "личное" {
		t.Fatalf("fallback failed: %q %v", got, err)
	}
	logText := out.String()
	if !strings.Contains(logText, "primary=Custom fallback=OpenRouter outcome=attempt kind=http HTTP=503") || !strings.Contains(logText, "primary=Custom fallback=OpenRouter outcome=answered") {
		t.Fatalf("fallback result not observable: %q", logText)
	}
	if strings.Contains(logText, "личное") || strings.Contains(logText, "secret") {
		t.Fatalf("private text leaked: %q", logText)
	}
}

func TestMultipartLanguage(t *testing.T) {
	wav := filepath.Join(t.TempDir(), "voice.wav")
	if err := os.WriteFile(wav, []byte("wav"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"de", "ru", "auto"} {
		spec := customSpec(Config{CustomSTTLanguage: lang})
		body, _, err := buildForm(wav, spec)
		if err != nil {
			t.Fatal(err)
		}
		hasLanguage := bytes.Contains(body, []byte(`name="language"`))
		if hasLanguage != (lang != "auto") {
			t.Errorf("language field for %s: %v", lang, hasLanguage)
		}
		if lang != "auto" && !strings.Contains(string(body), "\r\n"+lang+"\r\n") {
			t.Errorf("wrong language in form for %s", lang)
		}
	}
}
