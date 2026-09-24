package tts

import (
	"log"
	"regexp"
	"strings"
	"unicode"
)

// Expression markup is spelled differently by every vendor: Grok Voice takes a
// closed list of inline sounds plus <whisper>…</whisper> wrappers around a
// phrase, Gemini TTS takes any descriptive word in square brackets, ElevenLabs
// v3 has its own audio tags, and Piper or openai/tts-1 understand none at all.
// The dialog model can only write the right markup if it is told which engine
// is listening, so the dialect resolved here is used twice: its Guide is
// appended to the tutor system prompt, and its allowlist filters every reply
// before synthesis. The filter is the half that matters — a tag the engine does
// not know is read out loud to the caller ("eckige Klammer laugh"), which is
// worse than a flat delivery.
type Dialect struct {
	// Name identifies the dialect in the log ("grok", "gemini", "elevenlabs",
	// or "off" when the model must write plain text).
	Name string
	// Guide is the German prompt fragment describing the tags to the dialog
	// model. Empty means the engine supports none.
	Guide string

	inline   map[string]bool // allowed [tag]; ignored when freeform is set
	wrapping map[string]bool // allowed <tag>…</tag>
	freeform bool            // any descriptive word in square brackets is accepted
}

// Supported returns whether the engine understands any expression tag at all.
func (d Dialect) Supported() bool { return d.Guide != "" }

// GuideFor keeps the German production prompt unchanged and gives other
// profiles instructions in their own language. Sanitization remains shared.
func (d Dialect) GuideFor(language string) string {
	if language != "ru" {
		return d.Guide
	}
	switch d.Name {
	case "grok":
		return `## Голос и эмоции

Ответ будет озвучен. Используй не более двух уместных тегов на ответ. Теги пишутся по-английски, произносимый текст остаётся по-русски.
Допустимые отдельные звуки: [pause] [long-pause] [laugh] [cry] [sob] [sigh] [cough] [throat-clear] [smack] [breath] [exhale] [inhale].
Для отрывка текста можно использовать парные теги: <soft> <loud> <shouting> <whisper> <high> <low> <slow> <fast> <singing> <sad> <angry> <happy>.
Не добавляй других тегов. Слова должны быть понятны и без тегов.`
	case "gemini":
		return `## Голос и эмоции

Ответ будет озвучен. Уместно добавь один, максимум два английских тега эмоции в квадратных скобках; произносимый текст остаётся по-русски. Например: [curious] [calm] [serious]. Не заменяй тегами слова и не ставь два тега подряд.`
	case "elevenlabs":
		return `## Голос и эмоции

Ответ будет озвучен. Уместно добавь один, максимум два английских аудиотега в квадратных скобках; произносимый текст остаётся по-русски. Например: [curious] [serious] [sighs]. Не заменяй тегами слова и не ставь два тега подряд.`
	default:
		return ""
	}
}

// markupPattern matches one piece of markup: an expression tag in square or
// angle brackets, or a run of asterisks. The length cap keeps an unclosed
// bracket in ordinary text from swallowing a whole sentence, and German uses
// none of these characters in speech, so nothing legitimate matches. Asterisks
// are never allowed by any dialect — an engine reads "*wirklich*" back as
// "Sternchen wirklich" — but only the markers go: the word between them stays,
// because dropping it would silently swallow an emphasised sentence.
var markupPattern = regexp.MustCompile(`\[[^\[\]\n]{1,40}\]|</?[^<>\n]{1,40}>|\*+`)

var spaceBeforePunct = regexp.MustCompile(`\s+([,.!?;:…])`)

// Sanitize keeps the tags this engine understands and removes everything else.
// The model invents markup — it is asked for [laugh] and writes [lacht] or
// *seufzt* — and an unknown tag is not silently ignored downstream: the engine
// speaks it.
func (d Dialect) Sanitize(text string) string {
	return tidy(markupPattern.ReplaceAllStringFunc(text, func(m string) string {
		if d.allows(m) {
			return m
		}
		return " "
	}))
}

// PlainText strips every expression tag, whichever dialect wrote it. The
// history file feeds the post-call analysis, which grades the caller's German:
// stage directions addressed to a voice engine have no business in it.
func PlainText(text string) string {
	return tidy(markupPattern.ReplaceAllString(text, " "))
}

