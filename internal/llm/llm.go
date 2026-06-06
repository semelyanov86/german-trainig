package llm

import (
	"fmt"
	"log"
)

// Chat message roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is a single chat message.
type Message struct {
	Role    string
	Content string
}

// Provider is an LLM backend that produces a single completion.
type Provider interface {
	// Complete returns the assistant reply for the given system prompt and messages.
	Complete(system string, messages []Message) (string, error)
}

// Spec describes how to build a provider for one task (e.g. dialog or summary).
// Optional fields (Temperature, Reasoning, MaxTokens) are sent to the backend
// only when set, so the same code works for both reasoning and plain models.
type Spec struct {
	Engine      string // "polza" (default) or "claude"
	Model       string // provider-specific model id (used by polza)
	ClaudeModel string // model id passed to the Claude CLI (used by claude)
	Temperature string // optional; sent only if a valid float (some models reject it)
	Reasoning   string // optional reasoning effort: minimal|low|medium|high
	MaxTokens   int    // optional; sent only if > 0

	// Shared backend settings.
	PolzaAPIKey string
	ClaudeBin   string
	WorkDir     string
}

// New builds a Provider from a Spec. Defaults to the polza backend.
func New(s Spec, logger *log.Logger) Provider {
	switch s.Engine {
	case "claude":
		return newClaude(s, logger)
	default:
		return newPolza(s, logger)
	}
}

// Conversation adapts a Provider into the German-tutor dialog turn format:
// a fixed system prompt plus a single user message carrying the running
// transcript and the latest utterance.
type Conversation struct {
	provider Provider
	system   string
}

// NewConversation wraps a provider with the tutor system prompt.
func NewConversation(p Provider, systemPrompt string) *Conversation {
	return &Conversation{provider: p, system: systemPrompt}
}

// Call produces the tutor's reply. When history is empty (the very first
// turn) the user message is sent verbatim; otherwise the transcript is
// included and the model is asked to answer only the latest utterance.
func (c *Conversation) Call(history, userMessage string) (string, error) {
	var content string
	if history != "" {
		content = fmt.Sprintf("Gesprächsverlauf:\n%s\n\nLetzte Nachricht des Nutzers: %s\n\nAntworte nur auf die letzte Nachricht.", history, userMessage)
	} else {
		content = userMessage
	}
	return c.provider.Complete(c.system, []Message{{Role: RoleUser, Content: content}})
}
