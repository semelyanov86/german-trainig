package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sttSpec describes one OpenAI-compatible audio/transcriptions endpoint. Every
// engine in this package is one of these: the hosted ones (groq, polza,
// openrouter) pin the fields at compile time, while the "custom" engine reads
// all of them from the config — which is what makes a move to another server,
// or another model on the same server, one line in /etc/german-trainer/.env.
type sttSpec struct {
	name     string        // label in log lines
	endpoint string        // full URL, never assembled from host + path
	apiKey   string        // empty: send no Authorization header (local service)
	model    string        // empty: omit the field (endpoint picks its own)
	language string        // empty: omit the field (endpoint's own default)
	timeout  time.Duration // budget for the whole engine, retry included
	// retryOnce allows a single fast second attempt after a transient failure.
	// Only the custom engine sets it: it is the one engine with no provider-side
	// model chain behind it, and an engine serving as *fallback* must fail fast
	// rather than add its own retries to a turn that is already late.
	retryOnce bool
}

// defaultTimeout outlives the self-hosted server's own 25s cut-off on purpose:
// when that endpoint gives up (queue full, upstream stuck) we want its error
// message in the log rather than our own anonymous timeout. It also guards the
// deadline below against a spec built with no timeout at all.
const defaultTimeout = 30 * time.Second

// retryDelay is the pause before that single retry. STT sits between the caller
// falling silent and the tutor answering, and the whole gap is filled with hold
// music, so the budget is tight: a turn takes 1.7-4.9s today.
const retryDelay = 300 * time.Millisecond

// httpTranscriber talks to one endpoint described by an sttSpec.
type httpTranscriber struct {
	spec   sttSpec
	client *http.Client
	logger *log.Logger
}

// newHTTPTranscriber builds a transcriber for one endpoint. The http.Client is
// created once and reused for every turn of the call: keep-alive then saves the
// TLS handshake (~73ms, measured against our own whisper server) from the
// second turn on.
func newHTTPTranscriber(spec sttSpec, logger *log.Logger) *httpTranscriber {
	if spec.timeout <= 0 {
		// A zero timeout would mean "wait forever" to the deadline below.
		spec.timeout = defaultTimeout
	}
	return &httpTranscriber{spec: spec, client: &http.Client{}, logger: logger}
}

func (t *httpTranscriber) Transcribe(wavPath string) (string, error) {
	start := time.Now()

	body, contentType, err := buildForm(wavPath, t.spec)
	if err != nil {
		return "", err
	}

	// One deadline for the engine as a whole, retry included: a fast first
	// failure followed by a hanging retry must not cost two full timeouts before
	// the fallback engine gets its turn, because the caller is listening to hold
	// music throughout.
	ctx, cancel := context.WithTimeout(context.Background(), t.spec.timeout)
	defer cancel()

	text, err := t.attempt(ctx, body, contentType)
	if err != nil && t.spec.retryOnce && retriable(err) {
		t.logger.Printf("WARN %s STT attempt failed (%v), retrying in %v", t.spec.name, err, retryDelay)
		time.Sleep(retryDelay)
		text, err = t.attempt(ctx, body, contentType)
	}
	if err != nil {
		return "", err
	}

	// An empty transcript is an answer, not a failure: the self-hosted server
	// runs silero VAD ahead of the decoder and returns "" for a dead line, which
	// is the main reason we moved to it. main.go turns that into the spoken "say
	// something" nudge, and no fallback engine is asked to invent words out of
	// the same silence.
	if text == "" {
		t.logger.Printf("%s STT: no speech, %v", t.spec.name, time.Since(start))
		return "", nil
	}

	t.logger.Printf("%s STT took %v", t.spec.name, time.Since(start))
	return text, nil
}

// attempt performs one round trip and returns the normalized transcript.
func (t *httpTranscriber) attempt(ctx context.Context, body []byte, contentType string) (string, error) {
	engine := strings.ToLower(t.spec.name)

	req, err := http.NewRequestWithContext(ctx, "POST", t.spec.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	// An empty key means the endpoint wants no authorization at all (a local
	// service); sending "Bearer " with nothing after it would fail instead.
	if t.spec.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.spec.apiKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", &transportError{engine: engine, err: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &transportError{engine: engine, err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return "", &sttError{engine: engine, status: resp.StatusCode, message: errorMessage(respBody)}
	}

	// A *pointer*, so that a present-but-empty "text" (the server reporting "no
	// speech") stays distinguishable from a reply that carries no text at all.
	// Without that distinction a 200 with a foreign schema — a wrong URL, a
	// proxy's error page, {"text":null} — would read as silence and never reach
	// the fallback: every turn would become the spoken "say something" nudge,
	// with nothing in the log to say why.
	var result struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		// The body goes into the message: a wrong URL answering 200 with an HTML
		// page is otherwise reported only as "invalid character '<'".
		return "", fmt.Errorf("%s stt: cannot parse reply (%v): %s", engine, err, truncate(strings.TrimSpace(string(respBody))))
	}
	if result.Text == nil {
		return "", fmt.Errorf("%s stt: HTTP 200 without a \"text\" field: %s", engine, errorMessage(respBody))
	}
	return normalize(*result.Text), nil
}