func (d Dialect) allows(markup string) bool {
	if !strings.HasPrefix(markup, "[") && !strings.HasPrefix(markup, "<") {
		return false
	}
	body := markup[1 : len(markup)-1]
	if strings.HasPrefix(markup, "[") {
		name := tagName(body)
		if d.freeform {
			return name != ""
		}
		return d.inline[name]
	}
	return d.wrapping[tagName(strings.TrimPrefix(body, "/"))]
}

// tagName normalises a tag body into its comparable form and rejects anything
// that is not a word: "<3" and "[1]" are punctuation a caller might hear read
// back, not directions for the voice.
func tagName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && r != ' ' && r != '-' {
			return ""
		}
	}
	return s
}

// tidy closes the gaps a removed tag leaves behind, so the engine is not handed
// "Ach komm .  Das glaubst du nicht".
func tidy(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(spaceBeforePunct.ReplaceAllString(s, "$1"))
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// grokDialect — xAI Grok Voice TTS. A closed list on both sides: inline sounds
// fire at the point they sit at, wrapping tags restyle the phrase they enclose.
// https://docs.x.ai/developers/model-capabilities/audio/text-to-speech
func grokDialect() Dialect {
	return Dialect{
		Name:  "grok",
		Guide: grokGuide,
		inline: set("pause", "long-pause", "laugh", "cry", "sob", "sigh",
			"cough", "throat-clear", "smack", "breath", "exhale", "inhale"),
		wrapping: set("soft", "loud", "shouting", "whisper", "high", "low",
			"slow", "fast", "singing", "sad", "angry", "happy"),
	}
}

// geminiDialect — Google Gemini TTS. No fixed vocabulary: the model interprets
// whatever adverb stands in the brackets, which is why this one is freeform.
// https://ai.google.dev/gemini-api/docs/speech-generation
func geminiDialect() Dialect {
	return Dialect{Name: "gemini", Guide: geminiGuide, freeform: true}
}

// elevenDialect — ElevenLabs v3 audio tags. Same shape as Gemini's: square
// brackets, descriptive word, no closing tag. The older v2/flash models ignore
// them and read them out, so only v3 resolves to this dialect.
func elevenDialect() Dialect {
	return Dialect{Name: "elevenlabs", Guide: elevenGuide, freeform: true}
}

// DialectFor settles which markup the configured voice understands. TTS_STYLE_TAGS
// pins it by hand; "auto" (the default) reads it off the model id, because the
// model is the thing that changes — it lives in the Ansible vault and has moved
// from openai/tts-1 to gemini to grok without a line of code changing here.
func DialectFor(engine string, cfg Config, logger *log.Logger) Dialect {
	switch mode := strings.ToLower(strings.TrimSpace(cfg.StyleTags)); mode {
	case "", "auto":
	case "off", "none":
		return Dialect{Name: "off"}
	case "grok":
		return grokDialect()
	case "gemini":
		return geminiDialect()
	case "elevenlabs":
		return elevenDialect()
	default:
		// A typo must not hand the caller a reply full of spoken brackets, so
		// an unrecognised value falls back to plain text rather than guessing.
		logger.Printf("ERROR unknown TTS_STYLE_TAGS %q, speaking without style tags", mode)
		return Dialect{Name: "off"}
	}

	switch engine {
	case "openrouter":
		return dialectForModel(cfg.OpenRouterTTSModel)
	case "polza":
		return dialectForModel(cfg.PolzaTTSModel)
	case "elevenlabs":
		if strings.Contains(strings.ToLower(cfg.ElevenModel), "v3") {
			return elevenDialect()
		}
	}
	// openai (tts-1, gpt-4o-mini-tts: steered by a separate instructions field,
	// not by markup in the text) and piper (no styling at all).
	return Dialect{Name: "off"}
}

// dialectForModel matches the aggregator model id, which carries the vendor:
// "x-ai/grok-voice-tts-1.0", "google/gemini-3.1-flash-tts-preview".
func dialectForModel(model string) Dialect {
	m := strings.ToLower(model)
	if !strings.Contains(m, "tts") {
		return Dialect{Name: "off"}
	}
	switch {
	case strings.Contains(m, "grok"):
		return grokDialect()
	case strings.Contains(m, "gemini"):
		return geminiDialect()
	case strings.Contains(m, "eleven"):
		return elevenDialect()
	}
	return Dialect{Name: "off"}
}

const grokGuide = `## Stimme und Emotion

Deine Antwort geht an eine Sprachausgabe, die Regie-Tags versteht: sie werden
nicht vorgelesen, sondern gespielt. **Setze in jede Antwort ein Tag** — zwei,
wenn die Antwort wirklich zwei Momente hat, nie mehr als zwei. Eine Antwort
ganz ohne Tag ist die Ausnahme, nicht die Regel: ohne Tag klingt deine Stimme
flach, und genau das ist der Grund, warum es diesen Abschnitt gibt.

Einzelne Laute, genau an der Stelle im Satz:
[pause] [long-pause] [laugh] [cry] [sob] [sigh] [cough] [throat-clear] [smack] [breath] [exhale] [inhale]

Ganze Passagen, Tag öffnen und wieder schließen:
<soft> <loud> <shouting> <whisper> <high> <low> <slow> <fast> <singing> <sad> <angry> <happy>

So sieht das aus:
Ach komm. [laugh] Das glaubst du doch selbst nicht. Was war wirklich los?
<whisper>Das sage ich nur einmal.</whisper> Du windest dich. Warum eigentlich?

Regeln:
- Nur Tags aus diesen beiden Listen, und immer in englischer Schreibweise.
  Alles andere in eckigen oder spitzen Klammern wird vor der Ausgabe gelöscht.
- Tags ersetzen keine Wörter: ohne sie muss der Satz vollständig dastehen.
- Nie zwei Tags direkt hintereinander.
- Deine Frage am Schluss bleibt normal gesprochen.`

const geminiGuide = `## Stimme und Emotion

Deine Antwort geht an eine Sprachausgabe, die Regie-Tags in eckigen Klammern
versteht: sie werden nicht vorgelesen, sondern gespielt. Das Tag-Wort ist
englisch, der gesprochene Text bleibt deutsch.

**Setze in jede Antwort ein Tag** — zwei, wenn die Antwort wirklich zwei
Momente hat, nie mehr als zwei. Eine Antwort ganz ohne Tag ist die Ausnahme,
nicht die Regel: ohne Tag klingt deine Stimme flach, und genau das ist der
Grund, warum es diesen Abschnitt gibt.

Du bist in der Wahl frei, das Modell interpretiert jede Beschreibung. Bewährt:
[sarcastically] [amused] [deadpan] [curious] [serious] [excited] [bored]
[mischievously] [tired] [whispers] [shouting] [very fast] [very slowly]
[laughs] [giggles] [sighs] [gasp] [snorts]

So sieht das aus:
[amused] Ach komm. Das glaubst du doch selbst nicht. Was war wirklich los?
[sarcastically] Sehr bequem. [sighs] Was müsste passieren, damit du aufstehst?

Regeln:
- Das Tag sitzt da, wo ein Mensch auch wirklich so klingen würde — nicht
  dekorativ am Satzanfang, wenn der Satz nüchtern ist.
- Nie zwei Tags direkt hintereinander — dazwischen gehört Text oder ein
  Satzzeichen.
- Tags ersetzen keine Wörter: ohne sie muss der Satz vollständig dastehen.
- Nur eckige Klammern. Spitze Klammern und Sternchen werden gelöscht.
- Deine Frage am Schluss bleibt normal gesprochen.`

const elevenGuide = `## Stimme und Emotion

Deine Antwort geht an eine Sprachausgabe, die Audio-Tags in eckigen Klammern
versteht: sie werden nicht vorgelesen, sondern gespielt. Das Tag-Wort ist
englisch, der gesprochene Text bleibt deutsch.

**Setze in jede Antwort ein Tag** — zwei, wenn die Antwort wirklich zwei
Momente hat, nie mehr als zwei. Eine Antwort ganz ohne Tag ist die Ausnahme,
nicht die Regel.

Bewährt: [laughs] [giggles] [sighs] [sarcastic] [amused] [curious] [excited]
[serious] [whispers] [shouting] [snorts] [exhales] [long pause]

So sieht das aus:
[laughs] Das glaubst du doch selbst nicht. Was war wirklich los?
[sarcastic] Sehr bequem. [sighs] Was müsste passieren, damit du aufstehst?

Regeln:
- Das Tag sitzt da, wo ein Mensch auch wirklich so klingen würde — nicht
  dekorativ am Satzanfang, wenn der Satz nüchtern ist.
- Nie zwei Tags direkt hintereinander.
- Tags ersetzen keine Wörter: ohne sie muss der Satz vollständig dastehen.
- Nur eckige Klammern. Spitze Klammern und Sternchen werden gelöscht.
- Deine Frage am Schluss bleibt normal gesprochen.`
