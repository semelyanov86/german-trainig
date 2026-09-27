package agi

import (
	"bytes"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
)

type brokenIO struct{}

func (brokenIO) Read([]byte) (int, error)  { return 0, errors.New("private read error") }
func (brokenIO) Write([]byte) (int, error) { return 0, errors.New("private write error") }

func TestChannelDiagnosticsPreserveProtocolBehavior(t *testing.T) {
	for _, tc := range []struct {
		name, input, disconnect string
		alive                   bool
		status, result          int
		resultKnown             bool
	}{
		{"normal numeric collision", "200 result=25119 endpos=511040\n", "none", true, 200, 25119, true},
		{"record failure", "200 result=-1 endpos=16000\n", "none", true, 200, -1, true},
		{"Asterisk hangup with reply", "HANGUP\n200 result=-1 endpos=16000\n", "asterisk_hangup", false, 200, -1, true},
		{"Asterisk hangup then EOF", "HANGUP\n", "asterisk_hangup", false, 0, 0, false},
		{"dead channel", "511 Command Not Permitted on a dead channel\n", "dead_channel", false, 511, 0, false},
		{"EOF", "", "input_eof", false, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent bytes.Buffer
			ch := NewChannel(strings.NewReader(tc.input), &sent, log.New(io.Discard, "", 0))
			ch.Cmd(`RECORD FILE /private/session/audio wav "#" 150000 0 s=5`)
			got := ch.Diagnostics()
			if ch.IsAlive() != tc.alive || got.Command != "record" || got.Disconnect != tc.disconnect || got.Status != tc.status || got.Result != tc.result || got.ResultKnown != tc.resultKnown {
				t.Fatalf("alive=%v diagnostics=%+v", ch.IsAlive(), got)
			}
			if !tc.alive {
				before := sent.String()
				ch.Cmd("HANGUP")
				if sent.String() != before || ch.Diagnostics() != got {
					t.Fatal("dead channel sent another command or lost its terminal diagnostics")
				}
			}
		})
	}
}

func TestChannelDiagnosticsClassifyIOFailuresWithoutPayloads(t *testing.T) {
	for _, tc := range []struct {
		reader io.Reader
		writer io.Writer
		want   string
	}{
		{brokenIO{}, io.Discard, "input_error"},
		{strings.NewReader(""), brokenIO{}, "write_error"},
	} {
		ch := NewChannel(tc.reader, tc.writer, log.New(io.Discard, "", 0))
		ch.Cmd("STREAM FILE /private/audio \"\"")
		if got := ch.Diagnostics(); got.Disconnect != tc.want || got.Command != "playback" || ch.IsAlive() {
			t.Fatalf("incorrect I/O diagnostic: %+v, alive=%v", got, ch.IsAlive())
		}
	}
}

func TestChannelDiagnosticsResetResultForNextCommand(t *testing.T) {
	ch := NewChannel(strings.NewReader("200 result=25119\n510 Invalid or unknown command\n"), io.Discard, log.New(io.Discard, "", 0))
	ch.Cmd("STREAM FILE /private/audio \"\"")
	ch.Cmd("SECRET argument")
	if got := ch.Diagnostics(); got.Command != "other" || got.Status != 510 || got.Result != 0 || got.ResultKnown || got.Disconnect != "none" {
		t.Fatalf("stale playback result: %+v", got)
	}
}
