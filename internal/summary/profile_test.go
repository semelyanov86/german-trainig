package summary

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"german-trainer/internal/llm"
)

type reportProvider struct {
	system, input string
	calls         int
}

func (p *reportProvider) Complete(system string, messages []llm.Message) (string, error) {
	p.calls++
	p.system, p.input = system, messages[0].Content
	return "отчёт", nil
}

func TestProfileSummaryContentAndWebhook(t *testing.T) {
	var sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	p := &reportProvider{}
	s := NewWithPolicy(p, "profile summary prompt", server.URL, "token", "Транскрипт:\n\n", false, log.New(io.Discard, "", 0))
	if err := s.Run("Клиент: привет"); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || p.system != "profile summary prompt" || !strings.HasPrefix(p.input, "Транскрипт:\n\nКлиент: привет") || sent != "отчёт" {
		t.Fatalf("report policy ignored: calls=%d system=%q input=%q sent=%q", p.calls, p.system, p.input, sent)
	}
}

func TestEmptyHistorySkipsReport(t *testing.T) {
	p := &reportProvider{}
	s := NewWithPolicy(p, "prompt", "", "", "prefix", false, log.New(io.Discard, "", 0))
	if err := s.Run(""); err != nil || p.calls != 0 {
		t.Fatalf("empty history triggered report: %v calls=%d", err, p.calls)
	}
}