// buildForm renders the multipart body once, so the retry can send the same
// bytes: a sent request has already consumed its body, and re-reading the file
// would only be slower.
func buildForm(wavPath string, spec sttSpec) ([]byte, string, error) {
	file, err := os.Open(wavPath)
	if err != nil {
		return nil, "", fmt.Errorf("open audio: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filepath.Base(wavPath))
	if err != nil {
		return nil, "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", fmt.Errorf("copy file: %w", err)
	}

	// Both optional fields are omitted when unset. An endpoint that chooses its
	// own model ignores "model" anyway, while one that requires it cannot work
	// without it; and a wrong "language" is worse than none — the self-hosted
	// server answers 400 to language=auto.
	if spec.model != "" {
		writer.WriteField("model", spec.model)
	}
	if spec.language != "" {
		writer.WriteField("language", spec.language)
	}
	writer.WriteField("response_format", "json")

	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close form: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

// transportError is a failure of the round trip itself — connection refused or
// reset, DNS, TLS, or our own client-side timeout — as opposed to a reply the
// endpoint actually sent.
type transportError struct {
	engine string
	err    error
}

func (e *transportError) Error() string {
	return fmt.Sprintf("%s stt request: %v", e.engine, e.err)
}

func (e *transportError) Unwrap() error { return e.err }

// timedOut reports whether we gave up waiting, rather than being turned away.
func (e *transportError) timedOut() bool {
	var netErr net.Error
	return errors.As(e.err, &netErr) && netErr.Timeout()
}

// sttError is a non-2xx reply. The status is kept so the retry can tell a
// temporary failure (429/5xx, including the 503 "at capacity" the self-hosted
// server answers when its single-slot queue is full) from a permanent one (401
// with a stale token, 400 with a field the endpoint rejects).
type sttError struct {
	engine  string
	status  int
	message string
}

func (e *sttError) Error() string {
	return fmt.Sprintf("%s stt HTTP %d: %s", e.engine, e.status, e.message)
}

func (e *sttError) transient() bool {
	return e.status == http.StatusRequestTimeout ||
		e.status == http.StatusTooManyRequests ||
		e.status >= 500
}

// retriable reports whether a second attempt could plausibly succeed *soon*. A
// client-side timeout is deliberately excluded: it has already spent the whole
// per-request budget, so the caller is served better by the fallback engine than
// by waiting out the same timeout twice. Our own mistakes (unreadable file,
// unparsable reply) are not retried either.
func retriable(err error) bool {
	var transport *transportError
	if errors.As(err, &transport) {
		return !transport.timedOut()
	}
	var apiErr *sttError
	if errors.As(err, &apiErr) {
		return apiErr.transient()
	}
	return false
}

// errorMessage pulls the human-readable part out of an error reply. The
// self-hosted server answers {"detail":"…"} (FastAPI), the hosted engines answer
// {"error":{"message":"…"}}; anything else is passed through raw, because a
// prettified unknown format helps nobody while a call is failing.
func errorMessage(body []byte) string {
	var parsed struct {
		Detail json.RawMessage `json:"detail"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if len(parsed.Detail) > 0 {
			var detail string
			if json.Unmarshal(parsed.Detail, &detail) == nil && detail != "" {
				return detail
			}
			// A validation error puts a list here; its raw JSON is still the
			// most useful thing we can show.
			return string(parsed.Detail)
		}
		if parsed.Error.Message != "" {
			return parsed.Error.Message
		}
	}
	return truncate(strings.TrimSpace(string(body)))
}

// truncate bounds a message pasted into a log line. An endpoint answering with a
// whole HTML page (a wrong URL, a captive proxy) would otherwise print it once
// per turn.
func truncate(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "… (truncated)"
}

// normalize flattens the transcript to a single line. whisper.cpp marks segment
// boundaries with an internal "\n ", which would otherwise reach the history
// file and the LLM prompt verbatim.
func normalize(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
