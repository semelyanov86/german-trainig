package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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
	TTSStyleTags        string // grok|gemini|elevenlabs|off|auto (see tts.DialectFor)
	ThemesFile          string

	// The "custom" STT engine: any OpenAI-compatible audio/transcriptions
	// endpoint, described entirely by config. In production this is the
	// self-hosted whisper.cpp server, and keeping every detail here is the point
	// — moving to another host or model must not need a rebuild.
	CustomSTTURL      string        // CUSTOM_STT_URL, the full URL
	CustomSTTAPIKey   string        // CUSTOM_STT_API_KEY, empty: no Authorization header
	CustomSTTModel    string        // CUSTOM_STT_MODEL, empty: field not sent
	CustomSTTLanguage string        // CUSTOM_STT_LANGUAGE, "auto": field not sent
	CustomSTTTimeout  time.Duration // CUSTOM_STT_TIMEOUT, whole seconds

	// STT fallback. One host with one GPU is a single point of failure, so a
	// failed transcription can be retried on a hosted engine instead of costing
	// the turn.
	STTFallbackEngine  string        // STT_FALLBACK_ENGINE, empty: no fallback
	STTFallbackTimeout time.Duration // STT_FALLBACK_TIMEOUT, whole seconds

	// LLM provider selection and per-task model settings. LLM_ENGINE sets the
	// baseline; LLM_DIALOG_ENGINE / LLM_SUMMARY_ENGINE override it per task, so
	// the dialog can run on one provider and the post-call report on another.
	LLMEngine             string // "polza" (default), "openrouter" or "claude"
	LLMDialogEngine       string // optional override for the dialog
	LLMSummaryEngine      string // optional override for the summary
	LLMModel              string // dialog model id
	LLMSummaryModel       string // post-call summary model id
	LLMDialogTemperature  string // optional; omit for models that reject it
	LLMDialogReasoning    string // optional reasoning effort (reasoning models only)
	LLMDialogMaxTokens    int
	LLMSummaryTemperature string
	LLMSummaryReasoning   string
	LLMSummaryMaxTokens   int

	// Resilience knobs. A provider outage used to end a turn in silence: on
	// 2026-08-19 OpenRouter's shared pool for the dialog model answered 429 for
	// several minutes and the call degraded into a loop of empty turns. The
	// fallback lists let OpenRouter answer from another model inside the same
	// request; the retry counts cover a short-lived blip.
	LLMDialogFallbackModels  []string // LLM_DIALOG_FALLBACK_MODELS (comma-separated)
	LLMSummaryFallbackModels []string // LLM_SUMMARY_FALLBACK_MODELS (comma-separated)
	LLMDialogRetries         int      // LLM_DIALOG_RETRIES, attempts per turn
	LLMSummaryRetries        int      // LLM_SUMMARY_RETRIES, attempts per report

	// Claude CLI run settings, passed per invocation via --settings so the AGI
	// runs neither depend on nor disturb the interactive settings of the user
	// the CLI runs as. The output cap matters most: a full post-call analysis
	// is ~25k output tokens and the CLI's own 32k default used to cut the
	// reply in two.
	ClaudeMaxOutputTokens   int    // CLAUDE_MAX_OUTPUT_TOKENS
	ClaudeMaxThinkingTokens int    // CLAUDE_MAX_THINKING_TOKENS
	ClaudeEffort            string // CLAUDE_EFFORT: low|medium|high|xhigh
}

// Built-in model for the openrouter backend. Pinned to an exact version on
// purpose: OpenRouter's floating "~vendor/model-latest" aliases silently swap in
// whatever is newest, and latency is part of the requirement here — a call turn
// has to come back in about a second. Measured against SKILL.md, this model
// answers in ~0.7s and keeps to the persona's format rules.
const defaultOpenRouterModel = "mistralai/mistral-medium-3-5"

// Built-in fallback chain for the openrouter backend, in order. The entries must
// sit behind a *different* upstream vendor than the default model (Mistral): a
// shared-pool rate limit hits one vendor at a time, and that is exactly what
// took the dialog down on 2026-08-19.
//
// Measured on a tutor-sized German turn against ~0.6s for the primary:
// google/gemini-3.5-flash-lite 0.6-1.1s at $0.00017 (Google, half the primary's
// cost per turn), openai/gpt-5.4-mini 0.9-1.7s, anthropic/claude-haiku-4.5
// 1.7-2.5s. Latency is the requirement here, hence the order. Also verified:
// flash-lite reports reasoning_tokens=0, i.e. it does not silently think — a
// real hazard on gemini-3.x, where thinking costs seconds per turn.
var defaultOpenRouterFallbacks = []string{
	"google/gemini-3.5-flash-lite",
}

// defaultModel picks the built-in model id for an engine. The claude backend
// ignores it (it runs CLAUDE_MODEL), so only the HTTP backends need a value.
func defaultModel(engine, polzaModel string) string {
	if engine == "openrouter" {
		return defaultOpenRouterModel
	}
	return polzaModel
}

