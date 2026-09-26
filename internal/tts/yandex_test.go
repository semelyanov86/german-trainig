package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestYandexAudioStream(t *testing.T) {
	stream := `{"result":{"textChunk":{"text":"Привет"},"chunkType":"TEXT_ONLY"}}
{"result":{"audioChunk":{"data":"UklGRg=="}}}
{"result":{"audioChunk":{"data":"V0FWRQ=="}}}`
	var out bytes.Buffer
	if err := readYandexAudio(strings.NewReader(stream), &out); err != nil || out.String() != "RIFFWAVE" {
		t.Fatalf("stream truncated or text chunk not skipped: %q, %v", out.String(), err)
	}
	for name, data := range map[string]string{
		"empty": "", "no audio": `{"result":{"textChunk":{"text":"личный разговор"}}}`,
		"schema": `{}`, "null result": `{"result":null}`,
		"bad base64": `{"result":{"audioChunk":{"data":"bad!"}}}`,
		"incomplete": `{"result":{"audioChunk":`,
		"late error": stream + `{"error":{"code":8,"message":"личный разговор"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			err := readYandexAudio(strings.NewReader(data), &output)
			if err == nil || strings.Contains(err.Error(), "личный разговор") {
				t.Fatalf("invalid response accepted or private text exposed: %v", err)
			}
		})
	}
}

type yandexTimeoutReader struct{}

func (yandexTimeoutReader) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }

func TestYandexStreamTimeoutRemainsDiagnosable(t *testing.T) {
	if err := readYandexAudio(yandexTimeoutReader{}, io.Discard); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout hidden by stream parser: %v", err)
	}
}

func TestYandexAuthorization(t *testing.T) {
	for _, tc := range []struct{ token, mode, want string }{
		{"AQ-test-key", "", "Api-Key AQ-test-key"},
		{"t1.test-iam", "auto", "Bearer t1.test-iam"},
		{"custom-iam", "iam", "Bearer custom-iam"},
		{"custom-key", "api-key", "Api-Key custom-key"},
	} {
		got, err := yandexAuthorization(tc.token, tc.mode)
		if err != nil || got != tc.want {
			t.Fatalf("authentication mode %s: %q, %v", tc.mode, got, err)
		}
	}
	for _, tc := range []struct{ token, mode string }{{"", "auto"}, {"secret", "typo"}} {
		if _, err := yandexAuthorization(tc.token, tc.mode); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("bad credential configuration accepted or exposed: %v", err)
		}
	}
}

type yandexTransport struct{ target string }

func (t yandexTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(t.target, "http://")
	return http.DefaultTransport.RoundTrip(clone)
}

func TestYandexHTTPAndAsteriskAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is required for the production audio conversion")
	}
	wave := testYandexWAV()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tts/v3/utteranceSynthesis" || r.Method != "POST" || r.Header.Get("Authorization") != "Api-Key test-key" || r.Header.Get("x-folder-id") != "test-folder" {
			t.Error("wrong endpoint, method, auth or folder")
		}
		var payload struct {
			Model  string              `json:"model"`
			Text   string              `json:"text"`
			Hints  []map[string]string `json:"hints"`
			Unsafe bool                `json:"unsafeMode"`
			Output struct {
				Container struct {
					Type string `json:"containerAudioType"`
				} `json:"containerAudio"`
			} `json:"outputAudioSpec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "livetts" || payload.Text != "Привет!" || len(payload.Hints) != 2 || payload.Hints[0]["voice"] != "sofia" || payload.Hints[1]["role"] != "casual" || !payload.Unsafe || payload.Output.Container.Type != "WAV" {
			t.Errorf("wrong synthesis request: %+v", payload)
		}
		encoder := json.NewEncoder(w)
		encoder.Encode(map[string]interface{}{"result": map[string]interface{}{"textChunk": map[string]string{"text": "Привет!"}}})
		for _, part := range [][]byte{wave[:300], wave[300:]} {
			encoder.Encode(map[string]interface{}{"result": map[string]interface{}{"audioChunk": map[string][]byte{"data": part}}})
		}
	}))
	defer server.Close()
	y := YandexSynth{cfg: Config{YandexToken: "test-key", YandexFolderID: "test-folder", TempDir: t.TempDir(), SessionID: "test"}, logger: log.New(io.Discard, "", 0), client: &http.Client{Transport: yandexTransport{server.URL}}}
	path, files, err := y.Synthesize("Привет!")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 16000 || string(data[:4]) != "RIFF" || binary.LittleEndian.Uint32(data[24:28]) != 8000 || binary.LittleEndian.Uint16(data[22:24]) != 1 {
		t.Fatalf("not playable 8kHz mono WAV: bytes=%d, err=%v", len(data), err)
	}
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("audio is not private: %s, %v", file, err)
		}
	}
}

