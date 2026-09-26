package tts

import (
	"log"
	"regexp"
	"strconv"
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
	case "yandex":
		return yandexGuide
	default:
		return ""
	}
}

// markupPattern matches one piece of markup: an expression tag in square or
// angle brackets, or a run of asterisks. The length cap keeps an unclosed
// bracket in ordinary text from swallowing a whole sentence. Yandex native
// pauses and paired emphasis must be matched before generic brackets/stars.
// Other dialects strip the asterisks, retaining the word between them,
// because dropping it would silently swallow an emphasised sentence.
var markupPattern = regexp.MustCompile(`\*\*[^*\[\]<>\n]{1,120}\*\*|(?:sil)?<\[[^<>\[\]\n]{1,40}\]>|\[\[[^\[\]\n]{1,120}\]\]|\[[^\[\]\n]{1,40}\]|</?[^<>\n]{1,40}>|\*+`)

var yandexStressPattern = regexp.MustCompile(`\+([аеёиоуыэюяАЕЁИОУЫЭЮЯ])`)

var spaceBeforePunct = regexp.MustCompile(`\s+([,.!?;:…])`)

// Sanitize keeps the tags this engine understands and removes everything else.
// The model invents markup — it is asked for [laugh] and writes [lacht] or
// *seufzt* — and an unknown tag is not silently ignored downstream: the engine
// speaks it.
func (d Dialect) Sanitize(text string) string {
	if d.Name != "yandex" {
		text = yandexStressPattern.ReplaceAllString(text, "$1")
	}
	return tidy(markupPattern.ReplaceAllStringFunc(text, func(m string) string {
		if d.allows(m) {
			return m
		}
		return stripMarkup(m)
	}))
}

// PlainText strips every expression tag, whichever dialect wrote it. The
// history file feeds the post-call analysis, which grades the caller's German:
// stage directions addressed to a voice engine have no business in it.
func PlainText(text string) string {
	text = yandexStressPattern.ReplaceAllString(text, "$1")
	return tidy(markupPattern.ReplaceAllStringFunc(text, stripMarkup))
}

func stripMarkup(markup string) string {
	if strings.HasPrefix(markup, "**") && strings.HasSuffix(markup, "**") && len(markup) > 4 {
		return " " + markup[2:len(markup)-2] + " "
	}
	return " "
}

