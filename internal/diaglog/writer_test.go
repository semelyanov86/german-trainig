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

func TestPrivateProgressContainsNoPayload(t *testing.T) {
	var out bytes.Buffer
	l := log.New(Writer{Output: &out, ProfileID: "psychologist"}, "", log.LstdFlags)
	l.Println("Private session started")
	l.Println("Summary: generated 900 chars")
	l.Println("Summary: webhook sent, status 204")
	l.Println("Cleanup complete")
	l.Println("Summary: webhook sent, status 204 token=secret")
	l.Println("User said: личное сообщение")
	got := out.String()
	if strings.Count(got, "profile=psychologist:") != 4 || strings.Contains(got, "secret") || strings.Contains(got, "личное") {
		t.Fatalf("unsafe progress diagnostics: %q", got)
	}
}

func TestCallEventWhitelistRejectsAppendedOrUnknownPayloads(t *testing.T) {
	const event = "Call ended uniqueid=1790507489.16 turn=10 stage=reply_playback reason=farewell alive=true hangup_requested=true disconnect=none agi_command=playback agi_status=200 agi_result=0 agi_result_known=true duration_seconds=1220"
	for _, invalid := range []string{
		event + " token=secret",
		event + "\nUser said: private speech",
		strings.Replace(event, "reason=farewell", "reason=private_speech", 1),
		strings.Replace(event, "stage=reply_playback", "stage=https://secret.example", 1),
		strings.Replace(event, "uniqueid=1790507489.16", "uniqueid=caller_phone", 1),
		"Call started uniqueid=1790507489.16 transcript=secret",
		"Call turn uniqueid=1790507489.16 turn=10 transcript=secret",
	} {
		var out bytes.Buffer
		logger := log.New(Writer{Output: &out, ProfileID: "psychologist"}, "", log.LstdFlags)
		logger.Println(invalid)
		if got := out.String(); got != "" {
			t.Errorf("untrusted diagnostic passed the filter: %q", got)
		}
	}
}

func TestPrivateTTSFallbackNamesBothEngines(t *testing.T) {
	var out bytes.Buffer
	l := log.New(Writer{Output: &out, ProfileID: "psychologist"}, "", 0)
	l.Println("WARN TTS custom failed (custom tts HTTP 503 engine_unavailable), the rest of the call speaks through openrouter")
	l.Println("WARN TTS custom has no place for this call (custom tts timeout), the call speaks through elevenlabs")
	l.Println("WARN TTS custom rejected this reply (custom tts HTTP 400 unsupported_tag секрет), speaking it through elevenlabs")
	got := out.String()
	for _, want := range []string{
		"TTS fallback primary=custom fallback=openrouter outcome=switch kind=http HTTP=503",
		"TTS fallback primary=custom fallback=elevenlabs outcome=no_place kind=timeout",
		"TTS fallback primary=custom fallback=elevenlabs outcome=reply kind=http HTTP=400",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "секрет") || strings.Contains(got, "unsupported_tag") {
		t.Fatalf("payload leaked: %q", got)
	}
}
