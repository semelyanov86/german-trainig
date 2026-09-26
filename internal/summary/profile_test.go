package summary

import (
	"bytes"
	"errors"
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
	reply         string
	err           error
}

func (p *reportProvider) Complete(system string, messages []llm.Message) (string, error) {
	p.calls++
	p.system, p.input = system, messages[0].Content
	if p.err != nil {
		return "", p.err
	}
	if p.reply != "" {
		return p.reply, nil
	}
	return "отчёт", nil
}

func TestTranscriptAdviceDeliveryPreservesHistory(t *testing.T) {
	history := "Психолог: Что сейчас самое тяжёлое?\nUser: Я потерял работу. Мои слова: *буквально* | без пересказа.\n"
	for _, fail := range []bool{false, true} {
		var delivered string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("Content-Type") != "text/plain" {
				t.Error("webhook contract changed")
			}
			body, _ := io.ReadAll(r.Body)
			delivered = string(body)
			w.WriteHeader(http.StatusNoContent)
		}))
		p := &reportProvider{reply: "1. Запишите посильный шаг, который вы сами выбрали."}
		if fail {
			p.err = errors.New("provider unavailable")
		}
		var out bytes.Buffer
		s := NewWithReportPolicy(p, "only advice", server.URL, "token", Policy{Mode: "transcript_advice", Prefix: "Транскрипт:\n", LogContent: false}, log.New(&out, "", 0))
		if err := s.Run(history); err != nil {
			t.Fatal(err)
		}
		server.Close()
		if !strings.Contains(delivered, history) || !strings.Contains(delivered, "Что можно сделать") || p.input != "Транскрипт:\n"+history {
			t.Fatalf("transcript altered or advice context lost: %q", delivered)
		}
		if fail && !strings.Contains(delivered, "Советы не сформированы из-за технической ошибки.") {
			t.Fatal("model failure discarded the transcript")
		}
		if strings.Contains(out.String(), "truncated") || strings.Contains(out.String(), "Я потерял работу") {
			t.Fatal("short report mislabeled or private history logged")
		}
	}
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
