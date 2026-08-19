package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// chatBackend describes one OpenAI-compatible chat provider. polza.ai and
// openrouter.ai speak the same dialect, so they differ only in these fields.
type chatBackend struct {
	name         string // label used in log lines
	endpoint     string // full chat completions URL
	apiKey       string
	includeUsage bool // ask the API to report cost (OpenRouter needs the opt-in)
	// supportsModelFallback marks a backend that understands the "models" array
	// (OpenRouter): the request carries a list of model ids and the router moves
	// down it when one errors out. polza has no such field, so the list is
	// simply not sent there.
	supportsModelFallback bool
}

// openAIChatProvider talks to any OpenAI-compatible /chat/completions endpoint.
type openAIChatProvider struct {
	backend        chatBackend
	model          string
	fallbackModels []string
	temperature    string
	reasoning      string
	maxTokens      int
	retry          RetryPolicy
	logger         *log.Logger
}

func newOpenAIChat(b chatBackend, s Spec, logger *log.Logger) *openAIChatProvider {
	return &openAIChatProvider{
		backend:        b,
		model:          s.Model,
		fallbackModels: s.FallbackModels,
		temperature:    s.Temperature,
		reasoning:      s.Reasoning,
		maxTokens:      s.MaxTokens,
		retry:          s.Retry.withDefaults(),
		logger:         logger,
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// errPermanent marks a failure that cannot be fixed by trying again (a
// malformed request of our own making), so the retry loop gives up at once.
var errPermanent = errors.New("permanent error")

// apiError is a non-2xx reply. It keeps the status code so the retry loop can
// tell a temporary failure (429/5xx — worth another attempt) from a permanent
// one (400/401/402 — retrying it only makes the caller wait longer).
type apiError struct {
	backend    string
	status     int
	body       string
	retryAfter time.Duration // from the Retry-After header, 0 when absent
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s chat HTTP %d: %s", e.backend, e.status, e.body)
}

// transient reports whether another attempt could plausibly succeed. 408, 429
// and every 5xx are the codes documented as temporary (429 and 503 even carry
// Retry-After); a 400/401/402 means the request itself is wrong.
func (e *apiError) transient() bool {
	return e.status == http.StatusRequestTimeout ||
		e.status == http.StatusTooManyRequests ||
		e.status >= 500
}

// invalidModelIDMarker is OpenRouter's up-front validation error for an unknown
// model id. Verified against the live API: it rejects the whole request even
// when only a *fallback* entry is misspelled and the primary model is fine, so
// a single typo in LLM_*_FALLBACK_MODELS would otherwise break every call.
const invalidModelIDMarker = " is not a valid model ID"

// invalidModelID returns the model id the API rejected, when that is why the
// request failed. Knowing *which* id is bad is what lets the retry keep the rest
// of the fallback chain instead of discarding all of it.
func (e *apiError) invalidModelID() (string, bool) {
	if e.status != http.StatusBadRequest {
		return "", false
	}
	i := strings.Index(e.body, invalidModelIDMarker)
	if i < 0 {
		return "", false
	}
	// The id is the last token before the marker, inside a JSON string.
	head := e.body[:i]
	return head[strings.LastIndexAny(head, "\"' >:,{")+1:], true
}

func (p *openAIChatProvider) Complete(system string, messages []Message) (string, error) {
	start := time.Now()
	msgs := p.buildMessages(system, messages)

	var lastErr error
	attempt := 0
	for {
		attempt++
		content, usedModel, usage, err := p.do(msgs, p.sendsFallbackList())
		if err == nil {
			p.logResult(usedModel, usage, time.Since(start))
			return content, nil
		}
		lastErr = err

		if errors.Is(err, errPermanent) {
			return "", err
		}

		var apiErr *apiError
		if errors.As(err, &apiErr) {
			// A rejected id means the request never reached a model at all, so
			// drop just that id and go again with the rest of the chain — a typo
			// in one entry must not cost the working fallbacks (and with them the
			// turn). Bounded: every pass removes one entry. It also does not
			// spend a retry, which is why attempt is rolled back.
			if bad, ok := apiErr.invalidModelID(); ok {
				if !p.pruneFallback(bad) {
					// Not one of ours: the primary model itself is invalid, and
					// no retry can fix a bad id in the config.
					return "", err
				}
				p.logger.Printf("WARN %s rejected fallback model %q, dropping it; chain is now [%s]",
					p.backend.name, bad, strings.Join(p.fallbackModels, ", "))
				attempt--
				continue
			}
			if !apiErr.transient() {
				return "", err
			}
		}

		if attempt >= p.retry.Attempts {
			break
		}
		delay := p.backoff(attempt, apiErr)
		p.logger.Printf("WARN %s attempt %d/%d failed, retrying in %v: %v",
			p.backend.name, attempt, p.retry.Attempts, delay, err)
		time.Sleep(delay)
	}
	return "", fmt.Errorf("%s chat failed after %d attempts: %w", p.backend.name, p.retry.Attempts, lastErr)
}

// sendsFallbackList reports whether this request should carry the "models"
// array: only a backend that understands it, and only while the chain is
// non-empty (pruning can empty it).
func (p *openAIChatProvider) sendsFallbackList() bool {
	return p.backend.supportsModelFallback && len(p.fallbackModels) > 0
}

// pruneFallback drops one rejected model id from the fallback chain and reports
// whether it was there. A false means the rejected id is the primary model, i.e.
// a broken LLM_MODEL that retrying cannot rescue. The chain stays pruned for the
// rest of the call (one AGI process serves one call, single-threaded), so a bad
// id in the config costs one round trip rather than one per turn.
func (p *openAIChatProvider) pruneFallback(model string) bool {
	for i, m := range p.fallbackModels {
		if m == model {
			// Full copy: the configured slice may be shared with another task.
			pruned := make([]string, 0, len(p.fallbackModels)-1)
			pruned = append(pruned, p.fallbackModels[:i]...)
			p.fallbackModels = append(pruned, p.fallbackModels[i+1:]...)
			return true
		}
	}
	return false
}

// buildMessages prepends the system prompt, when there is one.
func (p *openAIChatProvider) buildMessages(system string, messages []Message) []chatMessage {
	msgs := make([]chatMessage, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, chatMessage{Role: RoleSystem, Content: system})
	}
	for _, m := range messages {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Content})
	}
	return msgs
}

