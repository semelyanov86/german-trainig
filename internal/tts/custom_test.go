package tts

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// tone returns 24 kHz samples: speech-like sine bursts separated by silence.
func tone(seconds float64, freq float64) []int16 {
	n := int(seconds * 24000)
	out := make([]int16, n)
	for i := range out {
		if (i/24000)%2 == 1 && i%24000 > 18000 { // a quarter second of pause every other second
			continue
		}
		out[i] = int16(8000 * math.Sin(2*math.Pi*freq*float64(i)/24000))
	}
	return out
}

func audioLine(samples []int16) string {
	buf := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(s))
	}
	return fmt.Sprintf(`{"type":"audio","pcm":"%s"}`+"\n", base64.StdEncoding.EncodeToString(buf))
}

const startLine = `{"type":"start","protocol":1,"encoding":"pcm_s16le","sample_rate":24000,"channels":1}` + "\n"

func endLine(complete bool, reason string, samples int) string {
	return fmt.Sprintf(`{"type":"end","complete":%t,"reason":%q,"samples":%d}`+"\n", complete, reason, samples)
}

// higgsServer fakes the service. reply writes the speech response for the
// n-th speech request (1-based).
type higgsServer struct {
	t        *testing.T
	mu       sync.Mutex
	events   []string
	speeches int
	sessions map[string]bool
	putCode  int
	reply    func(n int, w http.ResponseWriter)
}

