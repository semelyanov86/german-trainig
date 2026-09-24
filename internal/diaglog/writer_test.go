package diaglog

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestPrivateLogKeepsFailureWithoutConversationOrSecrets(t *testing.T) {
	var out bytes.Buffer
	l := log.New(Writer{Output: &out}, "", 0)
	l.Println("User said: очень личный разговор")
	l.Println("Tutor: секретная реплика")
	l.Println("TTS: cleaned style markup: секретная реплика")
	l.Println("WARN OpenRouter attempt failed: HTTP 429: token=topsecret body=очень личный разговор")
	got := out.String()
	if !strings.Contains(got, "WARN provider=OpenRouter kind=http HTTP=429") {
		t.Fatalf("provider diagnostic lost: %q", got)
	}
	for _, private := range []string{"очень личный", "секретная", "topsecret", "token="} {
		if strings.Contains(got, private) {
			t.Errorf("private content leaked: %q", got)
		}
	}
}

func TestSafeFailureKinds(t *testing.T) {
	var out bytes.Buffer
	l := log.New(Writer{Output: &out, ProfileID: "support"}, "", 0)
	l.Println("ERROR Custom STT: context deadline exceeded; transcript=секрет")
	l.Println("WARN Polza connection refused at https://secret.example")
	l.Println("ERROR OpenRouter cannot parse reply: секрет")
	got := out.String()
	for _, kind := range []string{"kind=timeout", "kind=network", "kind=schema"} {
		if !strings.Contains(got, kind) {
			t.Errorf("missing %s in %q", kind, got)
		}
	}
	if strings.Contains(got, "секрет") || strings.Contains(got, "secret.example") {
		t.Fatalf("private error payload leaked: %q", got)
	}
}
