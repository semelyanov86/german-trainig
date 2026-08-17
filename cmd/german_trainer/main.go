package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"german-trainer/internal/agi"
	"german-trainer/internal/config"
	"german-trainer/internal/farewell"
	"german-trainer/internal/llm"
	"german-trainer/internal/session"
	"german-trainer/internal/skill"
	"german-trainer/internal/stt"
	"german-trainer/internal/summary"
	"german-trainer/internal/theme"
	"german-trainer/internal/tts"
)

const (
	maxTurns    = 25
	maxRecordMs = 150000
	silenceSec  = 5
	logFile     = "/tmp/german_trainer.log"
	envFile     = "/etc/german-trainer/.env"
	// Dedicated Asterisk MOH class for the "thinking" pause. It holds a pool of
	// AI-generated calm instrumental tracks with sort=random, so each hold plays
	// different music. Scoped to this app — the global "default" class is untouched.
	mohClass = "german-thinking"
)

func main() {
	cfg, err := config.Load(envFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open log: %v\n", err)
		os.Exit(1)
	}
	defer lf.Close()
	logger := log.New(lf, "", log.LstdFlags)

	ch := agi.NewChannel(os.Stdin, os.Stdout, logger)
	ch.ReadVars()
	logger.Printf("AGI started, channel=%s callerid=%s tts=%s stt=%s",
		ch.Vars["agi_channel"], ch.Vars["agi_callerid"], cfg.TTSEngine, cfg.STTEngine)

	sess := session.New(cfg.HistoryDir, logger)
	logger.Printf("Session %s, history: %s", sess.ID, sess.HistoryFile)
	// Log the engine and model per task, and resolve the model the way the
	// backend will: on the claude engine the per-task ids are ignored in favour
	// of CLAUDE_MODEL, and printing them anyway used to suggest otherwise.
	logger.Printf("LLM dialog: engine=%s model=%s | summary: engine=%s model=%s",
		cfg.LLMDialogEngine, effectiveModel(cfg.LLMDialogEngine, cfg.LLMModel, cfg.ClaudeModel),
		cfg.LLMSummaryEngine, effectiveModel(cfg.LLMSummaryEngine, cfg.LLMSummaryModel, cfg.ClaudeModel))

	// System prompts are loaded from files shipped with the app (not from
	// server-side Claude skills), with YAML frontmatter stripped.
	tutorPrompt := loadPrompt(cfg.SkillFile, logger)
	summaryPrompt := loadPrompt(cfg.SummarySkillFile, logger)

	summaryProvider := llm.New(llm.Spec{
		Engine:                  cfg.LLMSummaryEngine,
		Model:                   cfg.LLMSummaryModel,
		ClaudeModel:             cfg.ClaudeModel,
		Temperature:             cfg.LLMSummaryTemperature,
		Reasoning:               cfg.LLMSummaryReasoning,
		MaxTokens:               cfg.LLMSummaryMaxTokens,
		PolzaAPIKey:             cfg.PolzaAPIKey,
		OpenRouterAPIKey:        cfg.OpenRouterAPIKey,
		ClaudeBin:               cfg.ClaudeBin,
		WorkDir:                 cfg.HistoryDir,
		ClaudeMaxOutputTokens:   cfg.ClaudeMaxOutputTokens,
		ClaudeMaxThinkingTokens: cfg.ClaudeMaxThinkingTokens,
		ClaudeEffort:            cfg.ClaudeEffort,
	}, logger)
	summarizer := summary.New(
		summaryProvider, summaryPrompt,
		cfg.NotifyWebhookURL, cfg.NotifyWebhookToken,
		logger,
	)
	defer func() {
		if err := summarizer.Run(sess.ReadHistory()); err != nil {
			logger.Printf("ERROR generating summary: %v", err)
		}
		sess.Cleanup()
	}()

	transcriber := stt.New(cfg.STTEngine, stt.Config{
		GroqAPIKey:         cfg.GroqAPIKey,
		PolzaAPIKey:        cfg.PolzaAPIKey,
		PolzaSTTModel:      cfg.PolzaSTTModel,
		OpenRouterAPIKey:   cfg.OpenRouterAPIKey,
		OpenRouterSTTModel: cfg.OpenRouterSTTModel,
	}, logger)
	synthesizer := tts.New(cfg.TTSEngine, tts.Config{
		SessionID:           sess.ID,
		ElevenAPIKey:        cfg.ElevenAPIKey,
		ElevenVoiceID:       cfg.ElevenVoiceID,
		ElevenModel:         cfg.ElevenModel,
		OpenAIAPIKey:        cfg.OpenAIAPIKey,
		OpenAIModel:         cfg.OpenAIModel,
		OpenAIVoice:         cfg.OpenAIVoice,
		PiperModel:          cfg.PiperModel,
		PolzaAPIKey:         cfg.PolzaAPIKey,
		PolzaTTSModel:       cfg.PolzaTTSModel,
		PolzaTTSVoice:       cfg.PolzaTTSVoice,
		OpenRouterAPIKey:    cfg.OpenRouterAPIKey,
		OpenRouterTTSModel:  cfg.OpenRouterTTSModel,
		OpenRouterTTSVoice:  cfg.OpenRouterTTSVoice,
		OpenRouterTTSFormat: cfg.OpenRouterTTSFormat,
	}, logger)
	dialogProvider := llm.New(llm.Spec{
		Engine:                  cfg.LLMDialogEngine,
		Model:                   cfg.LLMModel,
		ClaudeModel:             cfg.ClaudeModel,
		Temperature:             cfg.LLMDialogTemperature,
		Reasoning:               cfg.LLMDialogReasoning,
		MaxTokens:               cfg.LLMDialogMaxTokens,
		PolzaAPIKey:             cfg.PolzaAPIKey,
		OpenRouterAPIKey:        cfg.OpenRouterAPIKey,
		ClaudeBin:               cfg.ClaudeBin,
		WorkDir:                 cfg.HistoryDir,
		ClaudeMaxOutputTokens:   cfg.ClaudeMaxOutputTokens,
		ClaudeMaxThinkingTokens: cfg.ClaudeMaxThinkingTokens,
		ClaudeEffort:            cfg.ClaudeEffort,
	}, logger)
	dialog := llm.NewConversation(dialogProvider, tutorPrompt)

	ch.Cmd("ANSWER")
	if !ch.IsAlive() {
		return
	}

	// Play music while generating greeting
	ch.Cmd("EXEC StartMusicOnHold " + mohClass)
	if !ch.IsAlive() {
		return
	}

	themePrompt := "Starte ein neues Gespräch. Begrüße den Anrufer und schlage ein Thema vor."
	if cfg.ThemesFile != "" {
		if t, err := theme.RandomTheme(cfg.ThemesFile); err != nil {
			logger.Printf("WARN loading theme: %v, using default prompt", err)
		} else {
			logger.Printf("Selected theme: %s", t)
			themePrompt = fmt.Sprintf("Starte ein neues Gespräch. Begrüße den Anrufer kurz und stelle ihm folgende Frage als Gesprächseinstieg: %s", t)
		}
	}

	logger.Println("Generating initial greeting...")
	greeting, err := dialog.Call("", themePrompt)
	if err != nil {
		logger.Printf("ERROR initial LLM call: %v", err)
		ch.Cmd("EXEC StopMusicOnHold")
		return
	}
	logger.Printf("Greeting: %s", greeting)

	if !ch.IsAlive() {
		return
	}

	// Music keeps playing — playTTS stops it once the audio is synthesized.
	sess.WriteHistory("Tutor", greeting)
	if !playTTS(ch, sess, synthesizer, greeting, logger) {
		return
	}

	// Conversation loop
	for turn := 0; turn < maxTurns; turn++ {
		logger.Printf("--- Turn %d ---", turn+1)

		recFile := fmt.Sprintf("/tmp/user_%s_%d", sess.ID, turn)
		sess.AddTempFiles(recFile + ".wav")

		resp := ch.Cmd(fmt.Sprintf("RECORD FILE %s wav \"#\" %d 0 s=%d", recFile, maxRecordMs, silenceSec))
		if !ch.IsAlive() {
			break
		}
		if res, ok := agi.Result(resp); ok && res == -1 {
			logger.Println("Hangup during recording")
			break
		}

		wavPath := recFile + ".wav"
		if _, err := os.Stat(wavPath); os.IsNotExist(err) {
			logger.Println("No recording file")
			continue
		}

		// Start "thinking" music the moment the user stops talking. It masks
		// the latency of STT + LLM + TTS and is stopped inside playTTS, once
		// the reply audio is ready and about to be streamed.
		ch.Cmd("EXEC StartMusicOnHold " + mohClass)
		if !ch.IsAlive() {
			break
		}

		userText, err := transcriber.Transcribe(wavPath)
		if err != nil {
			logger.Printf("ERROR transcribing: %v", err)
			ch.Cmd("EXEC StopMusicOnHold")
			continue
		}
		userText = strings.TrimSpace(userText)
		if userText == "" {
			logger.Println("Empty transcription, skipping")
			nudge, _ := dialog.Call(sess.ReadHistory(), "Der Nutzer hat nichts gesagt. Fordere ihn auf, etwas zu sagen.")
			if nudge == "" {
				ch.Cmd("EXEC StopMusicOnHold")
			} else {
				sess.WriteHistory("Tutor", nudge)
				playTTS(ch, sess, synthesizer, nudge, logger)
			}
			if !ch.IsAlive() {
				break
			}
			continue
		}
		logger.Printf("User said: %s", userText)

		if farewell.IsFarewell(userText) {
			logger.Println("Farewell detected")
			sess.WriteHistory("User", userText)
			fw, _ := dialog.Call(sess.ReadHistory(), userText)
			if fw == "" {
				fw = "Tschüss! Bis zum nächsten Mal!"
			}
			sess.WriteHistory("Tutor", fw)
			playTTS(ch, sess, synthesizer, fw, logger)
			break
		}

		sess.WriteHistory("User", userText)

		history := sess.ReadHistory()
		response, err := dialog.Call(history, userText)
		if err != nil {
			logger.Printf("ERROR calling claude: %v", err)
			ch.Cmd("EXEC StopMusicOnHold")
			continue
		}
		logger.Printf("Tutor: %s", response)

		if !ch.IsAlive() {
			break
		}

		sess.WriteHistory("Tutor", response)
		if !playTTS(ch, sess, synthesizer, response, logger) {
			break
		}
	}

	logger.Println("Session ending")
	if ch.IsAlive() {
		ch.Cmd("HANGUP")
	}
}

