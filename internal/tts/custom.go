package tts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The custom engine is our own Higgs TTS server (contract:
// docs/integrations/tts-higgs.md in its repository). Three rules shape it:
//
//   - A call holds a place on the server from its first reply to its hangup
//     (PUT/DELETE /api/v1/sessions/{id}). A call that gets no place speaks
//     entirely through the fallback; a call that has one is never refused
//     for load.
//   - Audio streams as NDJSON: a start line, base64 PCM lines, an end line.
//     A reply counts as delivered only if the end line says complete with
//     reason "eos" and its sample count matches what arrived; anything else
//     broke off and is requested again, once.
//   - HTTP 200 arrives together with the first audio, so every failure before
//     the caller hears anything is a plain HTTP error the fallback can absorb.
//
// The server's own Go decoder lives in an internal package of its repository
// and cannot be imported from this module, so the same rules are implemented
// here.
const (
	// Asterisk starts playing once this much audio is buffered. The server
	// generates about twice as fast as real time, so while one piece plays
	// the next one grows to roughly twice its length.
	customPrebuffer = phoneRate // 1s
	// A later piece waits for at least this much audio unless the reply is
	// over: a server stall is better heard as one pause than as many seams.
	customMinPiece = phoneRate / 2
	// The server sends its first audio within 3s or answers engine_timeout,
	// and replies of a session wait at most 5s in its queue.
	customHeaderTimeout = 15 * time.Second
	// The server ends a reply with engine_stall after 3s without audio.
	customIdleTimeout    = 10 * time.Second
	customTotalTimeout   = 2 * time.Minute
	customSessionTimeout = 3 * time.Second
	// The server forgets a place after 5 minutes without a request; one caller
	// utterance on the psychologist line can take seven.
	customKeepaliveEvery = time.Minute
	customKeepaliveIdle  = 2 * time.Minute
	customMaxLine        = 4 << 20
)

// customError is a failure of the custom TTS service. phrase marks a problem
// of one reply (a rejected request, a reply the model broke off twice) as
// opposed to a failure of the service, which ends its use for the call.
type customError struct {
	status int    // HTTP status, 0 when the failure came without one
	code   string // service error code or end reason, identifier characters only
	phrase bool
	broke  bool // the reply was streaming when it failed: request it again
	msg    string
}

func (e *customError) Error() string {
	var parts []string
	if e.status != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.status))
	}
	if e.code != "" {
		parts = append(parts, e.code)
	}
	if e.msg != "" {
		parts = append(parts, e.msg)
	}
	return "custom tts " + strings.Join(parts, " ")
}

// phraseReasons are end reasons that blame the text, not the service.
var phraseReasons = map[string]bool{"too_long": true, "too_short": true, "silence": true}

var identifier = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// errorCode reads the service's error code without echoing anything else of
// the body: the message may quote the reply, and a non-JSON body (an nginx
// page) has no business in the log.
func errorCode(body []byte) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || !identifier.MatchString(e.Error.Code) {
		return "non_json_error"
	}
	return e.Error.Code
}

type customClient struct {
	cfg     Config
	logger  *log.Logger
	http    *http.Client
	session *customSession // nil when the call holds no place
	replies int
}

func newCustomClient(cfg Config, logger *log.Logger) *customClient {
	cfg.CustomURL = strings.TrimRight(strings.TrimSpace(cfg.CustomURL), "/")
	return &customClient{cfg: cfg, logger: logger, http: &http.Client{}}
}

var callIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// sessionID names the call's place after the Asterisk uniqueid, which the
// server's log then shares with the CDR.
func sessionID(cfg Config) string {
	if id := "gt-" + cfg.CallID; cfg.CallID != "" && callIDPattern.MatchString(id) {
		return id
	}
	return "gt-" + cfg.SessionID
}

