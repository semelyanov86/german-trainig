package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexTaskConfiguration(t *testing.T) {
	for _, tc := range []struct{ env, dialog, summary, dialogModel, summaryModel, bin string }{
		{"LLM_ENGINE=codex\n", "codex", "codex", "gpt-6-luna", "gpt-6-luna", "/usr/local/bin/codex"},
		{"LLM_ENGINE=openrouter\nLLM_SUMMARY_ENGINE=codex\nCODEX_BIN=/custom/codex\n", "openrouter", "codex", defaultOpenRouterModel, "gpt-6-luna", "/custom/codex"},
		{"LLM_ENGINE=codex\nLLM_DIALOG_ENGINE=claude\nLLM_SUMMARY_MODEL=custom-model\n", "claude", "codex", "openai/gpt-5.4-mini", "custom-model", "/usr/local/bin/codex"},
	} {
		path := filepath.Join(t.TempDir(), ".env")
		write(t, path, tc.env)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLMDialogEngine != tc.dialog || cfg.LLMSummaryEngine != tc.summary || cfg.LLMModel != tc.dialogModel || cfg.LLMSummaryModel != tc.summaryModel || cfg.CodexBin != tc.bin {
			t.Fatalf("unexpected engines/models for %q: %+v", tc.env, cfg)
		}
		if tc.dialog == "codex" && len(cfg.LLMDialogFallbackModels) != 0 || tc.summary == "codex" && len(cfg.LLMSummaryFallbackModels) != 0 {
			t.Fatal("OpenRouter fallback models leaked into Codex")
		}
	}
}

