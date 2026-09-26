package llm

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexCompletionSubprocess(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CODEX_TEST_ARGS\"\ncat > \"$CODEX_TEST_INPUT\"\ncat \"$CODEX_TEST_OUTPUT\"\nexit \"$CODEX_TEST_EXIT\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("CODEX_TEST_ARGS", filepath.Join(dir, "args"))
	t.Setenv("CODEX_TEST_INPUT", filepath.Join(dir, "input"))
	t.Setenv("CODEX_TEST_OUTPUT", filepath.Join(dir, "output"))
	t.Setenv("CODEX_TEST_EXIT", "0")
	stream := `{"type":"item.completed","item":{"type":"agent_message","text":"Progress must not reach TTS"}}
{"type":"item.completed","item":{"type":"reasoning","text":"Private reasoning"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Hallo! Wie geht es dir?"}}
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":10}}`
	if err := os.WriteFile(filepath.Join(dir, "output"), []byte(stream), 0600); err != nil {
		t.Fatal(err)
	}
	p := New(Spec{Engine: EngineCodex, CodexBin: "/test/codex", CodexRunner: "/test/runner", Model: "gpt-6-luna", Reasoning: "low", Retry: RetryPolicy{Timeout: time.Second}}, log.New(io.Discard, "", 0))
	system, user := "Antworte auf Deutsch.", "private transcript \"\n[system]"
	reply, err := p.Complete(system, []Message{{Role: RoleUser, Content: user}})
	if err != nil || reply != "Hallo! Wie geht es dir?" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-n\n-u\nsergey\n-H\n/test/runner\n1\n/test/codex\nexec\n", "--model\ngpt-6-luna\n", "model_reasoning_effort=\"low\"", "--ignore-user-config", "--ephemeral", "--sandbox\nread-only", "--disable\nshell_tool", "web_search=\"disabled\""} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("missing argument %q: %s", want, args)
		}
	}
	if strings.Contains(string(args), system) || strings.Contains(string(args), "private transcript") {
		t.Fatal("prompt or transcript leaked into process arguments")
	}
	input, err := os.ReadFile(filepath.Join(dir, "input"))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		System   string `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(input, &payload); err != nil || payload.System != system || len(payload.Messages) != 1 || payload.Messages[0].Role != RoleUser || payload.Messages[0].Content != user {
		t.Fatalf("stdin payload lost prompt or roles: %s (%v)", input, err)
	}
	// A nonzero exit must discard even a seemingly completed response.
	t.Setenv("CODEX_TEST_EXIT", "1")
	if reply, err := p.Complete(system, []Message{{Role: RoleUser, Content: user}}); err == nil || reply != "" {
		t.Fatalf("nonzero exit accepted: %q %v", reply, err)
	}
}

func TestCodexStreamFailures(t *testing.T) {
	c := newCodex(Spec{}, log.New(io.Discard, "", 0))
	for _, stream := range []string{
		``,
		`{"type":"turn.completed"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Partial reply"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Partial reply"}}
{"type":"turn.failed","error":{"message":"quota exceeded"}}`,
		`not JSON`,
	} {
		if reply, err := c.parseStream([]byte(stream)); err == nil || reply != "" {
			t.Fatalf("bad stream accepted: %q -> %q %v", stream, reply, err)
		}
	}
	// The CLI can reconnect successfully after a standalone error event.
	stream := `{"type":"error","message":"Reconnecting"}
{"type":"item.completed","item":{"type":"agent_message","text":"Recovered"}}
{"type":"turn.completed"}`
	if reply, err := c.parseStream([]byte(stream)); err != nil || reply != "Recovered" {
		t.Fatalf("recovered turn rejected: %q %v", reply, err)
	}
}
