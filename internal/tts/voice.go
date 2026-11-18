package tts

import (
	"errors"
	"log"
	"strings"
)

// Audio is a synthesized reply on its way to the caller, handed out as one or
// more files to play in order. A file engine produces one finished file. The
// custom engine produces the reply while it is being spoken: each Next returns
// the next piece, cut at a pause, so playback starts about a second after the
// request instead of after the whole reply has been generated.
type Audio interface {
	// Next returns the next file to play, or "" once the reply is over. A
	// non-nil error with "" reports a reply that broke off: every piece that
	// did arrive has already been handed out.
	Next() (string, error)
	// Files lists the temporary files written so far, for session cleanup.
	// It is meaningful after a failed Speak too, for partial output.
	Files() []string
	// Stop abandons the reply (the caller hung up) and cancels any synthesis
	// still running.
	Stop()
}

// Speaker is what a call talks through.
type Speaker interface {
	// Speak never returns a nil Audio, so its Files can always be collected.
	Speak(text string) (Audio, error)
}

type speaker interface {
	speak(text string) (Audio, error)
}

type fileAudio struct {
	path   string
	files  []string
	handed bool
}

func (a *fileAudio) Next() (string, error) {
	if a.handed || a.path == "" {
		return "", nil
	}
	a.handed = true
	return a.path, nil
}

func (a *fileAudio) Files() []string { return a.files }
func (a *fileAudio) Stop()           {}

// Voice speaks for one call. With the custom engine and TTS_FALLBACK_ENGINE
// set, a failure of the server moves the rest of the call to the fallback —
// once and for good, the rule of our own TTS service: a call keeps one voice whenever it
// can, and a caller who already heard the fallback is not switched back and
// forth between turns. A reply the service rejected (400/413) or that its
// model broke off twice is a problem of that reply, not of the service: only
// that reply goes through the fallback.
type Voice struct {
	primary, fallback       speaker
	primaryName, backupName string
	onFallback              bool
	session                 *customSession
	logger                  *log.Logger
	// broken counts replies in a row that broke off while they played, the
	// one failure Speak cannot see itself; brokenPhrase tells whether the
	// last one was the text's fault rather than the service's.
	broken       int
	brokenPhrase bool
}

// Open prepares the voice of one call and returns the expression-tag dialect
// the call's prompt must use. For the custom engine it reserves the call's
// place on the server first: a call that gets no place speaks entirely through
// the fallback, so the dialog model is given the fallback's tags from the
// start. Close must be called when the call ends.
//
// The fallback belongs to the custom engine only: the file engines have no
// session and no per-reply retry, and one failed ffmpeg run on them must not
// move a whole call to another voice.
func Open(engine string, cfg Config, logger *log.Logger) (*Voice, Dialect) {
	v := &Voice{primaryName: engine, logger: logger}
	if engine != "custom" {
		synth, dialect := newStyled(engine, cfg, logger)
		v.primary = synth
		return v, dialect
	}

	var backupDialect Dialect
	v.backupName, v.fallback, backupDialect = fallbackFor(cfg, logger)
	dialect := DialectFor(engine, cfg, logger)
	client := newCustomClient(cfg, logger)
	v.primary = &customSpeaker{client: client, dialect: dialect, logger: logger, logUtterances: cfg.LogUtterances}
	session, err := client.openSession(sessionID(cfg))
	switch {
	case err == nil:
		v.session = session
		client.session = session
	case v.fallback != nil:
		logger.Printf("WARN TTS custom has no place for this call (%v), the call speaks through %s", err, v.backupName)
		v.onFallback, dialect = true, backupDialect
	default:
		// Without a fallback the service is still the only voice there is;
		// replies without a session are served while it has spare capacity.
		logger.Printf("ERROR custom TTS session not opened (%v) and no TTS_FALLBACK_ENGINE, speaking without a session", err)
	}
	return v, dialect
}

// Speak synthesizes one reply, on the fallback when the primary engine fails.
func (v *Voice) Speak(text string) (Audio, error) {
	if v.onFallback {
		return v.fallback.speak(text)
	}
	// A reply that broke off during playback is repeated once on the same
	// voice (the caller asks for it again); a second break in a row is the
	// same verdict as a failure before the first audio.
	if v.broken >= 2 && v.fallback != nil {
		v.broken = 0
		if v.brokenPhrase {
			v.logger.Printf("WARN TTS %s rejected this reply (it broke off twice during playback), speaking it through %s", v.primaryName, v.backupName)
		} else {
			v.logger.Printf("WARN TTS %s failed (replies broke off twice during playback), the rest of the call speaks through %s", v.primaryName, v.backupName)
			v.onFallback = true
		}
		return v.fallback.speak(text)
	}
	audio, err := v.primary.speak(text)
	if err == nil {
		return &watchedAudio{Audio: audio, voice: v}, nil
	}
	if v.fallback == nil || errors.Is(err, errNothingToSpeak) {
		return audio, err
	}
	var failure *customError
	if errors.As(err, &failure) && failure.phrase {
		v.logger.Printf("WARN TTS %s rejected this reply (%v), speaking it through %s", v.primaryName, err, v.backupName)
	} else {
		v.logger.Printf("WARN TTS %s failed (%v), the rest of the call speaks through %s", v.primaryName, err, v.backupName)
		v.onFallback = true
	}
	return v.fallback.speak(text)
}

// watchedAudio tells the Voice how a reply of the primary engine ended.
type watchedAudio struct {
	Audio
	voice *Voice
}

func (a *watchedAudio) Next() (string, error) {
	path, err := a.Audio.Next()
	if path == "" {
		a.voice.ended(err)
	}
	return path, err
}

func (v *Voice) ended(err error) {
	if err == nil {
		v.broken = 0
		return
	}
	var failure *customError
	v.broken++
	v.brokenPhrase = errors.As(err, &failure) && failure.phrase
}

// Close gives the call's place on the server back. A forgotten place frees
// itself after five minutes, but a slot held that long after the hangup is
// a slot another call could not get.
func (v *Voice) Close() {
	if v.session != nil {
		v.session.close()
		v.session = nil
	}
}

// fallbackFor builds the fallback engine with its own dialect. It filters the
// reply with that dialect alone; the custom engine's <|…|> tags fall outside
// every other dialect's allowlist, so they are stripped rather than read out
// (a sound tag's onomatopoeia, "Haha,", stays and is spoken).
func fallbackFor(cfg Config, logger *log.Logger) (string, speaker, Dialect) {
	name := strings.ToLower(strings.TrimSpace(cfg.FallbackEngine))
	switch {
	case name == "":
		return "", nil, Dialect{}
	case !fileEngines[name]:
		// A typo must not route the call to an engine nobody chose, and the
		// custom engine cannot stand in for itself.
		logger.Printf("ERROR unknown TTS_FALLBACK_ENGINE %q, no TTS fallback", name)
		return "", nil, Dialect{}
	}
	if strings.EqualFold(strings.TrimSpace(cfg.StyleTags), "higgs") {
		cfg.StyleTags = "auto" // pinned for the primary; the fallback reads its own
	}
	synth, dialect := newStyled(name, cfg, logger)
	return name, synth, dialect
}
