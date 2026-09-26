package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	"unicode"

	"german-trainer/internal/agi"
	"german-trainer/internal/config"
	"german-trainer/internal/diaglog"
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

	// Samples per second on a standard Asterisk channel; endpos is in samples.
	recordSampleRate = 8000
	// A recording holding less speech than this counts as "the caller said
	// nothing", and the STT round trip is skipped. Asterisk trims the trailing
	// silence, so endpos is roughly the speech it heard: every dead turn of the
	// 2026-08-19 call came back with ~1.02s (8160 samples) of line noise, out of
	// which the STT engine then invented words.
	minSpeechSamples = 9600 // 1.2s
	// After this many dialog failures in a row the call ends with a spoken
	// apology instead of looping through turns the model cannot answer.
	maxConsecutiveErrors = 3
)

// Retry profiles per task. A dialog turn is retried with the caller waiting on
// hold music, so it gives up quickly and lets the spoken fallback take over;
// the post-call report has no listener and can sit out a longer outage.
var (
	dialogRetry = llm.RetryPolicy{
		BaseDelay: 400 * time.Millisecond,
		MaxDelay:  2 * time.Second,
		Timeout:   20 * time.Second,
	}
	summaryRetry = llm.RetryPolicy{
		BaseDelay: 2 * time.Second,
		MaxDelay:  20 * time.Second,
		Timeout:   180 * time.Second,
	}
)

