package agi

import (
	"bufio"
	"fmt"
	"io"
	"log"
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

	if c.scanner.Scan() {
		resp := c.scanner.Text()
		c.logger.Printf("AGI< %s", resp)
		if strings.Contains(resp, "511") || strings.Contains(resp, "dead channel") {
			c.dead = true
			c.logger.Println("Channel is dead, stopping AGI commands")
		}
		return resp
	}
	c.dead = true
	return ""
}

func (c *Channel) PlayAudio(wavPath string) {
	base := strings.TrimSuffix(wavPath, ".wav")
	c.Cmd(fmt.Sprintf("STREAM FILE %s \"\"", base))
}

func (c *Channel) IsAlive() bool {
	return !c.dead
}