func (h *higgsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(401)
		return
	}
	h.mu.Lock()
	switch {
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/sessions/"):
		h.events = append(h.events, "PUT "+strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"))
		h.mu.Unlock()
		if h.putCode != 0 {
			w.WriteHeader(h.putCode)
			io.WriteString(w, `{"error":{"code":"no_capacity","message":"full","retryable":true}}`)
			return
		}
		w.WriteHeader(201)
		return
	case r.Method == http.MethodDelete:
		h.events = append(h.events, "DELETE "+strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"))
		h.mu.Unlock()
		w.WriteHeader(204)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/speech":
		h.speeches++
		n := h.speeches
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		h.events = append(h.events, fmt.Sprintf("POST session=%s id=%s voice=%s lang=%s text=%s",
			r.Header.Get("X-TTS-Session"), r.Header.Get("X-Request-Id"), body["voice"], body["language"], body["text"]))
		h.mu.Unlock()
		h.reply(n, w)
		return
	}
	h.mu.Unlock()
	h.t.Errorf("unexpected request %s %s", r.Method, r.URL)
}

func (h *higgsServer) log() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

func wholeReply(samples []int16) func(int, http.ResponseWriter) {
	return func(_ int, w http.ResponseWriter) {
		io.WriteString(w, startLine)
		for i := 0; i < len(samples); i += 12000 {
			j := i + 12000
			if j > len(samples) {
				j = len(samples)
			}
			io.WriteString(w, audioLine(samples[i:j]))
		}
		io.WriteString(w, endLine(true, "eos", len(samples)))
	}
}

var higgsTagPattern = regexp.MustCompile(`<\|[a-z_]+:[a-z_]+\|>`)

func customConfig(t *testing.T, url string) Config {
	return Config{SessionID: "1", TempDir: t.TempDir(), CallID: "1731940000.42", CustomURL: url + "/",
		CustomToken: "test-token", CustomVoice: "female", CustomLanguage: "de-DE", StyleTags: "auto"}
}

func readAll(t *testing.T, a Audio) (int, int) {
	t.Helper()
	pieces, samples := 0, 0
	for {
		path, err := a.Next()
		if err != nil {
			t.Fatalf("reply broke off: %v", err)
		}
		if path == "" {
			return pieces, samples
		}
		if !strings.HasSuffix(path, ".sln") {
			t.Fatalf("piece %q is not raw 8 kHz audio", path)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("piece %q: %v %v", path, info, err)
		}
		pieces++
		samples += int(info.Size()) / 2
	}
}

func TestCustomCallHoldsOnePlaceAndStreamsWholeReplies(t *testing.T) {
	audio := tone(4, 440)
	h := &higgsServer{t: t, reply: wholeReply(audio)}
	srv := httptest.NewServer(h)
	defer srv.Close()

	v, dialect := Open("custom", customConfig(t, srv.URL), log.New(io.Discard, "", 0))
	if dialect.Name != "higgs" || !strings.Contains(dialect.Guide, "<|emotion:amusement|>") {
		t.Fatalf("dialect %q", dialect.Name)
	}
	a, err := v.Speak("<|emotion:amusement|>Ach komm. [laugh] <|prosody:long_pause|>Wirklich?")
	if err != nil {
		t.Fatal(err)
	}
	pieces, samples := readAll(t, a)
	if samples != len(audio)/3 || pieces < 1 {
		t.Fatalf("got %d samples in %d pieces, want %d", samples, pieces, len(audio)/3)
	}
	if len(a.Files()) != pieces {
		t.Fatalf("files %v", a.Files())
	}
	v.Close()
	want := []string{
		"PUT gt-1731940000.42",
		"POST session=gt-1731940000.42 id=gt-1731940000.42-1 voice=female lang=de-DE text=<|emotion:amusement|>Ach komm. Wirklich?",
		"DELETE gt-1731940000.42",
	}
	if got := h.log(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCustomPlaybackStartsBeforeTheReplyIsGenerated(t *testing.T) {
	audio := tone(3, 440)
	release := make(chan struct{})
	h := &higgsServer{t: t, reply: func(_ int, w http.ResponseWriter) {
		io.WriteString(w, startLine)
		io.WriteString(w, audioLine(audio[:30000])) // 1.25s
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, audioLine(audio[30000:]))
		io.WriteString(w, endLine(true, "eos", len(audio)))
	}}
	srv := httptest.NewServer(h)
	defer srv.Close()
	v, _ := Open("custom", customConfig(t, srv.URL), log.New(io.Discard, "", 0))
	defer v.Close()

	a, err := v.Speak("Hallo.")
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Next()
	if err != nil || first == "" {
		t.Fatalf("first piece: %q %v", first, err)
	}
	close(release)
	_, samples := readAll(t, a)
	info, _ := os.Stat(first)
	if total := samples + int(info.Size())/2; total != len(audio)/3 {
		t.Fatalf("got %d samples, want %d", total, len(audio)/3)
	}
}

func TestCustomRequestsABrokenReplyAgainOnce(t *testing.T) {
	audio := tone(2, 440)
	h := &higgsServer{t: t}
	h.reply = func(n int, w http.ResponseWriter) {
		if n == 1 {
			io.WriteString(w, startLine)
			io.WriteString(w, audioLine(audio[:6000]))
			io.WriteString(w, endLine(false, "engine_stall", 6000))
			return
		}
		wholeReply(audio)(n, w)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	v, _ := Open("custom", customConfig(t, srv.URL), log.New(io.Discard, "", 0))
	defer v.Close()
	a, err := v.Speak("Hallo.")
	if err != nil {
		t.Fatal(err)
	}
	if _, samples := readAll(t, a); samples != len(audio)/3 || h.speeches != 2 {
		t.Fatalf("samples=%d requests=%d", samples, h.speeches)
	}
}

// syncLog is a log sink the test can read while stream goroutines still write.
type syncLog struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type fakeSpeaker struct{ calls int }

func (f *fakeSpeaker) speak(string) (Audio, error) {
	f.calls++
	return &fileAudio{path: "fallback.wav"}, nil
}

func TestCustomFallbackPerReplyAndPerCall(t *testing.T) {
	audio := tone(1.5, 440)
	// Under the 1s prebuffer: a failure there happens before playback starts.
	// A failure after it is a reply broken mid-playback, repeated by the caller.
	short := audio[:12000]
	for _, tc := range []struct {
		name     string
		reply    func(int, http.ResponseWriter)
		requests int  // speech requests for two replies
		switched bool // the second reply skipped the service
	}{
		{"model broke the reply off twice", func(n int, w http.ResponseWriter) {
			if n <= 2 {
				io.WriteString(w, startLine+audioLine(audio[:3000])+endLine(false, "too_short", 3000))
				return
			}
			wholeReply(audio)(n, w)
		}, 3, false},
		{"rejected tag", func(n int, w http.ResponseWriter) {
			if n == 1 {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":{"code":"unsupported_tag","message":"<|x|> Hallo"}}`)
				return
			}
			wholeReply(audio)(n, w)
		}, 2, false},
		{"engine down", func(n int, w http.ResponseWriter) {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":"engine_unavailable"}}`)
		}, 1, true},
		{"nginx page", func(n int, w http.ResponseWriter) {
			w.WriteHeader(502)
			io.WriteString(w, "<html>Bad Gateway</html>")
		}, 1, true},
		{"samples missing", func(n int, w http.ResponseWriter) {
			io.WriteString(w, startLine+audioLine(short)+endLine(true, "eos", len(short)+10))
		}, 2, true},
		{"no end line", func(n int, w http.ResponseWriter) {
			io.WriteString(w, startLine+audioLine(short))
		}, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &higgsServer{t: t, reply: tc.reply}
			srv := httptest.NewServer(h)
			defer srv.Close()
			var out syncLog
			logger := log.New(&out, "", 0)
			client := newCustomClient(customConfig(t, srv.URL), logger)
			backup := &fakeSpeaker{}
			v := &Voice{primary: &customSpeaker{client: client, dialect: higgsDialect(), logger: logger},
				fallback: backup, primaryName: "custom", backupName: "openrouter", logger: logger}
			for i := 0; i < 2; i++ {
				a, err := v.Speak("Hallo.")
				if err != nil {
					t.Fatal(err)
				}
				for path, err := a.Next(); path != "" || err != nil; path, err = a.Next() {
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if h.speeches != tc.requests || v.onFallback != tc.switched || backup.calls < 1 {
				t.Fatalf("requests=%d fallback=%v backup calls=%d\n%s", h.speeches, v.onFallback, backup.calls, out.String())
			}
			if strings.Contains(out.String(), "Hallo") || strings.Contains(out.String(), "Bad Gateway") {
				t.Fatalf("reply text or error body reached the log: %s", out.String())
			}
		})
	}
}

func TestCustomCallWithoutAPlaceSpeaksThroughTheFallback(t *testing.T) {
	h := &higgsServer{t: t, putCode: 503, reply: func(int, http.ResponseWriter) { t.Error("speech requested without a place") }}
	srv := httptest.NewServer(h)
	defer srv.Close()
	cfg := customConfig(t, srv.URL)
	cfg.FallbackEngine = "openrouter"
	cfg.OpenRouterTTSModel = "x-ai/grok-voice-tts-1.0"
	var out strings.Builder
	v, dialect := Open("custom", cfg, log.New(&out, "", 0))
	v.Close()
	if dialect.Name != "grok" || !v.onFallback || !strings.Contains(out.String(), "no_capacity") {
		t.Fatalf("dialect=%s fallback=%v log=%s", dialect.Name, v.onFallback, out.String())
	}
	// Tags the dialog model writes for one engine are never read out by another.
	if got := dialect.Sanitize("<|sfx:laughter|>Haha, [laugh] gut."); got != "Haha, [laugh] gut." {
		t.Fatalf("grok sanitize: %q", got)
	}
	if got := strings.Join(h.log(), ","); got != "PUT gt-1731940000.42" {
		t.Fatalf("requests: %s", got)
	}
}

func TestCustomFallbackEngineValidation(t *testing.T) {
	for _, tc := range []struct{ fallback, want string }{
		{"elevenlabs", "elevenlabs"},
		{" OpenRouter ", "openrouter"},
		{"custom", ""},
		{"elevenlab", ""},
		{"", ""},
	} {
		var out strings.Builder
		name, _, _ := fallbackFor(Config{FallbackEngine: tc.fallback}, log.New(&out, "", 0))
		if name != tc.want || (tc.want == "" && tc.fallback != "" && !strings.Contains(out.String(), "ERROR")) {
			t.Errorf("%q: got %q, log %q", tc.fallback, name, out.String())
		}
	}
	// A file engine keeps its old behaviour: no fallback, no session.
	v, _ := Open("openrouter", Config{FallbackEngine: "elevenlabs"}, log.New(io.Discard, "", 0))
	if v.fallback != nil || v.session != nil {
		t.Fatal("fallback attached to a file engine")
	}
}

func TestStopCancelsAStreamingReply(t *testing.T) {
	audio := tone(2, 440)
	finished := make(chan struct{})
	h := &higgsServer{t: t, reply: func(_ int, w http.ResponseWriter) {
		defer close(finished)
		io.WriteString(w, startLine+audioLine(audio))
		w.(http.Flusher).Flush()
		select {
		case <-time.After(5 * time.Second):
			t.Error("stream not cancelled")
		case <-w.(interface{ CloseNotify() <-chan bool }).CloseNotify():
		}
	}}
	srv := httptest.NewServer(h)
	defer srv.Close()
	v, _ := Open("custom", customConfig(t, srv.URL), log.New(io.Discard, "", 0))
	defer v.Close()
	a, err := v.Speak("Hallo.")
	if err != nil {
		t.Fatal(err)
	}
	a.Stop()
	<-finished
	if path, _ := a.Next(); path != "" {
		t.Fatalf("stopped reply handed out %q", path)
	}
}

func TestHiggsDialect(t *testing.T) {
	d := DialectFor("custom", Config{StyleTags: "auto"}, log.New(io.Discard, "", 0))
	in := "<|emotion:anger|>Nein! <|prosody:long_pause|>Nie. <whisper>Leise</whisper> <|sfx:laughter|>Haha, <|emotion:Anger|>gut. <|prosody:pause|> Und?"
	if got := d.Sanitize(in); got != "<|emotion:anger|>Nein! Nie. Leise <|sfx:laughter|>Haha, gut. <|prosody:pause|> Und?" {
		t.Fatalf("sanitize: %q", got)
	}
	if got := PlainText(in); got != "Nein! Nie. Leise Haha, gut. Und?" {
		t.Fatalf("plain: %q", got)
	}
	if pinned := DialectFor("openrouter", Config{StyleTags: "higgs"}, log.New(io.Discard, "", 0)); pinned.Name != "higgs" {
		t.Fatalf("pinned dialect %q", pinned.Name)
	}
	ru := d.GuideFor("ru")
	if !strings.Contains(ru, "по-русски") || strings.Contains(ru, "Deine Antwort") || strings.Contains(ru, "long_pause") {
		t.Fatalf("Russian guide: %q", ru)
	}
	for _, guide := range []string{d.Guide, ru} {
		for _, tag := range higgsTagPattern.FindAllString(guide, -1) {
			if !higgsTags[tag] && tag != "<|kategorie:name|>" {
				t.Errorf("guide offers a tag the server rejects: %s", tag)
			}
		}
	}
}

func TestDecimatorKeepsSpeechAndRemovesAliases(t *testing.T) {
	rms := func(freq float64) float64 {
		d, err := newDecimator(24000)
		if err != nil {
			t.Fatal(err)
		}
		in := make([]int16, 24000)
		for i := range in {
			in[i] = int16(10000 * math.Sin(2*math.Pi*freq*float64(i)/24000))
		}
		var out []int16
		for i := 0; i < len(in); i += 777 { // arbitrary piece sizes: the state must carry over
			j := i + 777
			if j > len(in) {
				j = len(in)
			}
			out = append(out, d.push(in[i:j])...)
		}
		out = append(out, d.flush()...)
		if len(out) != 8000 {
			t.Fatalf("%v Hz: %d output samples, want 8000", freq, len(out))
		}
		sum := 0.0
		for _, s := range out[400:7600] {
			sum += float64(s) * float64(s)
		}
		return math.Sqrt(sum / 7200)
	}
	if got := rms(1000); math.Abs(got-7071) > 150 {
		t.Fatalf("1 kHz passes at rms %.0f, want ~7071", got)
	}
	if got := rms(6000); got > 30 {
		t.Fatalf("6 kHz aliases into the phone band at rms %.0f", got)
	}
	if _, err := newDecimator(22050); err == nil {
		t.Fatal("non-integer ratio accepted")
	}
}

func TestQuietCutLandsInThePause(t *testing.T) {
	pcm := make([]int16, 16000)
	for i := range pcm {
		if i < 11000 || i > 11800 {
			pcm[i] = int16(5000 * math.Sin(float64(i)))
		}
	}
	if cut := quietCut(pcm); cut < 11000 || cut > 11800 {
		t.Fatalf("cut at %d, outside the pause 11000-11800", cut)
	}
}

func TestCustomReplyBrokenMidPlaybackEndsWithAnError(t *testing.T) {
	audio := tone(2, 440)
	release := make(chan struct{})
	h := &higgsServer{t: t, reply: func(_ int, w http.ResponseWriter) {
		io.WriteString(w, startLine+audioLine(audio))
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, endLine(false, "engine_error", len(audio)))
	}}
	srv := httptest.NewServer(h)
	defer srv.Close()
	v, _ := Open("custom", customConfig(t, srv.URL), log.New(io.Discard, "", 0))
	defer v.Close()
	a, err := v.Speak("Hallo.")
	if err != nil {
		t.Fatalf("playback should start before the end line: %v", err)
	}
	var last error
	pieces := 0
	for {
		if pieces == 1 {
			close(release)
		}
		path, err := a.Next()
		if path == "" {
			last = err
			break
		}
		pieces++
	}
	if pieces == 0 || last == nil || !strings.Contains(last.Error(), "engine_error") {
		t.Fatalf("pieces=%d err=%v", pieces, last)
	}
}

func TestTwoRepliesBrokenDuringPlaybackMoveTheCallToTheFallback(t *testing.T) {
	for _, tc := range []struct {
		reason   string
		switched bool
	}{{"engine_error", true}, {"too_short", false}} {
		t.Run(tc.reason, func(t *testing.T) {
			var out syncLog
			logger := log.New(&out, "", 0)
			backup := &fakeSpeaker{}
			v := &Voice{primary: &brokenSpeaker{reason: tc.reason}, fallback: backup,
				primaryName: "custom", backupName: "openrouter", logger: logger}
			for i := 0; i < 2; i++ {
				a, err := v.Speak("Hallo.")
				if err != nil {
					t.Fatal(err)
				}
				for path, err := a.Next(); path != "" || err != nil; path, err = a.Next() {
					if err != nil {
						break
					}
				}
			}
			if backup.calls != 0 {
				t.Fatal("first repeat left the primary voice")
			}
			if _, err := v.Speak("Hallo."); err != nil || backup.calls != 1 || v.onFallback != tc.switched {
				t.Fatalf("third attempt: err=%v backup=%d switched=%v\n%s", err, backup.calls, v.onFallback, out.String())
			}
		})
	}
}

// brokenSpeaker hands out one piece and then reports the reply broken.
type brokenSpeaker struct{ reason string }

func (b *brokenSpeaker) speak(string) (Audio, error) {
	return &brokenAudio{err: &customError{code: b.reason, phrase: phraseReasons[b.reason], broke: true}}, nil
}

type brokenAudio struct {
	err    error
	handed bool
}

func (a *brokenAudio) Next() (string, error) {
	if a.handed {
		return "", a.err
	}
	a.handed = true
	return "piece.sln", nil
}
func (a *brokenAudio) Files() []string { return nil }
func (a *brokenAudio) Stop()           {}