func main() {
	profileID, err := selectedProfile(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := config.LoadProfile(envFile, profileID)
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
	var logger *log.Logger
	if cfg.LogUtterances {
		logger = log.New(lf, "", log.LstdFlags)
	} else {
		logger = log.New(diaglog.Writer{Output: lf, ProfileID: cfg.ProfileID}, "", log.LstdFlags)
	}

	ch := agi.NewChannel(os.Stdin, os.Stdout, logger)
	ch.ReadVars()
	logger.Printf("AGI started, channel=%s callerid=%s tts=%s stt=%s",
		ch.Vars["agi_channel"], ch.Vars["agi_callerid"], cfg.TTSEngine, cfg.STTEngine)

	sess := session.New(cfg.HistoryDir, logger)
	logger.Printf("Session %s, history: %s", sess.ID, sess.HistoryFile)
	// Log the engine and model per task, and resolve the model the way the
	// backend will: on the claude engine the per-task ids are ignored in favour
	// of CLAUDE_MODEL, and printing them anyway used to suggest otherwise.
	logger.Printf("LLM dialog: engine=%s model=%s%s | summary: engine=%s model=%s%s",
		cfg.LLMDialogEngine, effectiveModel(cfg.LLMDialogEngine, cfg.LLMModel, cfg.ClaudeModel),
		fallbackSuffix(cfg.LLMDialogFallbackModels),
		cfg.LLMSummaryEngine, effectiveModel(cfg.LLMSummaryEngine, cfg.LLMSummaryModel, cfg.ClaudeModel),
		fallbackSuffix(cfg.LLMSummaryFallbackModels))

	// System prompts are loaded from files shipped with the app (not from
	// server-side Claude skills), with YAML frontmatter stripped.
	tutorPrompt := loadPrompt(cfg.SkillFile, logger)
	var summaryPrompt string
	if cfg.SummaryEnabled {
		summaryPrompt = loadPrompt(cfg.SummarySkillFile, logger)
	}
	if cfg.ProfileID != "german" && (tutorPrompt == "" || (cfg.SummaryEnabled && summaryPrompt == "")) {
		logger.Println("ERROR named profile prompt is missing or empty")
		return
	}

	summarizer := newSummarizer(cfg, summaryPrompt, logger)
	defer func() {
		if summarizer != nil {
			if err := summarizer.Run(sess.ReadHistory()); err != nil {
				logger.Printf("ERROR generating summary: %v", err)
			}
		}
		sess.Cleanup()
	}()

	transcriber := stt.New(cfg.STTEngine, stt.Config{
		Language:           cfg.STTLanguage,
		GroqAPIKey:         cfg.GroqAPIKey,
		PolzaAPIKey:        cfg.PolzaAPIKey,
		PolzaSTTModel:      cfg.PolzaSTTModel,
		OpenRouterAPIKey:   cfg.OpenRouterAPIKey,
		OpenRouterSTTModel: cfg.OpenRouterSTTModel,
		CustomSTTURL:       cfg.CustomSTTURL,
		CustomSTTAPIKey:    cfg.CustomSTTAPIKey,
		CustomSTTModel:     cfg.CustomSTTModel,
		CustomSTTLanguage:  cfg.CustomSTTLanguage,
		CustomSTTTimeout:   cfg.CustomSTTTimeout,
		FallbackEngine:     cfg.STTFallbackEngine,
		FallbackTimeout:    cfg.STTFallbackTimeout,
	}, logger)
	synthesizer, voice := tts.New(cfg.TTSEngine, tts.Config{
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
		StyleTags:           cfg.TTSStyleTags,
		LogUtterances:       cfg.LogUtterances,
	}, logger)
	// The tutor writes the expression tags itself, so the vocabulary of the
	// voice that will read the reply is appended to its system prompt. Which
	// vocabulary that is depends on the TTS model, and the model is a vault
	// setting: the prompt has to follow it instead of naming one set of tags.
	if voice.Supported() {
		logger.Printf("TTS style tags: %s dialect", voice.Name)
		tutorPrompt = tutorPrompt + "\n\n" + voice.GuideFor(cfg.Language)
	} else {
		logger.Printf("TTS style tags: none, %s speaks plain text", cfg.TTSEngine)
	}
	dialogProvider := llm.New(dialogSpec(cfg), logger)
	dialog := llm.NewConversationForLanguage(dialogProvider, tutorPrompt, cfg.Language)

	ch.Cmd("ANSWER")
	if !ch.IsAlive() {
		return
	}

	// Play music while generating greeting
	ch.Cmd("EXEC StartMusicOnHold " + mohClass)
	if !ch.IsAlive() {
		return
	}

	themePrompt := cfg.GreetingPrompt
	if cfg.ThemesFile != "" {
		if t, err := theme.RandomTheme(cfg.ThemesFile); err != nil {
			logger.Printf("WARN loading theme: %v, using default prompt", err)
		} else {
			if cfg.LogUtterances {
				logger.Printf("Selected theme: %s", t)
			}
			themePrompt = strings.ReplaceAll(cfg.ThemePrompt, "{theme}", t)
		}
	}

	logger.Println("Generating initial greeting...")
	greeting, err := dialog.Call("", themePrompt)
	if err != nil {
		logger.Printf("ERROR initial LLM call: %v", err)
		// The caller is on hold music waiting to be greeted. Dropping the call
		// here used to leave them listening to silence with no idea why; playTTS
		// stops the music and speaks, so they at least hear that to call later.
		playTTS(ch, sess, synthesizer, cfg.OutageLine, logger)
		return
	}
	if cfg.LogUtterances {
		logger.Printf("Greeting: %s", greeting)
	}

	if !ch.IsAlive() {
		return
	}

	// Music keeps playing — playTTS stops it once the audio is synthesized.
	sess.WriteHistory(cfg.HistoryRole, tts.PlainText(greeting))
	if !playTTS(ch, sess, synthesizer, greeting, logger) {
		return
	}

	// Conversation loop. consecutiveErrors bounds a provider outage: without it
	// a model that keeps failing turns the call into a silent spin — record
	// nothing, transcribe noise, fail, repeat (2026-08-19).
	consecutiveErrors := 0
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

		// Skip the STT round trip when the recording holds no speech: on a
		// silent line the engine invents short phrases ("다섯", "Jeg heter") and
		// each invention used to become a real conversation turn.
		if n, ok := agi.Endpos(resp); ok && n < minSpeechSamples {
			logger.Printf("Recording holds only %.1fs of speech, treating as silence", speechSeconds(n))
			if !promptForSpeech(ch, sess, synthesizer, dialog, cfg, logger) {
				break
			}
			continue
		}

		userText, err := transcriber.Transcribe(wavPath)
		if err != nil {
			logger.Printf("ERROR transcribing: %v", err)
			ch.Cmd("EXEC StopMusicOnHold")
			continue
		}
		userText = strings.TrimSpace(userText)
		if userText == "" || !hasSpeechScript(userText, cfg.Language) {
			if cfg.LogUtterances {
				logger.Printf("No usable transcription (%q), asking the caller to speak", userText)
			} else {
				logger.Println("No usable transcription, asking the caller to speak")
			}
			if !promptForSpeech(ch, sess, synthesizer, dialog, cfg, logger) {
				break
			}
			continue
		}
		if cfg.LogUtterances {
			logger.Printf("User said: %s", userText)
		}

		if farewell.ContainsForLanguage(userText, cfg.FarewellPhrases, cfg.Language) {
			logger.Println("Farewell detected")
			sess.WriteHistory("User", userText)
			fw, err := dialog.Call(sess.ReadHistory(), userText)
			if err != nil || strings.TrimSpace(fw) == "" {
				logger.Printf("WARN farewell reply unavailable (%v), using the fixed line", err)
				fw = cfg.FarewellLine
			}
			sess.WriteHistory(cfg.HistoryRole, tts.PlainText(fw))
			playTTS(ch, sess, synthesizer, fw, logger)
			break
		}

		sess.WriteHistory("User", userText)

		history := sess.ReadHistory()
		response, err := dialog.Call(history, userText)
		if err != nil {
			consecutiveErrors++
			logger.Printf("ERROR dialog LLM (%d in a row): %v", consecutiveErrors, err)
			// Say something either way — stopping the music and recording again
			// is what made the failure feel like a frozen call.
			if consecutiveErrors >= maxConsecutiveErrors {
				logger.Printf("Giving up after %d failed turns in a row", consecutiveErrors)
				playTTS(ch, sess, synthesizer, cfg.OutageLine, logger)
				break
			}
			if !playTTS(ch, sess, synthesizer, cfg.GlitchLine, logger) {
				break
			}
			continue
		}
		consecutiveErrors = 0
		if cfg.LogUtterances {
			logger.Printf("%s: %s", cfg.HistoryRole, response)
		}

		if !ch.IsAlive() {
			break
		}

		sess.WriteHistory(cfg.HistoryRole, tts.PlainText(response))
		if !playTTS(ch, sess, synthesizer, response, logger) {
			break
		}
	}

	logger.Println("Session ending")
	if ch.IsAlive() {
		ch.Cmd("HANGUP")
	}
}