// effectiveModel names the model a task will actually run on: the claude
// backend takes its model from CLAUDE_MODEL and ignores the per-task ids.
func effectiveModel(engine, model, claudeModel string) string {
	if engine == llm.EngineClaude {
		return claudeModel
	}
	return model
}

// loadPrompt reads a system-prompt file and strips its YAML frontmatter.
func loadPrompt(path string, logger *log.Logger) string {
	if path == "" {
		logger.Println("WARN: prompt file path is empty")
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		logger.Printf("WARN: cannot read prompt file %s: %v", path, err)
		return ""
	}
	return skill.ExtractContent(string(raw))
}

// playTTS synthesizes the reply and streams it to the caller. Hold music is
// expected to be running on entry: TTS is a network round trip (several seconds
// on the hosted engines), so the music is stopped only after the audio file is
// ready — otherwise the caller sits in silence for the whole synthesis. The
// stop is unconditional so the music never survives a synthesis failure.
func playTTS(ch *agi.Channel, sess *session.Session, synth tts.Synthesizer, text string, logger *log.Logger) bool {
	wavPath, tmpFiles, err := synth.Synthesize(text)
	ch.Cmd("EXEC StopMusicOnHold")
	if err != nil {
		logger.Printf("ERROR synthesizing: %v", err)
		return ch.IsAlive()
	}
	sess.AddTempFiles(tmpFiles...)
	ch.PlayAudio(wavPath)
	return ch.IsAlive()
}
