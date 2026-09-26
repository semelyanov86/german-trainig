package farewell

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

var phrases = []string{
	"tschüss", "tschüs", "tschuss", "auf wiedersehen",
	"auf wiederhören", "bye", "goodbye", "ciao",
	"bis bald", "bis dann", "mach's gut", "machs gut",
}

func IsFarewell(text string) bool {
	return Contains(text, phrases)
}

func Contains(text string, phrases []string) bool {
	lower := strings.ToLower(text)
	for _, f := range phrases {
		if f != "" && strings.Contains(lower, strings.ToLower(f)) {
			return true
		}
	}
	return false
}

// ContainsForLanguage preserves the trainer's historical substring behavior.
// Russian profiles use word boundaries so "покажите" cannot match "пока".
func ContainsForLanguage(text string, phrases []string, language string) bool {
	if language != "ru" {
		return Contains(text, phrases)
	}
	lower := strings.ToLower(text)
	for _, phrase := range phrases {
		phrase = strings.ToLower(strings.TrimSpace(phrase))
		if phrase == "" {
			continue
		}
		for offset := 0; offset < len(lower); {
			i := strings.Index(lower[offset:], phrase)
			if i < 0 {
				break
			}
			start, end := offset+i, offset+i+len(phrase)
			before, after := rune(0), rune(0)
			if start > 0 {
				before, _ = utf8.DecodeLastRuneInString(lower[:start])
			}
			if end < len(lower) {
				after, _ = utf8.DecodeRuneInString(lower[end:])
			}
			if !unicode.IsLetter(before) && !unicode.IsNumber(before) && !unicode.IsLetter(after) && !unicode.IsNumber(after) {
				return true
			}
			offset = start + 1
		}
	}
	return false
}

// IsUtterance matches a whole closing utterance, rather than a goodbye quoted
// within a story. Negations, quoted speech and long monologues cannot hang up.
func IsUtterance(text string, phrases []string) bool {
	if strings.ContainsAny(text, "\"«»“”") {
		return false
	}
	normalize := func(s string) string {
		return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsPunct(r)
		}), " ")
	}
	closing := normalize(text)
	for _, phrase := range phrases {
		if closing != "" && closing == normalize(phrase) {
			return true
		}
	}
	return false
}
