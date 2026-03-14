package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	GroqAPIKey    string
	ElevenAPIKey  string
	ElevenVoiceID string
	ElevenModel   string
	TTSEngine     string
	ClaudeModel   string
	PiperModel    string
	SkillFile     string
	ClaudeBin     string
	HistoryDir    string
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
		case "TTS_ENGINE":
			cfg.TTSEngine = val
		case "CLAUDE_MODEL":
			cfg.ClaudeModel = val
		case "PIPER_MODEL":
			cfg.PiperModel = val
		case "SKILL_FILE":
			cfg.SkillFile = val
		case "CLAUDE_BIN":
			cfg.ClaudeBin = val
		case "HISTORY_DIR":
			cfg.HistoryDir = val
		}
	}
	return cfg, sc.Err()
}
