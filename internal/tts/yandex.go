package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const yandexTTSEndpoint = "https://tts.api.cloud.yandex.net/tts/v3/utteranceSynthesis"

// YandexSynth uses the REST gateway of SpeechKit v3. It returns the same
// streamed utterance chunks as gRPC without adding protobuf/HTTP2 dependencies.
// https://aistudio.yandex.ru/ru/docs/speechkit/tts/api/tts-v3-rest
type YandexSynth struct {
	cfg    Config
	logger *log.Logger
	client *http.Client // nil uses the production client; injectable for HTTP contract checks
}

func (y *YandexSynth) Synthesize(text string) (string, []string, error) {
	start := time.Now()
	auth, err := yandexAuthorization(y.cfg.YandexToken, y.cfg.YandexAuthType)
	if err != nil {
		return "", nil, err
	}
	model, voice, role := y.cfg.YandexTTSModel, y.cfg.YandexTTSVoice, y.cfg.YandexTTSRole
	if model == "" {
		model = "livetts"
	}
	if voice == "" {
		voice = "sofia"
		if strings.HasPrefix(model, "general") {
			voice = "marina"
		}
	}
	// Other voices have different role vocabularies. Omission lets the API use
	// their default instead of accidentally sending Sofia's role to Marina.
	if role == "" && model == "livetts" && voice == "sofia" {
		role = "casual"
	}
	hints := []map[string]string{{"voice": voice}}
	if role != "" {
		hints = append(hints, map[string]string{"role": role})
	}
	payload, err := json.Marshal(map[string]interface{}{
		"model": model, "text": text, "hints": hints,
		"outputAudioSpec": map[string]interface{}{
			"containerAudio": map[string]string{"containerAudioType": "WAV"},
		},
		// The psychologist has no sentence cap. SpeechKit may split a long
		// response into utterances, each billed separately by the provider.
		"unsafeMode": true,
	})
	if err != nil {
		return "", nil, fmt.Errorf("yandex tts encode request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", yandexTTSEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("yandex tts create request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	if y.cfg.YandexFolderID != "" {
		req.Header.Set("x-folder-id", y.cfg.YandexFolderID)
	}
	client := y.client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("yandex tts request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Do not echo upstream text: it can contain the private input or secrets.
		return "", nil, fmt.Errorf("yandex tts HTTP %d", resp.StatusCode)
	}

	base := y.cfg.audioBase()
	src, wav := base+".audio", base+"_8k.wav"
	files := []string{src, wav} // include partially written files on every failure
	f, err := os.OpenFile(src, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("yandex tts create audio: %w", err)
	}
	parseErr := readYandexAudio(resp.Body, f)
	closeErr := f.Close()
	if parseErr != nil {
		return "", files, parseErr
	}
	if closeErr != nil {
		return "", files, fmt.Errorf("yandex tts close audio: %w", closeErr)
	}
	// Create the destination privately before ffmpeg opens it for writing.
	if err := os.WriteFile(wav, nil, 0600); err != nil {
		return "", files, fmt.Errorf("yandex tts create wav: %w", err)
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-y", "-i", src,
		"-ar", "8000", "-ac", "1", "-acodec", "pcm_s16le", "-f", "wav", wav)
	if err := cmd.Run(); err != nil {
		return "", files, fmt.Errorf("yandex tts ffmpeg: %w", err)
	}
	y.logger.Printf("Yandex TTS took %v", time.Since(start))
	return wav, files, nil
}

func yandexAuthorization(token, mode string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("yandex tts: YANDEX_TOKEN is not set")
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		if strings.HasPrefix(token, "t1.") {
			return "Bearer " + token, nil
		}
		return "Api-Key " + token, nil
	case "api-key":
		return "Api-Key " + token, nil
	case "iam":
		return "Bearer " + token, nil
	default:
		return "", fmt.Errorf("yandex tts: invalid YANDEX_AUTH_TYPE")
	}
}

// REST v3 is a stream of JSON objects, not a JSON array. LiveTTS also sends
// text-only messages; these must be skipped without truncating the audio.
func readYandexAudio(r io.Reader, out io.Writer) error {
	const maxResponse = 32 << 20
	limited := &io.LimitedReader{R: r, N: maxResponse + 1}
	decoder := json.NewDecoder(limited)
	total := 0
	for {
		var chunk struct {
			Result *struct {
				AudioChunk struct {
					Data []byte `json:"data"` // JSON decoder validates/decodes base64
				} `json:"audioChunk"`
			} `json:"result"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		err := decoder.Decode(&chunk)
		if limited.N == 0 {
			return fmt.Errorf("yandex tts: audio response exceeds limit")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("yandex tts: audio response timeout")
			}
			var networkError net.Error
			if errors.As(err, &networkError) {
				if networkError.Timeout() {
					return fmt.Errorf("yandex tts: audio response timeout")
				}
				return fmt.Errorf("yandex tts: audio response network error")
			}
			// Syntax errors may quote input text, so keep them out of logs.
			return fmt.Errorf("yandex tts: invalid audio response")
		}
		if chunk.Error != nil {
			return fmt.Errorf("yandex tts: stream error code %d", chunk.Error.Code)
		}
		if chunk.Result == nil {
			return fmt.Errorf("yandex tts: missing result in audio response")
		}
		data := chunk.Result.AudioChunk.Data
		if len(data) == 0 {
			continue
		}
		if _, err := out.Write(data); err != nil {
			return fmt.Errorf("yandex tts write audio: %w", err)
		}
		total += len(data)
	}
	if total == 0 {
		return fmt.Errorf("yandex tts: empty audio response")
	}
	return nil
}