func selectedProfile(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("expected at most one profile ID")
	}
	if len(args) == 0 {
		return "", nil
	}
	return args[0], nil
}

// A disabled profile never constructs a report provider or sends a webhook.
func newSummarizer(cfg *config.Config, prompt string, logger *log.Logger) *summary.Summarizer {
	if !cfg.SummaryEnabled {
		return nil
	}
	provider := llm.New(summarySpec(cfg), logger)
	return summary.NewWithPolicy(provider, prompt, cfg.NotifyWebhookURL, cfg.NotifyWebhookToken, cfg.SummaryPrefix, cfg.LogUtterances, logger)
}

func baseLLMSpec(cfg *config.Config) llm.Spec {
	return llm.Spec{
		ClaudeModel:             cfg.ClaudeModel,
		PolzaAPIKey:             cfg.PolzaAPIKey,
		OpenRouterAPIKey:        cfg.OpenRouterAPIKey,
		ClaudeBin:               cfg.ClaudeBin,
		CodexBin:                cfg.CodexBin,
		CodexRunner:             cfg.CodexRunner,
		WorkDir:                 cfg.HistoryDir,
		ClaudeMaxOutputTokens:   cfg.ClaudeMaxOutputTokens,
		ClaudeMaxThinkingTokens: cfg.ClaudeMaxThinkingTokens,
		ClaudeEffort:            cfg.ClaudeEffort,
	}
}

func dialogSpec(cfg *config.Config) llm.Spec {
	s := baseLLMSpec(cfg)
	s.Engine = cfg.LLMDialogEngine
	s.Model = cfg.LLMModel
	s.Temperature = cfg.LLMDialogTemperature
	s.Reasoning = cfg.LLMDialogReasoning
	s.MaxTokens = cfg.LLMDialogMaxTokens
	s.FallbackModels = cfg.LLMDialogFallbackModels
	s.Retry = withAttempts(dialogRetry, cfg.LLMDialogRetries)
	return s
}

func summarySpec(cfg *config.Config) llm.Spec {
	s := baseLLMSpec(cfg)
	s.Engine = cfg.LLMSummaryEngine
	s.Model = cfg.LLMSummaryModel
	s.Temperature = cfg.LLMSummaryTemperature
	s.Reasoning = cfg.LLMSummaryReasoning
	s.MaxTokens = cfg.LLMSummaryMaxTokens
	s.FallbackModels = cfg.LLMSummaryFallbackModels
	s.Retry = withAttempts(summaryRetry, cfg.LLMSummaryRetries)
	return s
}

// withAttempts fills in the attempt count of a retry profile from the config.
func withAttempts(p llm.RetryPolicy, attempts int) llm.RetryPolicy {
	p.Attempts = attempts
	return p
}

// fallbackSuffix renders a fallback chain for the startup log line, so the log
// shows which models a turn could land on and not just the one it asked for.
func fallbackSuffix(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return " (fallback: " + strings.Join(models, ", ") + ")"
}

// speechSeconds converts an endpos sample count into seconds.
func speechSeconds(samples int) float64 {
	return float64(samples) / recordSampleRate
}

// hasSpeechScript keeps the line-noise guard while admitting Cyrillic for a
// Russian profile. Punctuation or speech in unrelated scripts is discarded.
func hasSpeechScript(s, language string) bool {
	for _, r := range s {
		if (language == "ru" && unicode.Is(unicode.Cyrillic, r)) || (language != "ru" && unicode.Is(unicode.Latin, r)) {
			return true
		}
	}
	return false
}

// promptForSpeech asks the caller to say something. The wording normally comes
// from the tutor model so it fits the conversation; a fixed line stands in when
// the model is unavailable, since silence is exactly what this branch exists to
// avoid. Hold music is expected to be running — playTTS stops it.
func promptForSpeech(ch *agi.Channel, sess *session.Session, synth tts.Synthesizer, dialog *llm.Conversation, cfg *config.Config, logger *log.Logger) bool {
	line, err := dialog.Call(sess.ReadHistory(), cfg.SilencePrompt)
	if err != nil || strings.TrimSpace(line) == "" {
		logger.Printf("WARN nudge unavailable (%v), using the fixed line", err)
		line = cfg.SilenceLine
	}
	sess.WriteHistory(cfg.HistoryRole, tts.PlainText(line))
	return playTTS(ch, sess, synth, line, logger)
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
