package main

import (
	"io"
	"log"
	"testing"

	"german-trainer/internal/config"
)

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
		PolzaAPIKey: "polza-key", OpenRouterAPIKey: "router-key", ClaudeBin: "/usr/bin/claude",
		HistoryDir: "/tmp/history", ClaudeMaxOutputTokens: 64000, ClaudeMaxThinkingTokens: 8192, ClaudeEffort: "medium",
	}
	dialog, report := dialogSpec(cfg), summarySpec(cfg)
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