func (c *customClient) request(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	if c.cfg.CustomURL == "" {
		return nil, &customError{msg: "CUSTOM_TTS_URL is not set"}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.CustomURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, &customError{msg: "invalid CUSTOM_TTS_URL"}
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.CustomToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// sessionCall sends one PUT or DELETE for the call's place.
func (c *customClient) sessionCall(method, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), customSessionTimeout)
	defer cancel()
	req, err := c.request(ctx, method, "/api/v1/sessions/"+id, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &customError{msg: transportError(err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return &customError{status: resp.StatusCode, code: errorCode(body)}
	}
	return nil
}

func transportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	// Strip the URL net/http prefixes to the error.
	var urlErr interface{ Unwrap() error }
	if errors.As(err, &urlErr) && urlErr.Unwrap() != nil {
		err = urlErr.Unwrap()
	}
	return err.Error()
}

// customSession is the call's place on the server.
type customSession struct {
	client  *customClient
	id      string
	mu      sync.Mutex
	lastUse time.Time
	done    chan struct{}
	wg      sync.WaitGroup
}

func (c *customClient) openSession(id string) (*customSession, error) {
	start := time.Now()
	if err := c.sessionCall(http.MethodPut, id); err != nil {
		return nil, err
	}
	c.logger.Printf("Custom TTS session %s opened in %v", id, time.Since(start).Round(time.Millisecond))
	s := &customSession{client: c, id: id, lastUse: time.Now(), done: make(chan struct{})}
	s.wg.Add(1)
	go s.keepalive()
	return s, nil
}

func (s *customSession) touch() {
	s.mu.Lock()
	s.lastUse = time.Now()
	s.mu.Unlock()
}

// keepalive renews the place while the caller talks for a long time. Every
// reply renews it as well, so on an ordinary call this never sends anything.
func (s *customSession) keepalive() {
	defer s.wg.Done()
	ticker := time.NewTicker(customKeepaliveEvery)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			idle := time.Since(s.lastUse)
			s.mu.Unlock()
			if idle < customKeepaliveIdle {
				continue
			}
			if err := s.client.sessionCall(http.MethodPut, s.id); err != nil {
				s.client.logger.Printf("WARN Custom TTS session renewal failed: %v", err)
				continue
			}
			s.touch()
		}
	}
}

func (s *customSession) close() {
	close(s.done)
	s.wg.Wait()
	if err := s.client.sessionCall(http.MethodDelete, s.id); err != nil {
		s.client.logger.Printf("WARN Custom TTS session %s not released: %v", s.id, err)
		return
	}
	s.client.logger.Printf("Custom TTS session %s released", s.id)
}

// customSpeaker filters a reply and streams it, requesting it once more if it
// broke off before the caller heard any of it.
type customSpeaker struct {
	client        *customClient
	dialect       Dialect
	logger        *log.Logger
	logUtterances bool
}

func (s *customSpeaker) speak(text string) (Audio, error) {
	clean, err := cleanReply(s.dialect, text, s.logger, s.logUtterances)
	if err != nil {
		return &fileAudio{}, err
	}
	for attempt := 1; ; attempt++ {
		audio, err := s.client.stream(clean)
		if err == nil {
			return audio, nil
		}
		var failure *customError
		// Only a reply that started streaming is requested again: an HTTP
		// or connection error before the first audio is the service's
		// answer, and the caller is waiting on hold music for the fallback.
		if attempt == 2 || !errors.As(err, &failure) || !failure.broke {
			return &fileAudio{}, err
		}
		s.logger.Printf("WARN Custom TTS reply broke off before playback (%v), requesting it again", err)
	}
}

