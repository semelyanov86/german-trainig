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
	"german-trainer/internal/stt"
	"german-trainer/internal/summary"
	"german-trainer/internal/tts"
)

const (
	maxTurns    = 25
	maxRecordMs = 30000
	silenceSec  = 3
	logFile     = "/tmp/german_trainer.log"
	envFile     = "/etc/german-trainer/.env"
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

	summarizer := summary.New(
		cfg.ClaudeBin, cfg.HistoryDir, cfg.SummarySkillFile,
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
		GroqAPIKey:    cfg.GroqAPIKey,
		PolzaAPIKey:   cfg.PolzaAPIKey,
		PolzaSTTModel: cfg.PolzaSTTModel,
	}, logger)
	synthesizer := tts.New(cfg.TTSEngine, tts.Config{
		SessionID:     sess.ID,
		ElevenAPIKey:  cfg.ElevenAPIKey,
		ElevenVoiceID: cfg.ElevenVoiceID,
		ElevenModel:   cfg.ElevenModel,
		OpenAIAPIKey:  cfg.OpenAIAPIKey,
		OpenAIModel:   cfg.OpenAIModel,
		OpenAIVoice:   cfg.OpenAIVoice,
		PiperModel:    cfg.PiperModel,
		PolzaAPIKey:   cfg.PolzaAPIKey,
		PolzaTTSModel: cfg.PolzaTTSModel,
		PolzaTTSVoice: cfg.PolzaTTSVoice,
	}, logger)
	claude := llm.NewClaude(cfg.ClaudeBin, cfg.ClaudeModel, cfg.HistoryDir, cfg.SkillFile, logger)

	ch.Cmd("ANSWER")
	if !ch.IsAlive() {
		return
	}

	// Play music while generating greeting
	ch.Cmd("EXEC StartMusicOnHold default")
	if !ch.IsAlive() {
		return
	}

	logger.Println("Generating initial greeting...")
	greeting, err := claude.Call("", "Starte ein neues Gespräch. Begrüße den Anrufer und schlage ein Thema vor.")
	if err != nil {
		logger.Printf("ERROR initial claude call: %v", err)
		ch.Cmd("EXEC StopMusicOnHold")
		return
	}
	logger.Printf("Greeting: %s", greeting)

	ch.Cmd("EXEC StopMusicOnHold")
	if !ch.IsAlive() {
		return
	}

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
		if strings.Contains(resp, "result=-1") {
			logger.Println("Hangup during recording")
			break
		}

		wavPath := recFile + ".wav"
		if _, err := os.Stat(wavPath); os.IsNotExist(err) {
			logger.Println("No recording file")
			continue
		}

		userText, err := transcriber.Transcribe(wavPath)
		if err != nil {
			logger.Printf("ERROR transcribing: %v", err)
			continue
		}
		userText = strings.TrimSpace(userText)
		if userText == "" {
			logger.Println("Empty transcription, skipping")
			nudge, _ := claude.Call(sess.ReadHistory(), "Der Nutzer hat nichts gesagt. Fordere ihn auf, etwas zu sagen.")
			if nudge != "" {
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
			fw, _ := claude.Call(sess.ReadHistory(), userText)
			if fw == "" {
				fw = "Tschüss! Bis zum nächsten Mal!"
			}
			sess.WriteHistory("Tutor", fw)
			playTTS(ch, sess, synthesizer, fw, logger)
			break
		}

		sess.WriteHistory("User", userText)

		ch.Cmd("EXEC StartMusicOnHold default")
		if !ch.IsAlive() {
			break
		}

		history := sess.ReadHistory()
		response, err := claude.Call(history, userText)
		if err != nil {
			logger.Printf("ERROR calling claude: %v", err)
			ch.Cmd("EXEC StopMusicOnHold")
			continue
		}
		logger.Printf("Claude: %s", response)

		ch.Cmd("EXEC StopMusicOnHold")
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

func playTTS(ch *agi.Channel, sess *session.Session, synth tts.Synthesizer, text string, logger *log.Logger) bool {
	wavPath, tmpFiles, err := synth.Synthesize(text)
	if err != nil {
		logger.Printf("ERROR synthesizing: %v", err)
		return ch.IsAlive()
	}
	sess.AddTempFiles(tmpFiles...)
	ch.PlayAudio(wavPath)
	return ch.IsAlive()
}