// backoff waits out a temporary failure: exponential from BaseDelay, never
// longer than MaxDelay. A Retry-After hint wins when it is longer than the
// computed delay but still under the cap — during a live call we would rather
// fail over to the spoken fallback line than hold the caller for the full
// minute a provider may ask for.
func (p *openAIChatProvider) backoff(attempt int, apiErr *apiError) time.Duration {
	if attempt > 10 {
		attempt = 10 // keep the shift below from overflowing
	}
	delay := p.retry.BaseDelay << (attempt - 1)
	if apiErr != nil && apiErr.retryAfter > delay {
		delay = apiErr.retryAfter
	}
	if delay > p.retry.MaxDelay {
		delay = p.retry.MaxDelay
	}
	return delay
}

// chatUsage is the accounting part of a reply.
type chatUsage struct {
	totalTokens int
	costRub     float64 // polza
	cost        float64 // openrouter, in USD credits
}

// do performs one request. It returns the reply, the model that actually
// answered (which differs from the requested one when a fallback kicked in)
// and the usage figures.
func (p *openAIChatProvider) do(msgs []chatMessage, useFallback bool) (string, string, chatUsage, error) {
	var usage chatUsage

	body := map[string]interface{}{
		"model":    p.model,
		"messages": msgs,
	}
	if useFallback {
		// OpenRouter walks this list when a model errors out, so an upstream
		// rate limit on the primary is served by the next entry instead of
		// failing the turn. The primary has to be the first element; sending
		// "model" alongside "models" is accepted (verified on the live API).
		body["models"] = append([]string{p.model}, p.fallbackModels...)
	}
	if p.maxTokens > 0 {
		body["max_tokens"] = p.maxTokens
	}
	if p.temperature != "" {
		if t, err := strconv.ParseFloat(p.temperature, 64); err == nil {
			body["temperature"] = t
		}
	}
	if p.reasoning != "" {
		// OpenRouter-style reasoning control; supported by reasoning models.
		body["reasoning"] = map[string]string{"effort": p.reasoning}
	}
	if p.backend.includeUsage {
		body["usage"] = map[string]bool{"include": true}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", "", usage, fmt.Errorf("marshal request: %v: %w", err, errPermanent)
	}

	req, err := http.NewRequest("POST", p.backend.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", usage, fmt.Errorf("create request: %v: %w", err, errPermanent)
	}
	req.Header.Set("Authorization", "Bearer "+p.backend.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: p.retry.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		// Connection reset, DNS hiccup, timeout — all worth another attempt.
		return "", "", usage, fmt.Errorf("%s chat request: %w", p.backend.name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", usage, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", "", usage, &apiError{
			backend:    p.backend.name,
			status:     resp.StatusCode,
			body:       string(respBody),
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	var result struct {
		Model   string `json:"model"` // the model that actually answered
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int     `json:"total_tokens"`
			CostRub     float64 `json:"cost_rub"` // polza
			Cost        float64 `json:"cost"`     // openrouter, in USD credits
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", usage, fmt.Errorf("parse response: %w (body: %s)", err, string(respBody))
	}
	usage = chatUsage{
		totalTokens: result.Usage.TotalTokens,
		costRub:     result.Usage.CostRub,
		cost:        result.Usage.Cost,
	}
	if len(result.Choices) == 0 {
		return "", result.Model, usage, fmt.Errorf("%s chat: no choices in response: %s", p.backend.name, string(respBody))
	}

	// Reasoning models put their thoughts in a separate field, so an empty
	// content here means the reply itself was lost (usually max_tokens spent on
	// reasoning) — report it instead of playing silence to the caller.
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	if content == "" {
		return "", result.Model, usage, fmt.Errorf("%s chat: empty content in response: %s", p.backend.name, string(respBody))
	}
	return content, result.Model, usage, nil
}

// logResult reports latency and cost, naming the model that actually answered.
// When the router moved on to a fallback, this log line is the only trace of it.
func (p *openAIChatProvider) logResult(usedModel string, u chatUsage, took time.Duration) {
	label := usedModel
	switch {
	case usedModel == "":
		label = p.model
	case usedModel != p.model:
		label = fmt.Sprintf("%s, fallback for %s", usedModel, p.model)
	}
	p.logger.Printf("%s LLM (%s) took %v, tokens=%d cost=%s",
		p.backend.name, label, took, u.totalTokens, formatCost(u.costRub, u.cost))
}

// parseRetryAfter reads the delay-seconds form of the header. The HTTP-date
// form is legal but not used by these APIs, and guessing at clock skew is worse
// than falling back to our own backoff.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// formatCost renders whichever cost field the backend reported.
func formatCost(rub, usd float64) string {
	switch {
	case rub > 0:
		return fmt.Sprintf("%.4f RUB", rub)
	case usd > 0:
		return fmt.Sprintf("%.6f USD", usd)
	default:
		return "n/a"
	}
}
