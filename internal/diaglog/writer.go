package diaglog

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var httpStatus = regexp.MustCompile(`\bHTTP ([1-5][0-9][0-9])\b`)
var sttFallbackAttempt = regexp.MustCompile(`(?s)\bWARN (Custom|Groq|Polza|OpenRouter) STT failed .*falling back to (Custom|Groq|Polza|OpenRouter)\s*$`)
var sttFallbackAnswered = regexp.MustCompile(`\b(Custom|Groq|Polza|OpenRouter) STT answered instead of (Custom|Groq|Polza|OpenRouter)\s*$`)
var safeProgress = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} (Summary: generated \d+ chars|Summary: webhook sent, status 2\d\d|Cleanup complete|Private session started)\n$`)

// Writer is the final log boundary for a private profile. Provider errors may
// contain response bodies, URLs or CLI stderr, so no original line passes.
type Writer struct {
	Output    io.Writer
	ProfileID string // already validated against config's ID allowlist
}

func (w Writer) Write(p []byte) (int, error) {
	line := string(p)
	if match := safeProgress.FindStringSubmatch(line); len(match) == 2 {
		_, err := fmt.Fprintf(w.Output, "%s profile=%s: %s\n", time.Now().Format("2006/01/02 15:04:05"), w.ProfileID, match[1])
		return len(p), err
	}
	if m := sttFallbackAnswered.FindStringSubmatch(line); len(m) == 3 {
		_, err := fmt.Fprintf(w.Output, "%s profile=%s: STT fallback primary=%s fallback=%s outcome=answered\n", time.Now().Format("2006/01/02 15:04:05"), w.ProfileID, m[2], m[1])
		return len(p), err
	}
	if m := sttFallbackAttempt.FindStringSubmatch(line); len(m) == 3 {
		status := ""
		if code := httpStatus.FindStringSubmatch(line); len(code) == 2 {
			status = " HTTP=" + code[1]
		}
		_, err := fmt.Fprintf(w.Output, "%s profile=%s: STT fallback primary=%s fallback=%s outcome=attempt kind=%s%s\n", time.Now().Format("2006/01/02 15:04:05"), w.ProfileID, m[1], m[2], failureKind(line, status), status)
		return len(p), err
	}
	severity := ""
	switch {
	case strings.Contains(line, "ERROR"):
		severity = "ERROR"
	case strings.Contains(line, "WARN") || strings.Contains(strings.ToLower(line), "failed"):
		severity = "WARN"
	default:
		return len(p), nil
	}
	provider := "unknown"
	for _, name := range []string{"OpenRouter", "Polza", "Groq", "Custom", "Claude", "ElevenLabs", "OpenAI", "Yandex", "Piper", "ffmpeg"} {
		if strings.Contains(strings.ToLower(line), strings.ToLower(name)) {
			provider = name
			break
		}
	}
	status := ""
	if m := httpStatus.FindStringSubmatch(line); len(m) == 2 {
		status = " HTTP=" + m[1]
	}
	_, err := fmt.Fprintf(w.Output, "%s profile=%s: %s provider=%s kind=%s%s\n", time.Now().Format("2006/01/02 15:04:05"), w.ProfileID, severity, provider, failureKind(line, status), status)
	return len(p), err
}

func failureKind(line, status string) string {
	lower := strings.ToLower(line)
	switch {
	case status != "":
		return "http"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out"):
		return "timeout"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "connection reset") || strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no such host"):
		return "network"
	case strings.Contains(lower, "cannot parse") || strings.Contains(lower, "parse response") || strings.Contains(lower, "without a \"text\" field") || strings.Contains(lower, "no choices"):
		return "schema"
	case strings.Contains(lower, "empty content") || strings.Contains(lower, "returned no text"):
		return "empty"
	case strings.Contains(lower, "claude error") || strings.Contains(lower, "claude stderr"):
		return "cli"
	default:
		return "other"
	}
}
