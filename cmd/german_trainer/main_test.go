package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"german-trainer/internal/agi"
	"german-trainer/internal/config"
	"german-trainer/internal/session"
)

type finalTranscriber struct {
	text  string
	calls int
}

func (s *finalTranscriber) Transcribe(string) (string, error) {
	s.calls++
	return s.text, nil
}

func TestFinalHangupRecordingIsIncludedWithSpeechGuards(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	cfg := &config.Config{Scenario: config.Scenario{Language: "ru", SummaryMode: "transcript_advice"}}
	for _, tc := range []struct {
		samples     int
		text, reply string
		want        bool
	}{
		{16000, "Я потерял работу и хочу об этом поговорить.", "", true},
		{16000, "Мне тяжело.", "200 result=-1 endpos=16000", true},
		{8000, "выдуманные слова", "", false},
		{16000, "", "", false},
		{16000, "다섯", "", false},
	} {
		var wav bytes.Buffer
		wav.WriteString("RIFF")
		binary.Write(&wav, binary.LittleEndian, uint32(12+tc.samples*2))
		wav.WriteString("WAVEdata")
		binary.Write(&wav, binary.LittleEndian, uint32(tc.samples*2))
		wav.Write(make([]byte, tc.samples*2))
		path := filepath.Join(t.TempDir(), "last.wav")
		os.WriteFile(path, wav.Bytes(), 0600)
		sess := session.New(t.TempDir(), logger)
		tr := &finalTranscriber{text: tc.text}
		captureFinalUtterance(path, tc.reply, cfg, tr, sess, logger)
		if got := strings.Contains(sess.ReadHistory(), "User: "); got != tc.want {
			t.Errorf("final utterance captured=%v, want %v for %+v", got, tc.want, tc)
		}
		if tc.samples < minSpeechSamples && tr.calls != 0 {
			t.Fatal("short noise reached STT")
		}
	}
}

type playbackSynth struct{ err error }

func (s playbackSynth) Synthesize(string) (string, []string, error) {
	return "/tmp/test-playback.wav", nil, s.err
}

func TestSpokenTranscriptIncludesOnlyPlayedReplies(t *testing.T) {
	for _, fail := range []bool{false, true} {
		logger := log.New(io.Discard, "", 0)
		sess := session.New(t.TempDir(), logger)
		sess.SpokenRole = "Психолог"
		var sent bytes.Buffer
		ch := agi.NewChannel(strings.NewReader("200 result=0\n200 result=0\n"), &sent, logger)
		synth := playbackSynth{}
		if fail {
			synth.err = errors.New("unavailable")
		}
		playTTS(ch, sess, synth, "<soft>Повторите, пожалуйста.</soft>", logger)
		if got := sess.ReadHistory(); (!fail && got != "Психолог: Повторите, пожалуйста.\n") || (fail && got != "") {
			t.Errorf("incorrect spoken transcript: %q, synth failed=%v", got, fail)
		}
	}
}

func TestProfileSelectionAndSpeechGuard(t *testing.T) {
	if id, err := selectedProfile(nil); err != nil || id != "" {
		t.Fatalf("legacy AGI: %q %v", id, err)
	}
	if id, err := selectedProfile([]string{"support"}); err != nil || id != "support" {
		t.Fatalf("named AGI: %q %v", id, err)
	}
	if _, err := selectedProfile([]string{"a", "b"}); err == nil {
		t.Fatal("multiple arguments accepted")
	}
	for _, tc := range []struct {
		text, lang string
		want       bool
	}{
		{"Guten Tag!", "de", true},
		{"Привет, как дела?", "de", false},
		{"Привет, как дела?", "ru", true},
		{"OpenAI", "ru", false},
		{"Привет, OpenAI", "ru", true},
		{"다섯", "de", false},
		{"धन्यवाद", "ru", false},
		{"... 123 #", "ru", false},
	} {
		if got := hasSpeechScript(tc.text, tc.lang); got != tc.want {
			t.Errorf("hasSpeechScript(%q,%q)=%v, want %v", tc.text, tc.lang, got, tc.want)
		}
	}
}