func TestLegacyCallAndNamedProfile(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".env")
	write(t, base, "PROFILE_IDS=support\nSKILL_FILE=/legacy/skill.md\nSUMMARY_SKILL_FILE=/legacy/summary.md\nTHEMES_FILE=/legacy/themes.txt\nCUSTOM_STT_LANGUAGE=auto\nLLM_MODEL=legacy-model\nOPENROUTER_TTS_VOICE=legacy-voice\nNOTIFY_WEBHOOK_URL=https://legacy.invalid/hook\n")
	legacy, err := LoadProfile(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if explicit, err := LoadProfile(base, "german"); err != nil || explicit.SkillFile != legacy.SkillFile || explicit.SummaryEnabled != legacy.SummaryEnabled {
		t.Fatalf("explicit German differs from no-argument call: %+v %v", explicit, err)
	}
	if legacy.ProfileID != "german" || legacy.SkillFile != "/legacy/skill.md" || legacy.ThemesFile != "/legacy/themes.txt" || legacy.CustomSTTLanguage != "auto" || legacy.STTLanguage != "de" || legacy.LLMModel != "legacy-model" || legacy.OpenRouterTTSVoice != "legacy-voice" || !legacy.SummaryEnabled || !legacy.LogUtterances {
		t.Fatalf("legacy settings changed: %+v", legacy)
	}
	if legacy.GreetingPrompt != "Starte ein neues Gespräch. Begrüße den Anrufer und schlage ein Thema vor." || legacy.SilenceLine != "Ich höre nichts. Sag bitte etwas!" || legacy.FarewellLine != "Tschüss! Bis zum nächsten Mal!" {
		t.Fatalf("legacy German lines changed: %+v", legacy)
	}
	if _, err := LoadProfile(base, "../../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := LoadProfile(base, "unknown"); err == nil {
		t.Fatal("unlisted profile accepted")
	}
	if _, err := LoadProfile(base, "support"); err == nil {
		t.Fatal("listed profile without a file accepted")
	}

	profile := filepath.Join(dir, "profiles", "support.env")
	write(t, profile, "LANGUAGE=ru\nSKILL_FILE=/private/support.md\nGREETING_PROMPT=Начни разговор.\nSILENCE_PROMPT=Попроси собеседника говорить.\nSILENCE_LINE=Я вас не слышу.\nFAREWELL_LINE=До свидания.\nGLITCH_LINE=Повторите, пожалуйста.\nOUTAGE_LINE=Позвоните позже.\nFAREWELL_PHRASES=пока,до свидания\nHISTORY_ROLE=Собеседник\nLLM_MODEL=private-model\nOPENROUTER_TTS_VOICE=private-voice\n")
	private, err := LoadProfile(base, "support")
	if err != nil {
		t.Fatal(err)
	}
	if private.Language != "ru" || private.STTLanguage != "ru" || private.CustomSTTLanguage != "ru" || private.LLMModel != "private-model" || private.OpenRouterTTSVoice != "private-voice" {
		t.Fatalf("profile overrides ignored: %+v", private)
	}
	if private.SkillFile != "/private/support.md" || private.ThemesFile != "" || private.SummarySkillFile != "" || private.NotifyWebhookURL != "" || private.SummaryEnabled || private.LogUtterances {
		t.Fatalf("private profile inherited German scenario or report: %+v", private)
	}
	write(t, profile, "LANGUAGE=ru\nSKILL_FILE=/private/support.md\nSUMMARY_ENABLED=true\n")
	if _, err := LoadProfile(base, "support"); err == nil || !strings.Contains(err.Error(), "required scenario") {
		t.Fatalf("incomplete profile accepted: %v", err)
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPsychologistConfigurationIsIndependent(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".env")
	write(t, base, "PROFILE_IDS=psychologist\nLLM_ENGINE=codex\nLLM_MODEL=trainer-model\nLLM_SUMMARY_MODEL=trainer-summary\nMAX_RECORD_SECONDS=120\n")
	example, err := os.ReadFile("../../profiles/psychologist.env.example")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "profiles", "psychologist.env"), string(example)+"\nLLM_MODEL=psychologist-model\nLLM_SUMMARY_MODEL=psychologist-summary\n")
	psychologist, err := LoadProfile(base, "psychologist")
	if err != nil {
		t.Fatal(err)
	}
	if psychologist.MaxRecordSeconds != 420 || psychologist.SummaryMode != "transcript_advice" || psychologist.FarewellMode != "utterance" || psychologist.Language != "ru" || psychologist.STTLanguage != "ru" || psychologist.CustomSTTLanguage != "ru" {
		t.Fatalf("incorrect psychologist behavior: %+v", psychologist.Scenario)
	}
	if psychologist.LogUtterances || !psychologist.SummaryEnabled || psychologist.GreetingNotice == "" || psychologist.TTSStyleTags != "auto" || psychologist.LLMModel != "psychologist-model" || psychologist.LLMSummaryModel != "psychologist-summary" {
		t.Fatal("independent model, notice, style or reporting policy ignored")
	}
	trainer, err := Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if trainer.LLMModel != "trainer-model" || trainer.LLMSummaryModel != "trainer-summary" || trainer.MaxRecordSeconds != 120 || trainer.SummaryMode != "analysis" || trainer.FarewellMode != "phrase" {
		t.Fatal("psychologist configuration affected the German trainer")
	}
	write(t, base, "")
	legacy, err := Load(base)
	if err != nil || legacy.MaxRecordSeconds != 150 {
		t.Fatalf("legacy recording limit changed: %v", err)
	}
}

func TestRejectInvalidConversationPolicy(t *testing.T) {
	for _, env := range []string{"MAX_RECORD_SECONDS=0", "MAX_RECORD_SECONDS=-1", "MAX_RECORD_SECONDS=901", "MAX_RECORD_SECONDS=9999999999999999999999999", "MAX_RECORD_SECONDS=seven", "SUMMARY_MODE=unknown", "FAREWELL_MODE=unknown"} {
		path := filepath.Join(t.TempDir(), ".env")
		write(t, path, env)
		if _, err := Load(path); err == nil {
			t.Errorf("accepted invalid configuration: %s", env)
		}
	}
}
