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
	scanner *bufio.Scanner
	writer  io.Writer
	logger  *log.Logger
	dead    bool
	Vars    map[string]string
}

func NewChannel(r io.Reader, w io.Writer, logger *log.Logger) *Channel {
	return &Channel{
		scanner: bufio.NewScanner(r),
		writer:  w,
		logger:  logger,
		Vars:    make(map[string]string),
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
	c.logger.Printf("AGI> %s", cmd)
	fmt.Fprintf(c.writer, "%s\n", cmd)

	for c.scanner.Scan() {
		resp := c.scanner.Text()

		// Asterisk pushes a bare "HANGUP" line into our stdin when the caller
		// hangs up. It is not a reply to our command, so skip it and keep
		// reading — but remember the channel is gone.
		if strings.TrimSpace(resp) == "HANGUP" {
			c.logger.Println("HANGUP received from Asterisk")
			c.dead = true
			continue
		}

		c.logger.Printf("AGI< %s", resp)

		// A reply always starts with a 3-digit status code; 511 is
		// "Command Not Permitted on a dead channel". Match the code as a
		// prefix — a substring search also hits digits inside a perfectly
		// normal reply ("200 result=25119 ...", "... endpos=511040"), which
		// used to kill a live call mid-conversation.
		if strings.HasPrefix(resp, "511") {
			c.dead = true
			c.logger.Println("Channel is dead, stopping AGI commands")
		}
		return resp
	}
	c.dead = true
	return ""
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

func (c *Channel) PlayAudio(wavPath string) {
	base := strings.TrimSuffix(wavPath, ".wav")
	c.Cmd(fmt.Sprintf("STREAM FILE %s \"\"", base))
}

func (c *Channel) IsAlive() bool {
	return !c.dead
}
