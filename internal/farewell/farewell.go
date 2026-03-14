package farewell

import "strings"

var phrases = []string{
	"tschüss", "tschüs", "tschuss", "auf wiedersehen",
	"auf wiederhören", "bye", "goodbye", "ciao",
	"bis bald", "bis dann", "mach's gut", "machs gut",
}

func IsFarewell(text string) bool {
	lower := strings.ToLower(text)
	for _, f := range phrases {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}
