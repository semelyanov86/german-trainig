package main

import (
	"bytes"
	"io"
	"log"
	"strings"
	"testing"

	"german-trainer/internal/agi"
	"german-trainer/internal/diaglog"
)

func TestPrivateCallDiagnosticsDistinguishObservedDisconnects(t *testing.T) {
	for _, tc := range []struct {
		input, disconnect string
	}{
		{"HANGUP\n200 result=-1 endpos=16000\n", "asterisk_hangup"},
		{"511 Command Not Permitted on a dead channel\n", "dead_channel"},
		{"", "input_eof"},
	} {
		var out bytes.Buffer
		logger := log.New(diaglog.Writer{Output: &out, ProfileID: "psychologist"}, "", log.LstdFlags)
		call := newCallDiagnostics(logger, "1790507489.16")
		ch := agi.NewChannel(strings.NewReader(tc.input), io.Discard, logger)
		call.startTurn(11)
		ch.Cmd(`RECORD FILE /private/transcript wav "#" 420000 0 s=5`)
		call.finish(ch)
		// Later report/cleanup work must not overwrite the recorded exit.
		call.stage = "setup"
		call.finish(ch)
		got := out.String()
		for _, want := range []string{"Call started uniqueid=1790507489.16", "Call turn uniqueid=1790507489.16 turn=11", "turn=11 stage=record reason=channel_closed", "alive=false hangup_requested=false", "disconnect=" + tc.disconnect, "agi_command=record"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in %q", want, got)
			}
		}
		if strings.Count(got, "Call ended") != 1 || strings.Contains(got, "/private/") || strings.Contains(got, "endpos=") {
			t.Fatalf("duplicated exit or raw protocol payload leaked: %q", got)
		}
	}
}

func TestCallExitReasonsSurvivePrivateFilter(t *testing.T) {
	for _, reason := range []string{"unexpected_exit", "session_error", "prompt_error", "greeting_llm_error", "record_failed", "farewell", "dialog_errors", "turn_limit"} {
		t.Run(reason, func(t *testing.T) {
			var out bytes.Buffer
			logger := log.New(diaglog.Writer{Output: &out, ProfileID: "psychologist"}, "", log.LstdFlags)
			call := newCallDiagnostics(logger, "1790507489.16")
			ch := agi.NewChannel(strings.NewReader("200 result=-1\n"), io.Discard, logger)
			call.startTurn(25)
			call.reason = reason
			call.hangupRequested = true
			ch.Cmd("RECORD FILE /private/audio wav \"#\" 420000 0 s=5")
			call.finish(ch)
			got := out.String()
			if !strings.Contains(got, "reason="+reason+" alive=true hangup_requested=true disconnect=none agi_command=record agi_status=200 agi_result=-1 agi_result_known=true") {
				t.Fatalf("lost application exit reason or numeric AGI failure: %q", got)
			}
		})
	}
}

func TestCallDiagnosticsRejectUntrustedUniqueID(t *testing.T) {
	var out bytes.Buffer
	logger := log.New(&out, "", 0)
	newCallDiagnostics(logger, "1790507489.16\ntoken=private")
	if got := out.String(); got != "Call started uniqueid=unknown\n" {
		t.Fatalf("untrusted AGI variable leaked: %q", got)
	}
}
