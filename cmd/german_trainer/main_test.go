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
	"time"

	"german-trainer/internal/agi"
	"german-trainer/internal/config"
	"german-trainer/internal/llm"
	"german-trainer/internal/session"
	"german-trainer/internal/tts"
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

func (s playbackSynth) Speak(string) (tts.Audio, error) {
	return &pieces{paths: []string{"/tmp/test-playback.wav"}}, s.err
}

// pieces is a reply handed out as several files, optionally breaking off
// after the last one.
type pieces struct {
	paths   []string
	err     error
	stopped bool
}

func (p *pieces) Next() (string, error) {
	if p.stopped || len(p.paths) == 0 {
		return "", p.err
	}
	path := p.paths[0]
	p.paths = p.paths[1:]
	return path, nil
}

func (p *pieces) Files() []string { return nil }
func (p *pieces) Stop()           { p.stopped = true }

// brokenOnce is a streaming voice whose first reply breaks off mid-playback.
type brokenOnce struct{ calls int }

func (b *brokenOnce) Speak(string) (tts.Audio, error) {
	b.calls++
	if b.calls == 1 {
		return &pieces{paths: []string{"/tmp/a_00.sln", "/tmp/a_01.sln"}, err: errors.New("reply incomplete")}, nil
	}
	return &pieces{paths: []string{"/tmp/b_00.sln", "/tmp/b_01.sln", "/tmp/b_02.sln"}}, nil
}

func TestStreamedReplyPlaysEveryPieceAndRepeatsABrokenOne(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	sess := session.New(t.TempDir(), logger)
	sess.SpokenRole = "Tutor"
	var sent bytes.Buffer
	ch := agi.NewChannel(strings.NewReader(strings.Repeat("200 result=0 endpos=8000\n", 10)), &sent, logger)
	voice := &brokenOnce{}
	if !playTTS(ch, sess, voice, "<|emotion:amusement|>Ach komm.", logger) {
		t.Fatal("channel reported dead")
	}
	want := []string{
		"EXEC StopMusicOnHold", "STREAM FILE /tmp/a_00 \"\"", "STREAM FILE /tmp/a_01 \"\"",
		"EXEC StartMusicOnHold " + mohClass,
		"EXEC StopMusicOnHold", "STREAM FILE /tmp/b_00 \"\"", "STREAM FILE /tmp/b_01 \"\"", "STREAM FILE /tmp/b_02 \"\"",
	}
	if got := strings.Split(strings.TrimSpace(sent.String()), "\n"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("AGI commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if voice.calls != 2 {
		t.Fatalf("reply requested %d times, want 2", voice.calls)
	}
	if got := sess.ReadHistory(); got != "Tutor: Ach komm.\n" {
		t.Fatalf("spoken transcript: %q", got)
	}
}

func TestHangupStopsAStreamedReply(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	sess := session.New(t.TempDir(), logger)
	sess.SpokenRole = "Tutor"
	var sent bytes.Buffer
	ch := agi.NewChannel(strings.NewReader("HANGUP\n200 result=-1 endpos=100\n"), &sent, logger)
	audio := &pieces{paths: []string{"/tmp/c_00.sln", "/tmp/c_01.sln", "/tmp/c_02.sln"}}
	if heard, err := playAudio(ch, audio); heard || err != nil {
		t.Fatalf("heard=%v err=%v after hangup", heard, err)
	}
	if !audio.stopped || strings.Count(sent.String(), "STREAM FILE") != 1 {
		t.Fatalf("stream not stopped at hangup: %q", sent.String())
	}
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

	cfg.LLMDialogEngine, cfg.LLMSummaryEngine = llm.EngineCodex, llm.EngineCodex
	dialog, report = dialogSpec(cfg), summarySpec(cfg)
	if dialog.Retry.Timeout != 20*time.Second || report.Retry.Timeout != 10*time.Minute {
		t.Fatalf("Codex task deadlines: dialog=%v report=%v", dialog.Retry.Timeout, report.Retry.Timeout)
	}
}