func (d Dialect) allows(markup string) bool {
	if d.Name == "yandex" {
		if strings.HasPrefix(markup, "**") && strings.HasSuffix(markup, "**") && len(markup) > 4 {
			return true
		}
		switch markup {
		case "<[tiny]>", "<[small]>", "<[medium]>", "<[large]>", "<[huge]>", "<[accented]>":
			return true
		}
		if strings.HasPrefix(markup, "sil<[") && strings.HasSuffix(markup, "]>") {
			n, err := strconv.Atoi(markup[5 : len(markup)-2])
			return err == nil && n >= 1 && n <= 7000
		}
		return false
	}
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
	case "yandex":
		return Dialect{Name: "yandex", Guide: yandexGuide}
	default:
		// A typo must not hand the caller a reply full of spoken brackets, so
		// an unrecognised value falls back to plain text rather than guessing.
		logger.Printf("ERROR unknown TTS_STYLE_TAGS %q, speaking without style tags", mode)
		return Dialect{Name: "off"}
	}

	switch engine {
	case "yandex":
		return Dialect{Name: "yandex", Guide: yandexGuide}
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

// SpeechKit v3 uses native TTS markup, not SSML or Grok/Gemini emotion tags.
// Voice and role are request-wide API hints configured separately.
// https://aistudio.yandex.ru/ru/docs/speechkit/tts/markup/tts-markup
const yandexGuide = `## Голос и интонация

Ответ озвучивает Яндекс SpeechKit. Голос и амплуа уже заданы приложением; произносимый текст остаётся по-русски. Эмоцию передавай естественными словами, пунктуацией и уместными паузами. Не имитируй плач или смех собеседника.
Поддерживаемая разметка:
- Паузы по контексту: <[tiny]> <[small]> <[medium]> <[large]> <[huge]>.
- Пауза в миллисекундах: sil<[300]>. Допустимо от 1 до 7000 мс, в диалоге предпочитай 200–600 мс.
- Акцент на слове: <[accented]>слово или **слово**.
- Ударение: + перед нужной гласной, например зам+ок. Используй только при неоднозначном произношении.
Добавляй разметку по смыслу, обычно не больше двух пауз или акцентов на ответ. Пауза должна находиться между словами или предложениями, не в начале и не в конце ответа. Не ставь два маркера подряд.
Нет тегов эмоции [sad], [sigh], <soft>, <whisper> и других тегов сторонних TTS. Не используй SSML и фонемы [[...]]. Разметка не заменяет произносимые слова.
Пример: Похоже, тебе сейчас очень непросто. <[small]> Что из этого тревожит тебя сильнее всего?`

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

Deine Antwort geht an Grok Voice, das nur die folgenden Regie-Tags versteht: sie werden
nicht vorgelesen, sondern gespielt. **Setze in jede Antwort ein Tag** — zwei,
wenn die Antwort wirklich zwei Momente hat, nie mehr als zwei. Eine Antwort
ganz ohne Tag ist die Ausnahme, nicht die Regel: ohne Tag klingt deine Stimme
flach, und genau das ist der Grund, warum es diesen Abschnitt gibt.

Es gibt genau zwei Tag-Formen. Verwechsle ihre Klammern nicht:

1. Einzelne Laute stehen in eckigen Klammern, ohne Schlusstag, genau an der Stelle im Satz:
[pause] [long-pause] [laugh] [cry] [sob] [sigh] [cough] [throat-clear] [smack] [breath] [exhale] [inhale]
Nur diese zwölf Laute dürfen in eckigen Klammern stehen.

2. Die Sprechweise steht in spitzen Klammern und umschließt gesprochenen Text.
Jedes öffnende Tag braucht das passende Schlusstag direkt nach der Passage:
<soft>Text</soft> <loud>Text</loud> <shouting>Text</shouting> <whisper>Text</whisper>
<high>Text</high> <low>Text</low> <slow>Text</slow> <fast>Text</fast>
<singing>Text</singing> <sad>Text</sad> <angry>Text</angry> <happy>Text</happy>
Ein solches Paar zählt als ein Tag. Es darf nie allein vor oder nach der Antwort stehen.

Verboten sind insbesondere [soft], [/soft], [low], [slow] und [whisper].
soft und low sind Sprechweisen: schreibe immer <soft>Text</soft> bzw. <low>Text</low>.
Erfinde keine anderen Tags, auch nicht [thoughtful] oder [sarcastically].
Ungültige Tags werden gelöscht; ihre Emotion kommt dann nicht beim Hörer an.

Korrekte Beispiele, deren Text ohne Tags ebenfalls vollständig ist:
<soft>Das klingt enttäuschend.</soft> Was hättest du dir von ihm gewünscht?
<low>Du hast ihm also vertraut.</low> Woran hast du gemerkt, dass das ein Fehler war?
<slow>Ein enttäuschender Abend erklärt noch nicht alles.</slow> Hast du ihn danach darauf angesprochen?
Ach komm. [laugh] Das glaubst du doch selbst nicht. Was war wirklich los?
<whisper>Das sage ich nur einmal.</whisper> Du windest dich. Warum eigentlich?

Regeln:
- Nur Tags aus diesen beiden Listen, und immer in englischer Schreibweise.
  Alles andere in eckigen oder spitzen Klammern wird vor der Ausgabe gelöscht.
- Tags ersetzen keine Wörter: ohne sie muss der Satz vollständig dastehen.
- Nie zwei Tags direkt hintereinander.
- Verschachtele keine Tags. Deine Frage am Schluss bleibt außerhalb der Tags und normal gesprochen.

Prüfe vor der Ausgabe still: mindestens ein gültiges Tag, nur Namen aus diesen Listen,
Laute in [eckigen Klammern], Sprechweisen ausschließlich als <name>Text</name> mit
passendem Schlusstag. Korrigiere jeden Formatfehler, bevor du antwortest.
Gib nur die fertige Antwort aus, ohne diese Prüfung zu erwähnen.`

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
