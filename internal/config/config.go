package config

import (
	"bufio"
	"fmt"
	"os"
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
	SkillFile          string
	SummarySkillFile   string
	ClaudeBin          string
	HistoryDir          string
	NotifyWebhookURL    string
	NotifyWebhookToken  string
	PolzaAPIKey         string
	PolzaSTTModel       string
	PolzaTTSModel       string
	PolzaTTSVoice       string
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
		}
	}
	return cfg, sc.Err()
}
