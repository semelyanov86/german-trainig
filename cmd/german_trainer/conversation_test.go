package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"german-trainer/internal/config"
)

func TestGoodbyeDoesNotHangUp(t *testing.T) {
	for _, tc := range []struct {
		profile, language, mode, phrases string
		utterances                       []string
	}{
		{"german", "de", "phrase", "tschuss,tschüss,auf wiedersehen", []string{
			"Aber wie gesagt, unter Kontrolle um Weltschuss bringen und nicht Kontrolle von Bern oder etwas.",
			"Okay, tschüss!", "Auf Wiedersehen!",
		}},
		{"psychologist", "ru", "utterance", "до свидания,спасибо до свидания", []string{
			"До свидания!", "Спасибо, до свидания.",
		}},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			dir := t.TempDir()
			prompt := filepath.Join(dir, "prompt.md")
			if err := os.WriteFile(prompt, []byte("Respond to the caller."), 0600); err != nil {
				t.Fatal(err)
			}
			// Exercise the real factories without any network or speech binaries.
			for _, name := range []string{"piper", "ffmpeg"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nfor output; do :; done\n: > \"$output\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			base := filepath.Join(dir, ".env")
			settings := fmt.Sprintf("PROFILE_IDS=psychologist\nHISTORY_DIR=%s\nLLM_ENGINE=openrouter\nSTT_ENGINE=custom\nCUSTOM_STT_URL=https://stt.invalid/transcribe\nTTS_ENGINE=piper\n", dir)
			// Old production settings must load, but must not restore auto-hangup.
			scenario := fmt.Sprintf("LANGUAGE=%s\nSKILL_FILE=%s\nGREETING_PROMPT=Hello\nSILENCE_PROMPT=Speak\nSILENCE_LINE=Speak\nGLITCH_LINE=Repeat\nOUTAGE_LINE=Unavailable\nHISTORY_ROLE=Assistant\nSUMMARY_ENABLED=false\nLOG_UTTERANCES=false\nFAREWELL_MODE=%s\nFAREWELL_PHRASES=%s\nFAREWELL_LINE=Goodbye\n", tc.language, prompt, tc.mode, tc.phrases)
			if err := os.WriteFile(base, []byte(settings+scenario), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.profile != "german" {
				if err := os.Mkdir(filepath.Join(dir, "profiles"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "profiles", tc.profile+".env"), []byte(scenario), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := config.LoadProfile(base, tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			transcriptions := 0
			var modelInputs []string
			originalTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			http.DefaultTransport = conversationTransport(func(req *http.Request) (*http.Response, error) {
				var body []byte
				switch {
				case req.URL.Path == "/transcribe":
					if transcriptions >= len(tc.utterances) {
						t.Fatal("unexpected transcription after caller hangup")
					}
					body, _ = json.Marshal(map[string]string{"text": tc.utterances[transcriptions]})
					transcriptions++
				case strings.HasSuffix(req.URL.Path, "/chat/completions"):
					var payload struct{ Messages []struct{ Content string } }
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload.Messages) == 0 {
						t.Fatalf("invalid model request: %v", err)
					}
					modelInputs = append(modelInputs, payload.Messages[len(payload.Messages)-1].Content)
					body = []byte(`{"choices":[{"message":{"content":"Test reply."}}]}`)
				default:
					t.Fatalf("unexpected request: %s", req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
			})
			channel := &conversationChannel{t: t, utterances: len(tc.utterances)}
			channel.input.WriteString("agi_uniqueid: 123.1\n\n")
			var events bytes.Buffer
			runCall(cfg, log.New(&events, "", 0), channel, channel)
			if channel.hangupRequested || channel.recordings != len(tc.utterances)+1 || channel.playbacks != len(tc.utterances)+1 {
				t.Fatalf("call ended before caller hangup: recordings=%d playbacks=%d hangup_requested=%t\n%s", channel.recordings, channel.playbacks, channel.hangupRequested, events.String())
			}
			if len(modelInputs) != len(tc.utterances)+1 {
				t.Fatalf("model turns=%d, want %d", len(modelInputs), len(tc.utterances)+1)
			}
			for i, text := range tc.utterances {
				if !strings.Contains(modelInputs[i+1], text) {
					t.Errorf("utterance %q did not reach the model", text)
				}
			}
			if !strings.Contains(events.String(), "reason=channel_closed") || !strings.Contains(events.String(), "disconnect=asterisk_hangup") {
				t.Fatalf("caller hangup was not handled: %s", events.String())
			}
		})
	}
}

type conversationTransport func(*http.Request) (*http.Response, error)

func (f conversationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type conversationChannel struct {
	t                                 *testing.T
	input                             bytes.Buffer
	utterances, recordings, playbacks int
	hangupRequested                   bool
}

func (c *conversationChannel) Read(p []byte) (int, error) { return c.input.Read(p) }

func (c *conversationChannel) Write(p []byte) (int, error) {
	command := strings.TrimSpace(string(p))
	reply := "200 result=0\n"
	switch {
	case strings.HasPrefix(command, "RECORD FILE "):
		c.recordings++
		if c.recordings > c.utterances {
			reply = "HANGUP\n200 result=-1 endpos=0\n"
		} else {
			path := strings.Fields(command)[2] + ".wav"
			if err := os.WriteFile(path, []byte("test audio"), 0600); err != nil {
				c.t.Fatal(err)
			}
			reply = "200 result=0 endpos=16000\n"
		}
	case strings.HasPrefix(command, "STREAM FILE "):
		c.playbacks++
	case command == "HANGUP":
		c.hangupRequested = true
	}
	c.input.WriteString(reply)
	return len(p), nil
}
