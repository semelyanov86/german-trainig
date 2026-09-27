package main

import (
	"log"
	"regexp"
	"time"

	"german-trainer/internal/agi"
)

var callUniqueID = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// Only fixed categories and protocol numbers enter these events. Neither caller
// identity nor conversation/provider payloads are needed to diagnose an exit.
type callDiagnostics struct {
	logger          *log.Logger
	uniqueID        string
	started         time.Time
	turn            int
	stage           string
	reason          string
	hangupRequested bool
	finished        bool
}

func newCallDiagnostics(logger *log.Logger, uniqueID string) *callDiagnostics {
	if !callUniqueID.MatchString(uniqueID) {
		uniqueID = "unknown"
	}
	d := &callDiagnostics{logger: logger, uniqueID: uniqueID, started: time.Now(), stage: "setup", reason: "unexpected_exit"}
	logger.Printf("Call started uniqueid=%s", uniqueID)
	return d
}

func (d *callDiagnostics) startTurn(turn int) {
	d.turn = turn
	d.stage = "record"
	d.logger.Printf("Call turn uniqueid=%s turn=%d", d.uniqueID, turn)
}

func (d *callDiagnostics) finish(ch *agi.Channel) {
	if d.finished {
		return
	}
	d.finished = true
	state := ch.Diagnostics()
	if d.reason == "unexpected_exit" && !ch.IsAlive() {
		d.reason = "channel_closed"
	}
	d.logger.Printf("Call ended uniqueid=%s turn=%d stage=%s reason=%s alive=%t hangup_requested=%t disconnect=%s agi_command=%s agi_status=%d agi_result=%d agi_result_known=%t duration_seconds=%d",
		d.uniqueID, d.turn, d.stage, d.reason, ch.IsAlive(), d.hangupRequested,
		state.Disconnect, state.Command, state.Status, state.Result, state.ResultKnown,
		int(time.Since(d.started)/time.Second))
}