func TestPsychologistFarewellDoesNotInterruptStories(t *testing.T) {
	cfg := &config.Config{Scenario: config.Scenario{Language: "ru", FarewellMode: "utterance", FarewellPhrases: []string{"до свидания", "спасибо до свидания"}}}
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"До свидания!", true},
		{"Спасибо, до свидания.", true},
		{"Начальник сказал до свидания, и я потерял работу.", false},
		{"Я не хочу говорить до свидания.", false},
		{"«До свидания»", false},
		{"Сейчас причиню себе вред. До свидания.", false},
		{"Мне пока плохо, не знаю, что делать.", false},
	} {
		if got := isFarewell(tc.text, cfg); got != tc.want {
			t.Errorf("isFarewell(%q)=%v, want %v", tc.text, got, tc.want)
		}
	}
	legacy := &config.Config{Scenario: config.Scenario{Language: "de", FarewellPhrases: []string{"tschüss"}}}
	if !isFarewell("Okay, tschüss!", legacy) {
		t.Fatal("German farewell behavior changed")
	}
}

func TestDisabledReportBuildsNoSummarizer(t *testing.T) {
	cfg := &config.Config{Scenario: config.Scenario{SummaryEnabled: false}}
	if got := newSummarizer(cfg, "unused", log.New(io.Discard, "", 0)); got != nil {
		t.Fatal("disabled profile constructed report provider")
	}
}

func TestLLMSpecsKeepSharedSettingsAndTaskOverrides(t *testing.T) {
	cfg := &config.Config{
		LLMDialogEngine: "openrouter", LLMSummaryEngine: "polza",
		LLMModel: "dialog-model", LLMSummaryModel: "summary-model", ClaudeModel: "cli-model",
		LLMDialogTemperature: "0.2", LLMSummaryTemperature: "0.8",
		LLMDialogReasoning: "low", LLMSummaryReasoning: "high",
		LLMDialogMaxTokens: 300, LLMSummaryMaxTokens: 8000,
		LLMDialogFallbackModels: []string{"dialog-backup"}, LLMSummaryFallbackModels: []string{"report-backup"},
		LLMDialogRetries: 2, LLMSummaryRetries: 3,
		PolzaAPIKey: "polza-key", OpenRouterAPIKey: "router-key", ClaudeBin: "/usr/bin/claude", CodexBin: "/usr/bin/codex", CodexRunner: "/test/codex-runner",
		HistoryDir: "/tmp/history", ClaudeMaxOutputTokens: 64000, ClaudeMaxThinkingTokens: 8192, ClaudeEffort: "medium",
	}
	dialog, report := dialogSpec(cfg), summarySpec(cfg)
	if dialog.CodexBin != "/usr/bin/codex" || report.CodexBin != dialog.CodexBin || dialog.CodexRunner != "/test/codex-runner" || report.CodexRunner != dialog.CodexRunner {
		t.Fatalf("Codex entrypoint lost between task specs: dialog=%q report=%q", dialog.CodexBin, report.CodexBin)
	}
	if dialog.Engine != "openrouter" || dialog.Model != "dialog-model" || dialog.Temperature != "0.2" || dialog.Reasoning != "low" || dialog.MaxTokens != 300 || len(dialog.FallbackModels) != 1 || dialog.FallbackModels[0] != "dialog-backup" || dialog.Retry.Attempts != 2 || dialog.Retry.Timeout != dialogRetry.Timeout {
		t.Fatalf("dialog settings changed: %+v", dialog)
	}
	if report.Engine != "polza" || report.Model != "summary-model" || report.Temperature != "0.8" || report.Reasoning != "high" || report.MaxTokens != 8000 || len(report.FallbackModels) != 1 || report.FallbackModels[0] != "report-backup" || report.Retry.Attempts != 3 || report.Retry.Timeout != summaryRetry.Timeout {
		t.Fatalf("summary settings changed: %+v", report)
	}
	if dialog.ClaudeModel != "cli-model" || dialog.PolzaAPIKey != "polza-key" || dialog.OpenRouterAPIKey != "router-key" || dialog.ClaudeBin != "/usr/bin/claude" || dialog.WorkDir != "/tmp/history" || dialog.ClaudeMaxOutputTokens != 64000 || dialog.ClaudeMaxThinkingTokens != 8192 || dialog.ClaudeEffort != "medium" {
		t.Fatalf("shared provider settings changed: %+v", dialog)
	}
	if report.ClaudeModel != dialog.ClaudeModel || report.PolzaAPIKey != dialog.PolzaAPIKey || report.OpenRouterAPIKey != dialog.OpenRouterAPIKey || report.ClaudeBin != dialog.ClaudeBin || report.WorkDir != dialog.WorkDir || report.ClaudeMaxOutputTokens != dialog.ClaudeMaxOutputTokens || report.ClaudeMaxThinkingTokens != dialog.ClaudeMaxThinkingTokens || report.ClaudeEffort != dialog.ClaudeEffort {
		t.Fatalf("task specs diverged on shared settings: dialog=%+v report=%+v", dialog, report)
	}
}