func TestYandexFailureNeverReturnsPlayablePartialAudio(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"HTTP", `{"message":"private-input test-key"}`, 403},
		{"late error", `{"result":{"audioChunk":{"data":"UklGRg=="}}}{"error":{"code":8,"message":"private-input test-key"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer server.Close()
			y := YandexSynth{cfg: Config{YandexToken: "test-key", TempDir: t.TempDir()}, logger: log.New(io.Discard, "", 0), client: &http.Client{Transport: yandexTransport{server.URL}}}
			path, files, err := y.Synthesize("private-input")
			if path != "" || err == nil || strings.Contains(err.Error(), "private-input") || strings.Contains(err.Error(), "test-key") {
				t.Fatalf("failure exposes partial audio or input: path=%q err=%v", path, err)
			}
			if tc.status == 200 && len(files) != 2 {
				t.Fatalf("partial files missing from cleanup: %v", files)
			}
		})
	}
}

func testYandexWAV() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+48000))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	for _, value := range []uint16{1, 1} {
		binary.Write(&b, binary.LittleEndian, value)
	}
	for _, value := range []uint32{24000, 48000} {
		binary.Write(&b, binary.LittleEndian, value)
	}
	for _, value := range []uint16{2, 16} {
		binary.Write(&b, binary.LittleEndian, value)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(48000))
	b.Write(make([]byte, 48000))
	return b.Bytes()
}

func TestYandexDialectAndCleanHistory(t *testing.T) {
	d := DialectFor("yandex", Config{}, log.New(io.Discard, "", 0))
	text := "Привет! <[small]> Как ты? sil<[300]> <[accented]>Важно **сейчас**. Зам+ок. [sigh] <soft>Расскажи</soft>."
	want := "Привет! <[small]> Как ты? sil<[300]> <[accented]>Важно **сейчас**. Зам+ок. Расскажи."
	if d.Name != "yandex" || d.Sanitize(text) != want {
		t.Fatalf("wrong native markup: %q", d.Sanitize(text))
	}
	plain := "Привет! Как ты? Важно сейчас. Замок. Расскажи."
	if PlainText(text) != plain || (Dialect{Name: "off"}).Sanitize(text) != plain {
		t.Fatalf("native markup leaked into history or disabled styling: %q", PlainText(text))
	}
	if d.Sanitize("**Тебе сейчас** непросто.") != "**Тебе сейчас** непросто." || PlainText("**Тебе сейчас** непросто.") != "Тебе сейчас непросто." {
		t.Fatal("phrase emphasis or its plain transcript was lost")
	}
	if strings.Contains(d.Sanitize("**[sigh] Важные слова**"), "sigh") || strings.Contains(PlainText("**[sigh] Важные слова**"), "sigh") {
		t.Fatal("foreign tags hidden inside emphasis passed the filter")
	}
	for _, tag := range []string{"<[tiny]>", "<[small]>", "<[medium]>", "<[large]>", "<[huge]>", "sil<[1]>", "sil<[7000]>"} {
		if !strings.Contains(d.Sanitize("До "+tag+" после."), tag) {
			t.Errorf("supported pause dropped: %s", tag)
		}
	}
	for _, tag := range []string{"sil<[0]>", "sil<[7001]>", "sil<[text]>", "<[soft]>", "[[f o n]]", "[whisper]"} {
		if d.Sanitize("До "+tag+" после.") != "До после." {
			t.Errorf("invalid markup passed: %s", tag)
		}
	}
	if strings.Contains(d.GuideFor("ru"), "Deine Antwort") || !strings.Contains(d.GuideFor("ru"), "sil<[300]>") {
		t.Fatal("Russian SpeechKit guide missing")
	}
}