// stream sends one reply and returns once enough of it has arrived to start
// playback, or the whole reply if it is shorter than that.
func (c *customClient) stream(text string) (Audio, error) {
	start := time.Now()
	c.replies++
	payload, _ := json.Marshal(map[string]string{
		"text": text, "voice": c.cfg.CustomVoice, "language": c.cfg.CustomLanguage,
	})
	ctx, cancel := context.WithTimeout(context.Background(), customTotalTimeout)
	req, err := c.request(ctx, http.MethodPost, "/api/v1/speech", payload)
	if err != nil {
		cancel()
		return nil, err
	}
	if c.session != nil {
		c.session.touch()
		req.Header.Set("X-TTS-Session", c.session.id)
		req.Header.Set("X-Request-Id", fmt.Sprintf("%s-%d", c.session.id, c.replies))
	}
	// One timer covers both the wait for the first audio and every gap in
	// the stream after it; a stalled stream is cancelled, not waited out.
	stalled := false
	var stallMu sync.Mutex
	watchdog := time.AfterFunc(customHeaderTimeout, func() {
		stallMu.Lock()
		stalled = true
		stallMu.Unlock()
		cancel()
	})
	resp, err := c.http.Do(req)
	if err != nil {
		watchdog.Stop()
		cancel()
		return nil, &customError{msg: transportError(err)}
	}
	if resp.StatusCode != http.StatusOK {
		watchdog.Stop()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		cancel()
		status := resp.StatusCode
		return nil, &customError{status: status, code: errorCode(body), phrase: status == 400 || status == 413}
	}
	a := &liveAudio{base: c.cfg.audioBase(), cancel: cancel}
	a.cond = sync.NewCond(&a.mu)
	go func() {
		err := a.produce(resp.Body, watchdog)
		resp.Body.Close()
		cancel()
		var failure *customError
		if errors.As(err, &failure) {
			failure.broke = true
			// The watchdog cancelled the read: name the cause, not its symptom.
			stallMu.Lock()
			if stalled && failure.code == "read_error" {
				failure.code, failure.msg = "stalled", "no audio for too long"
			}
			stallMu.Unlock()
		}
		a.finish(err)
		if err == nil {
			c.logger.Printf("Custom TTS took %v for %.1fs of audio", time.Since(start).Round(time.Millisecond), float64(a.total)/phoneRate)
		}
	}()
	if err := a.waitReady(); err != nil {
		a.Stop()
		return nil, err
	}
	c.logger.Printf("Custom TTS ready to play in %v", time.Since(start).Round(time.Millisecond))
	return a, nil
}

// liveAudio is a reply that is still arriving. A single growing file cannot
// be played: Asterisk's STREAM FILE reports the end position as the file size
// at the start of the command, so resuming after a stall would replay audio.
// The reply is therefore cut into pieces, each one finished before it plays,
// and every cut sits in the quietest 20ms of the audio waiting to be cut, so
// the few milliseconds between two pieces fall into a pause between words.
type liveAudio struct {
	mu      sync.Mutex
	cond    *sync.Cond
	pcm     []int16 // 8 kHz samples not handed out yet
	total   int     // 8 kHz samples produced so far
	done    bool
	err     error
	stopped bool
	base    string
	files   []string
	cancel  context.CancelFunc
}

type customLine struct {
	Type       string `json:"type"`
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	PCM        []byte `json:"pcm"` // base64, decoded by encoding/json
	Complete   bool   `json:"complete"`
	Reason     string `json:"reason"`
	Samples    int    `json:"samples"`
}

