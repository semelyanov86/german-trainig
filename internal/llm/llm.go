package llm

import (
	"fmt"
	"log"
	"time"
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

// Available backends. Each task (dialog, summary) picks one independently.
const (
	EnginePolza      = "polza"
	EngineOpenRouter = "openrouter"
	EngineClaude     = "claude"
)

// RetryPolicy bounds the automatic retries of a temporary provider failure
// (HTTP 429/5xx, connection errors). The two tasks want very different profiles:
// a dialog turn happens while the caller waits on hold music, so it retries fast
// and few times, while the post-call summary has nobody listening and can wait.
type RetryPolicy struct {
	Attempts  int           // total attempts including the first; < 1 means 1
	BaseDelay time.Duration // first backoff step, doubled on each further attempt
	MaxDelay  time.Duration // cap for the backoff and for any Retry-After hint
	Timeout   time.Duration // per-attempt HTTP timeout
}

// Built-in retry profile, used for any field the caller left at zero.
const (
	defaultRetryAttempts  = 3
	defaultRetryBaseDelay = 500 * time.Millisecond
	defaultRetryMaxDelay  = 5 * time.Second
	defaultRetryTimeout   = 120 * time.Second
)

func (r RetryPolicy) withDefaults() RetryPolicy {
	if r.Attempts < 1 {
		r.Attempts = defaultRetryAttempts
	}
	if r.BaseDelay <= 0 {
		r.BaseDelay = defaultRetryBaseDelay
	}
	if r.MaxDelay <= 0 {
		r.MaxDelay = defaultRetryMaxDelay
	}
	if r.Timeout <= 0 {
		r.Timeout = defaultRetryTimeout
	}
	return r
}

// Spec describes how to build a provider for one task (e.g. dialog or summary).
// Optional fields (Temperature, Reasoning, MaxTokens) are sent to the backend
// only when set, so the same code works for both reasoning and plain models.
type Spec struct {
	Engine      string // EnginePolza (default), EngineOpenRouter or EngineClaude
	Model       string // provider-specific model id (used by polza and openrouter)
	ClaudeModel string // model id passed to the Claude CLI (used by claude)
	Temperature string // optional; sent only if a valid float (some models reject it)
	Reasoning   string // optional reasoning effort: minimal|low|medium|high
	MaxTokens   int    // optional; sent only if > 0

	// FallbackModels are tried, in order, when Model errors out. Sent as
	// OpenRouter's "models" array, which is why this is openrouter-only: the
	// router itself moves down the list inside a single request, so an upstream
	// rate limit on Model costs no extra round trip. Every id is validated up
	// front, so one typo rejects the whole request — the provider notices that
	// and retries without the list rather than failing the turn.
	FallbackModels []string

	// Retry bounds the retries of a temporary failure; zero fields take the
	// built-in profile.
	Retry RetryPolicy

	// Shared backend settings.
	PolzaAPIKey      string
	OpenRouterAPIKey string
	ClaudeBin        string
	WorkDir          string

	// Claude CLI run settings (see config.Config); zero values are omitted.
	ClaudeMaxOutputTokens   int
	ClaudeMaxThinkingTokens int
	ClaudeEffort            string
}

// New builds a Provider from a Spec. Defaults to the polza backend.
func New(s Spec, logger *log.Logger) Provider {
	switch s.Engine {
	case EngineClaude:
		return newClaude(s, logger)
	case EngineOpenRouter:
		return newOpenRouter(s, logger)
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
