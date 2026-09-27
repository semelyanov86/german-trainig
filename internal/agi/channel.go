package agi

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
)

type Channel struct {
	scanner    *bufio.Scanner
	writer     io.Writer
	logger     *log.Logger
	dead       bool
	diagnostic Diagnostics
	Vars       map[string]string
}

// Diagnostics contains only fixed command/failure categories and numeric AGI
// fields. It deliberately excludes command arguments and raw replies.
type Diagnostics struct {
	Command     string
	Status      int
	Result      int
	ResultKnown bool
	Disconnect  string
}

func NewChannel(r io.Reader, w io.Writer, logger *log.Logger) *Channel {
	return &Channel{
		scanner:    bufio.NewScanner(r),
		writer:     w,
		logger:     logger,
		Vars:       make(map[string]string),
		diagnostic: Diagnostics{Command: "none", Disconnect: "none"},
	}
}

func (c *Channel) ReadVars() {
	for c.scanner.Scan() {
		line := c.scanner.Text()
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			c.Vars[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
}

func (c *Channel) Cmd(cmd string) string {
	if c.dead {
		return ""
	}
	c.diagnostic.Command = commandCategory(cmd)
	c.diagnostic.Status = 0
	c.diagnostic.Result = 0
	c.diagnostic.ResultKnown = false
	c.logger.Printf("AGI> %s", cmd)
	_, writeErr := fmt.Fprintf(c.writer, "%s\n", cmd)

	for c.scanner.Scan() {
		resp := c.scanner.Text()

		// Asterisk pushes a bare "HANGUP" line into our stdin when the caller
		// hangs up. It is not a reply to our command, so skip it and keep
		// reading — but remember the channel is gone.
		if strings.TrimSpace(resp) == "HANGUP" {
			c.logger.Println("HANGUP received from Asterisk")
			c.dead = true
			c.setDisconnect("asterisk_hangup")
			continue
		}

		c.logger.Printf("AGI< %s", resp)
		if len(resp) >= 3 {
			if status, err := strconv.Atoi(resp[:3]); err == nil && status >= 100 && status <= 599 {
				c.diagnostic.Status = status
			}
		}
		c.diagnostic.Result, c.diagnostic.ResultKnown = Result(resp)

		// A reply always starts with a 3-digit status code; 511 is
		// "Command Not Permitted on a dead channel". Match the code as a
		// prefix — a substring search also hits digits inside a perfectly
		// normal reply ("200 result=25119 ...", "... endpos=511040"), which
		// used to kill a live call mid-conversation.
		if strings.HasPrefix(resp, "511") {
			c.dead = true
			c.setDisconnect("dead_channel")
			c.logger.Println("Channel is dead, stopping AGI commands")
		}
		return resp
	}
	c.dead = true
	switch {
	case writeErr != nil:
		c.setDisconnect("write_error")
	case c.scanner.Err() != nil:
		c.setDisconnect("input_error")
	default:
		c.setDisconnect("input_eof")
	}
	return ""
}

func (c *Channel) setDisconnect(reason string) {
	if c.diagnostic.Disconnect == "none" {
		c.diagnostic.Disconnect = reason
	}
}

func (c *Channel) Diagnostics() Diagnostics { return c.diagnostic }

func commandCategory(cmd string) string {
	switch {
	case cmd == "ANSWER":
		return "answer"
	case cmd == "HANGUP":
		return "hangup"
	case strings.HasPrefix(cmd, "RECORD FILE "):
		return "record"
	case strings.HasPrefix(cmd, "STREAM FILE "):
		return "playback"
	case strings.HasPrefix(cmd, "EXEC StartMusicOnHold "):
		return "start_moh"
	case cmd == "EXEC StopMusicOnHold":
		return "stop_moh"
	default:
		return "other"
	}
}

// Result extracts the numeric value of the "result=" field of an AGI reply
// ("200 result=-1 endpos=1234" -> -1). Returns 0 and false when the reply
// carries no parsable result. Callers must not substring-match on the raw
// reply: "result=" and "endpos=" digits collide with status codes and with
// each other.
func Result(resp string) (int, bool) {
	return numField(resp, "result=")
}

// Endpos extracts the "endpos=" field. After RECORD FILE it is the length of
// the recording in samples (8000 per second on a standard channel), measured
// after Asterisk cut the trailing silence it detected — so it approximates how
// much the caller actually said.
func Endpos(resp string) (int, bool) {
	return numField(resp, "endpos=")
}

// numField reads one "name=<int>" field out of an AGI reply.
func numField(resp, name string) (int, bool) {
	idx := strings.Index(resp, name)
	if idx < 0 {
		return 0, false
	}
	field := resp[idx+len(name):]
	if end := strings.IndexByte(field, ' '); end >= 0 {
		field = field[:end]
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (c *Channel) PlayAudio(wavPath string) string {
	base := strings.TrimSuffix(wavPath, ".wav")
	return c.Cmd(fmt.Sprintf("STREAM FILE %s \"\"", base))
}

func (c *Channel) IsAlive() bool {
	return !c.dead
}