// produce reads the NDJSON stream until its end line and checks that the reply
// arrived whole: an end line, complete with reason "eos", and exactly as many
// samples as the end line counts.
func (a *liveAudio) produce(body io.Reader, watchdog *time.Timer) error {
	defer watchdog.Stop()
	reader := bufio.NewReaderSize(body, 64<<10)
	var resampler *decimator
	received := 0
	var end *customLine
	for {
		line, err := readLine(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return &customError{code: "read_error", msg: transportError(err)}
		}
		watchdog.Reset(customIdleTimeout)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if end != nil {
			return &customError{msg: "data after the end line"}
		}
		var m customLine
		if err := json.Unmarshal(line, &m); err != nil {
			return &customError{msg: "invalid stream line"}
		}
		switch m.Type {
		case "start":
			if m.Encoding != "pcm_s16le" || m.Channels != 1 {
				return &customError{msg: fmt.Sprintf("unsupported audio %s/%d channels", m.Encoding, m.Channels)}
			}
			if resampler, err = newDecimator(m.SampleRate); err != nil {
				return &customError{msg: err.Error()}
			}
		case "audio":
			if resampler == nil {
				return &customError{msg: "audio before the start line"}
			}
			if len(m.PCM)%2 != 0 {
				return &customError{msg: "odd PCM length"}
			}
			samples := make([]int16, len(m.PCM)/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(m.PCM[2*i:]))
			}
			received += len(samples)
			a.add(resampler.push(samples))
		case "end":
			last := m
			end = &last
		}
	}
	if end == nil {
		return &customError{msg: "stream ended without an end line"}
	}
	if !end.Complete || end.Reason != "eos" {
		reason := end.Reason
		if !identifier.MatchString(reason) {
			reason = "unknown"
		}
		return &customError{code: reason, phrase: phraseReasons[reason], msg: "reply incomplete"}
	}
	if end.Samples != received {
		return &customError{msg: fmt.Sprintf("received %d of %d samples", received, end.Samples)}
	}
	if resampler != nil {
		a.add(resampler.flush())
	}
	if received == 0 {
		return &customError{msg: "reply without audio"}
	}
	return nil
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > customMaxLine {
			return nil, errors.New("stream line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(line) > 0 {
			return line, nil
		}
		return line, err
	}
}

func (a *liveAudio) add(samples []int16) {
	if len(samples) == 0 {
		return
	}
	a.mu.Lock()
	a.pcm = append(a.pcm, samples...)
	a.total += len(samples)
	a.mu.Unlock()
	a.cond.Broadcast()
}

func (a *liveAudio) finish(err error) {
	a.mu.Lock()
	a.done, a.err = true, err
	a.mu.Unlock()
	a.cond.Broadcast()
}

// waitReady blocks until playback can start. Audio that arrived before a
// failure is discarded with it: nobody has heard it, and the reply is
// requested again.
func (a *liveAudio) waitReady() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for !a.done && len(a.pcm) < customPrebuffer {
		a.cond.Wait()
	}
	if a.done && a.err != nil {
		return a.err
	}
	return nil
}

func (a *liveAudio) Next() (string, error) {
	a.mu.Lock()
	for !a.done && !a.stopped && len(a.pcm) < customMinPiece {
		a.cond.Wait()
	}
	if a.stopped || len(a.pcm) == 0 {
		err := a.err
		a.mu.Unlock()
		return "", err
	}
	n := len(a.pcm)
	if !a.done {
		n = quietCut(a.pcm)
	}
	piece := a.pcm[:n]
	a.pcm = append([]int16(nil), a.pcm[n:]...)
	path := fmt.Sprintf("%s_%02d.sln", a.base, len(a.files))
	a.files = append(a.files, path)
	a.mu.Unlock()

	buf := make([]byte, 2*len(piece))
	for i, s := range piece {
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(s))
	}
	if err := os.WriteFile(path, buf, 0600); err != nil {
		a.Stop()
		return "", &customError{msg: "write audio: " + err.Error()}
	}
	return path, nil
}

func (a *liveAudio) Files() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.files...)
}

func (a *liveAudio) Stop() {
	a.mu.Lock()
	a.stopped = true
	a.mu.Unlock()
	a.cancel()
	a.cond.Broadcast()
}

// quietCut picks where the next piece ends: in the middle of the quietest 20ms
// frame of the second half of the buffered audio. The second half keeps the
// pieces long, and so the seams few; a later frame wins a tie for the same
// reason.
func quietCut(pcm []int16) int {
	const frame = phoneRate / 50
	frames := len(pcm) / frame
	if frames < 2 {
		return len(pcm)
	}
	best, bestEnergy := frames-1, -1.0
	for f := frames / 2; f < frames; f++ {
		energy := 0.0
		for _, s := range pcm[f*frame : (f+1)*frame] {
			energy += float64(s) * float64(s)
		}
		if bestEnergy < 0 || energy <= bestEnergy {
			best, bestEnergy = f, energy
		}
	}
	return best*frame + frame/2
}