// splitModels parses a comma-separated model list, dropping blanks.
func splitModels(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if m := strings.TrimSpace(part); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// resolveFallbacks settles the fallback chain for one task. Only the openrouter
// engine can use one (it is sent as that API's "models" array), and the primary
// model is filtered out: it is always tried first anyway, and OpenRouter
// validates every id in the list, so a duplicate is pointless noise.
func resolveFallbacks(engine string, configured []string, primary string) []string {
	if engine != "openrouter" {
		return nil
	}
	if configured == nil {
		configured = defaultOpenRouterFallbacks
	}
	out := make([]string, 0, len(configured))
	for _, m := range configured {
		if m != primary {
			out = append(out, m)
		}
	}
	return out
}

// seconds parses a duration written as whole seconds, the unit every timeout key
// in the .env uses. A missing or malformed value yields 0, meaning "use the
// built-in default".
func seconds(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
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
		case "TTS_STYLE_TAGS":
			cfg.TTSStyleTags = val
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
		case "CUSTOM_STT_URL":
			cfg.CustomSTTURL = val
		case "CUSTOM_STT_API_KEY":
			cfg.CustomSTTAPIKey = val
		case "CUSTOM_STT_MODEL":
			cfg.CustomSTTModel = val
		case "CUSTOM_STT_LANGUAGE":
			cfg.CustomSTTLanguage = val
		case "CUSTOM_STT_TIMEOUT":
			cfg.CustomSTTTimeout = seconds(val)
		case "STT_FALLBACK_ENGINE":
			cfg.STTFallbackEngine = val
		case "STT_FALLBACK_TIMEOUT":
			cfg.STTFallbackTimeout = seconds(val)
		case "THEMES_FILE":
			cfg.ThemesFile = val
		case "LLM_ENGINE":
			cfg.LLMEngine = val
		case "LLM_DIALOG_ENGINE":
			cfg.LLMDialogEngine = val
		case "LLM_SUMMARY_ENGINE":
			cfg.LLMSummaryEngine = val
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
		case "LLM_DIALOG_FALLBACK_MODELS":
			// A present-but-empty key means "no fallback", which is why this is
			// a non-nil empty slice: nil selects the built-in chain below.
			cfg.LLMDialogFallbackModels = append([]string{}, splitModels(val)...)
		case "LLM_SUMMARY_FALLBACK_MODELS":
			cfg.LLMSummaryFallbackModels = append([]string{}, splitModels(val)...)
		case "LLM_DIALOG_RETRIES":
			cfg.LLMDialogRetries, _ = strconv.Atoi(val)
		case "LLM_SUMMARY_RETRIES":
			cfg.LLMSummaryRetries, _ = strconv.Atoi(val)
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
	// An unset per-task engine inherits the shared one.
	if cfg.LLMDialogEngine == "" {
		cfg.LLMDialogEngine = cfg.LLMEngine
	}
	if cfg.LLMSummaryEngine == "" {
		cfg.LLMSummaryEngine = cfg.LLMEngine
	}
	if cfg.LLMModel == "" {
		cfg.LLMModel = defaultModel(cfg.LLMDialogEngine, "openai/gpt-5.4-mini")
	}
	if cfg.LLMSummaryModel == "" {
		cfg.LLMSummaryModel = defaultModel(cfg.LLMSummaryEngine, "google/gemini-3.5-flash")
	}
	// A dialog turn is retried while the caller waits on hold music, so it gets
	// fewer attempts than the post-call report, which nobody is listening to.
	if cfg.LLMDialogRetries == 0 {
		cfg.LLMDialogRetries = 2
	}
	if cfg.LLMSummaryRetries == 0 {
		cfg.LLMSummaryRetries = 3
	}
	// Fallback chains depend on the model ids resolved just above.
	cfg.LLMDialogFallbackModels = resolveFallbacks(cfg.LLMDialogEngine, cfg.LLMDialogFallbackModels, cfg.LLMModel)
	cfg.LLMSummaryFallbackModels = resolveFallbacks(cfg.LLMSummaryEngine, cfg.LLMSummaryFallbackModels, cfg.LLMSummaryModel)

	// STT defaults. German is spelled out rather than left to the endpoint so a
	// different server gets told explicitly; CUSTOM_STT_LANGUAGE=auto omits the
	// field and lets the endpoint decide.
	if cfg.CustomSTTLanguage == "" {
		cfg.CustomSTTLanguage = "de"
	}
	// Longer than the self-hosted server's own 25s cut-off, so its error reaches
	// the log instead of our own anonymous timeout.
	if cfg.CustomSTTTimeout == 0 {
		cfg.CustomSTTTimeout = 30 * time.Second
	}
	// The fallback engine runs after the primary already spent its budget, so it
	// gets a tighter cap than its own default (60s on openrouter/polza): measured
	// p99 there is 4.9s, and a caller who has listened to 50s of hold music has
	// lost the turn either way.
	if cfg.STTFallbackTimeout == 0 {
		cfg.STTFallbackTimeout = 20 * time.Second
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
