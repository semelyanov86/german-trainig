package llm

import (
	"strings"
	"testing"
)

type captureProvider struct{ content, system string }

func (p *captureProvider) Complete(system string, messages []Message) (string, error) {
	p.system = system
	p.content = messages[0].Content
	return "ok", nil
}

func TestConversationServiceLanguage(t *testing.T) {
	for _, tc := range []struct{ lang, marker string }{{"de", "Gesprächsverlauf:"}, {"ru", "История разговора:"}} {
		p := &captureProvider{}
		c := NewConversationForLanguage(p, "system", tc.lang)
		if _, err := c.Call("history", "message"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p.content, tc.marker) || !strings.Contains(p.content, "history") || !strings.Contains(p.content, "message") || p.system != "system" {
			t.Fatalf("%s: %q", tc.lang, p.content)
		}
	}
}
