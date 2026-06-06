package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	GroqAPIKey         string
	ElevenAPIKey       string
	ElevenVoiceID      string
	ElevenModel        string
	OpenAIAPIKey       string
	OpenAIModel        string
	OpenAIVoice        string
	TTSEngine          string
	STTEngine          string
	ClaudeModel        string
	PiperModel         string
	SkillFile          string
	SummarySkillFile   string
	ClaudeBin          string
	HistoryDir         string
	NotifyWebhookURL   string
	NotifyWebhookToken string
	WebhookBaseURL     string
	PolzaAPIKey        string
	PolzaSTTModel      string
	PolzaTTSModel      string
	PolzaTTSVoice      string
	ThemesFile         string

	// LLM provider selection and per-task model settings.
	LLMEngine             string // "polza" (default) or "claude"
	LLMModel              string // dialog model id
	LLMSummaryModel       string // post-call summary model id
	LLMDialogTemperature  string // optional; omit for models that reject it
	LLMDialogReasoning    string // optional reasoning effort (reasoning models only)
	LLMDialogMaxTokens    int
	LLMSummaryTemperature string
	LLMSummaryReasoning   string
	LLMSummaryMaxTokens   int
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open env file %s: %w", path, err)
	}
	defer f.Close()

	cfg := &Config{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "GROQ_API_KEY":
			cfg.GroqAPIKey = val
		case "ELEVENLABS_API_KEY":
			cfg.ElevenAPIKey = val
		case "ELEVENLABS_VOICE_ID":
			cfg.ElevenVoiceID = val
		case "ELEVENLABS_MODEL":
			cfg.ElevenModel = val
		case "OPENAI_TTS_API_KEY":
			cfg.OpenAIAPIKey = val
		case "OPENAI_TTS_MODEL":
			cfg.OpenAIModel = val
		case "OPENAI_TTS_VOICE":
			cfg.OpenAIVoice = val
		case "TTS_ENGINE":
			cfg.TTSEngine = val
		case "CLAUDE_MODEL":
			cfg.ClaudeModel = val
		case "PIPER_MODEL":
			cfg.PiperModel = val
		case "SKILL_FILE":
			cfg.SkillFile = val
		case "SUMMARY_SKILL_FILE":
			cfg.SummarySkillFile = val
		case "CLAUDE_BIN":
			cfg.ClaudeBin = val
		case "HISTORY_DIR":
			cfg.HistoryDir = val
		case "NOTIFY_WEBHOOK_URL":
			cfg.NotifyWebhookURL = val
		case "NOTIFY_WEBHOOK_TOKEN":
			cfg.NotifyWebhookToken = val
		case "WEBHOOK_URL":
			cfg.WebhookBaseURL = val
		case "STT_ENGINE":
			cfg.STTEngine = val
		case "POLZA_API_KEY":
			cfg.PolzaAPIKey = val
		case "POLZA_STT_MODEL":
			cfg.PolzaSTTModel = val
		case "POLZA_TTS_MODEL":
			cfg.PolzaTTSModel = val
		case "POLZA_TTS_VOICE":
			cfg.PolzaTTSVoice = val
		case "THEMES_FILE":
			cfg.ThemesFile = val
		case "LLM_ENGINE":
			cfg.LLMEngine = val
		case "LLM_MODEL":
			cfg.LLMModel = val
		case "LLM_SUMMARY_MODEL":
			cfg.LLMSummaryModel = val
		case "LLM_DIALOG_TEMPERATURE":
			cfg.LLMDialogTemperature = val
		case "LLM_DIALOG_REASONING":
			cfg.LLMDialogReasoning = val
		case "LLM_DIALOG_MAX_TOKENS":
			cfg.LLMDialogMaxTokens, _ = strconv.Atoi(val)
		case "LLM_SUMMARY_TEMPERATURE":
			cfg.LLMSummaryTemperature = val
		case "LLM_SUMMARY_REASONING":
			cfg.LLMSummaryReasoning = val
		case "LLM_SUMMARY_MAX_TOKENS":
			cfg.LLMSummaryMaxTokens, _ = strconv.Atoi(val)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// Defaults so the app runs with a minimal .env.
	if cfg.LLMEngine == "" {
		cfg.LLMEngine = "polza"
	}
	if cfg.LLMModel == "" {
		cfg.LLMModel = "openai/gpt-5.4-mini"
	}
	if cfg.LLMSummaryModel == "" {
		cfg.LLMSummaryModel = "google/gemini-3.5-flash"
	}
	return cfg, nil
}
