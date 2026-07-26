package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	GroqAPIKey          string
	ElevenAPIKey        string
	ElevenVoiceID       string
	ElevenModel         string
	OpenAIAPIKey        string
	OpenAIModel         string
	OpenAIVoice         string
	TTSEngine           string
	STTEngine           string
	ClaudeModel         string
	PiperModel          string
	SkillFile           string
	SummarySkillFile    string
	ClaudeBin           string
	HistoryDir          string
	NotifyWebhookURL    string
	NotifyWebhookToken  string
	WebhookBaseURL      string
	PolzaAPIKey         string
	PolzaSTTModel       string
	PolzaTTSModel       string
	PolzaTTSVoice       string
	OpenRouterAPIKey    string
	OpenRouterSTTModel  string
	OpenRouterTTSModel  string
	OpenRouterTTSVoice  string
	OpenRouterTTSFormat string
	ThemesFile          string

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

	// Claude CLI run settings, passed per invocation via --settings so the AGI
	// runs neither depend on nor disturb the interactive settings of the user
	// the CLI runs as. The output cap matters most: a full post-call analysis
	// is ~25k output tokens and the CLI's own 32k default used to cut the
	// reply in two.
	ClaudeMaxOutputTokens   int    // CLAUDE_MAX_OUTPUT_TOKENS
	ClaudeMaxThinkingTokens int    // CLAUDE_MAX_THINKING_TOKENS
	ClaudeEffort            string // CLAUDE_EFFORT: low|medium|high|xhigh
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
		case "OPENROUTER_API_KEY":
			cfg.OpenRouterAPIKey = val
		case "OPENROUTER_STT_MODEL":
			cfg.OpenRouterSTTModel = val
		case "OPENROUTER_TTS_MODEL":
			cfg.OpenRouterTTSModel = val
		case "OPENROUTER_TTS_VOICE":
			cfg.OpenRouterTTSVoice = val
		case "OPENROUTER_TTS_FORMAT":
			cfg.OpenRouterTTSFormat = val
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
		case "CLAUDE_MAX_OUTPUT_TOKENS":
			cfg.ClaudeMaxOutputTokens, _ = strconv.Atoi(val)
		case "CLAUDE_MAX_THINKING_TOKENS":
			cfg.ClaudeMaxThinkingTokens, _ = strconv.Atoi(val)
		case "CLAUDE_EFFORT":
			cfg.ClaudeEffort = val
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
	// 64k is the model ceiling and twice the CLI default; the analysis plus its
	// thinking must fit in one reply. CLAUDE_EFFORT is left unset (the CLI's own
	// setting applies) — set it to low/medium to spend fewer tokens per report.
	if cfg.ClaudeMaxOutputTokens == 0 {
		cfg.ClaudeMaxOutputTokens = 64000
	}
	if cfg.ClaudeMaxThinkingTokens == 0 {
		cfg.ClaudeMaxThinkingTokens = 8192
	}
	return cfg, nil
}
