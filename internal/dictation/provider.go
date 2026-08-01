package dictation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nhooyr.io/websocket"
)

type ProviderError struct {
	Err       error
	Retryable bool
}

func (e *ProviderError) Error() string { return e.Err.Error() }
func retryable(err error) error        { return &ProviderError{Err: err, Retryable: true} }
func permanent(err error) error        { return &ProviderError{Err: err} }
func IsRetryable(err error) bool       { var e *ProviderError; return errors.As(err, &e) && e.Retryable }

type ASRProvider interface {
	Transcribe(context.Context, string, Config) (string, error)
}

func ProviderFor(cfg Config) ASRProvider {
	switch cfg.ASR.Provider {
	case "sarvam":
		return sarvamProvider{}
	case "whisper_cli":
		return whisperProvider{}
	default:
		return elevenProvider{}
	}
}

type elevenProvider struct{}

func (elevenProvider) Transcribe(ctx context.Context, audio string, cfg Config) (string, error) {
	key := os.Getenv(cfg.ASR.APIKeyEnv)
	if key == "" {
		return "", permanent(fmt.Errorf("%s is not set", cfg.ASR.APIKeyEnv))
	}
	endpoint := cfg.ASR.Endpoint
	if endpoint == "" || strings.HasPrefix(endpoint, "ws") {
		endpoint = "https://api.elevenlabs.io/v1/speech-to-text"
	}
	model := cfg.ASR.Model
	if model == "" || strings.Contains(model, "realtime") {
		model = "scribe_v2"
	}
	fields := map[string][]string{"model_id": {model}}
	if cfg.ASR.Language != "" {
		fields["language_code"] = []string{cfg.ASR.Language}
	}
	if cfg.ASR.NoVerbatim {
		fields["no_verbatim"] = []string{"true"}
	}
	for _, term := range cfg.ASR.Keyterms {
		fields["keyterms"] = append(fields["keyterms"], term)
	}
	return multipartTranscribe(ctx, endpoint, key, "xi-api-key", audio, fields)
}

type sarvamProvider struct{}

func (sarvamProvider) Transcribe(ctx context.Context, audio string, cfg Config) (string, error) {
	key := os.Getenv(cfg.ASR.APIKeyEnv)
	if key == "" {
		return "", permanent(fmt.Errorf("%s is not set", cfg.ASR.APIKeyEnv))
	}
	endpoint := cfg.ASR.Endpoint
	if endpoint == "" {
		endpoint = "https://api.sarvam.ai/speech-to-text"
	}
	model := cfg.ASR.Model
	if model == "" {
		model = "saaras:v3"
	}
	fields := map[string][]string{"model": {model}, "mode": {"transcribe"}}
	if cfg.ASR.Language != "" {
		fields["language_code"] = []string{cfg.ASR.Language}
	}
	return multipartTranscribe(ctx, endpoint, key, "api-subscription-key", audio, fields)
}

func multipartTranscribe(ctx context.Context, endpoint, key, keyHeader, audio string, fields map[string][]string) (string, error) {
	f, err := os.Open(audio)
	if err != nil {
		return "", permanent(err)
	}
	defer f.Close()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filepath.Base(audio))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	for k, vals := range fields {
		for _, v := range vals {
			_ = w.WriteField(k, v)
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", permanent(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set(keyHeader, key)
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return "", retryable(fmt.Errorf("asr request: %w", err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return "", retryable(fmt.Errorf("asr HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b))))
	}
	if resp.StatusCode >= 400 {
		return "", permanent(fmt.Errorf("asr HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b))))
	}
	var out struct {
		Text       string `json:"text"`
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", retryable(fmt.Errorf("decode asr response: %w", err))
	}
	if out.Text == "" {
		out.Text = out.Transcript
	}
	if strings.TrimSpace(out.Text) == "" {
		return "", retryable(errors.New("ASR returned an empty transcript"))
	}
	return strings.TrimSpace(out.Text), nil
}

type whisperProvider struct{}

func (whisperProvider) Transcribe(ctx context.Context, audio string, cfg Config) (string, error) {
	bin := cfg.ASR.WhisperBinary
	if bin == "" {
		bin = "whisper-cli"
	}
	if cfg.ASR.WhisperModel == "" {
		return "", permanent(errors.New("asr.whisper_model must be set for whisper_cli"))
	}
	base := strings.TrimSuffix(audio, filepath.Ext(audio))
	cmd := exec.CommandContext(ctx, bin, "-m", cfg.ASR.WhisperModel, "-f", audio, "-otxt", "-of", base)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", retryable(fmt.Errorf("whisper-cli: %w: %s", err, strings.TrimSpace(string(out))))
	}
	b, err := os.ReadFile(base + ".txt")
	if err != nil {
		return "", retryable(err)
	}
	text := strings.TrimSpace(string(b))
	if text == "" {
		return "", retryable(errors.New("whisper returned empty text"))
	}
	return text, nil
}

func Clean(ctx context.Context, raw string, cfg Config) (string, error) {
	if !cfg.Cleanup.Enabled {
		return raw, nil
	}
	key := os.Getenv(cfg.Cleanup.APIKeyEnv)
	if key == "" {
		return raw, permanent(fmt.Errorf("%s is not set", cfg.Cleanup.APIKeyEnv))
	}
	if cfg.Cleanup.Endpoint == "" || cfg.Cleanup.Model == "" {
		return raw, permanent(errors.New("cleanup.endpoint and cleanup.model are required"))
	}
	prompt := "Return only the final intended message. Remove filler words, stutters, repetitions, and explicitly abandoned text. Apply a correction only when unambiguous. Preserve names, numbers, technical terms, commands, language/script, and meaning. Never invent, summarize, translate, or add content."
	payload := map[string]any{"model": cfg.Cleanup.Model, "temperature": 0, "messages": []map[string]string{{"role": "system", "content": prompt}, {"role": "user", "content": raw}}}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Cleanup.Endpoint, bytes.NewReader(b))
	if err != nil {
		return raw, permanent(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return raw, retryable(fmt.Errorf("cleanup request: %w", err))
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return raw, retryable(fmt.Errorf("cleanup HTTP %d", resp.StatusCode))
	}
	if resp.StatusCode >= 400 {
		return raw, permanent(fmt.Errorf("cleanup HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb))))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rb, &out); err != nil || len(out.Choices) == 0 {
		return raw, retryable(errors.New("invalid cleanup response"))
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		return raw, retryable(errors.New("cleanup returned empty text"))
	}
	return text, nil
}

// RealtimeScribe streams audio while it is being recorded. Partial messages are
// intentionally ignored; only committed text is returned after the hotkey is released.
type RealtimeScribe struct {
	conn      *websocket.Conn
	committed chan string
	errs      chan error
}

func StartRealtimeScribe(ctx context.Context, cfg Config) (*RealtimeScribe, error) {
	key := os.Getenv(cfg.ASR.APIKeyEnv)
	if key == "" {
		return nil, permanent(fmt.Errorf("%s is not set", cfg.ASR.APIKeyEnv))
	}
	endpoint := cfg.ASR.Endpoint
	if endpoint == "" || !strings.HasPrefix(endpoint, "ws") {
		endpoint = "wss://api.elevenlabs.io/v1/speech-to-text/realtime"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, permanent(fmt.Errorf("invalid realtime endpoint: %w", err))
	}
	q := u.Query()
	q.Set("model_id", "scribe_v2_realtime")
	q.Set("audio_format", "pcm_16000")
	q.Set("commit_strategy", "manual")
	q.Set("filter_background_audio", "true")
	if cfg.ASR.Language != "" {
		q.Set("language_code", cfg.ASR.Language)
	}
	for _, s := range cfg.ASR.Secondary {
		q.Add("secondary_languages", s)
	}
	for _, term := range cfg.ASR.Keyterms {
		q.Add("keyterms", term)
	}
	if cfg.ASR.NoVerbatim {
		q.Set("no_verbatim", "true")
	}
	u.RawQuery = q.Encode()
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: http.Header{"xi-api-key": []string{key}}})
	if err != nil {
		return nil, retryable(fmt.Errorf("connect realtime ASR: %w", err))
	}
	r := &RealtimeScribe{conn: conn, committed: make(chan string, 8), errs: make(chan error, 1)}
	go r.read()
	return r, nil
}
func (r *RealtimeScribe) read() {
	ctx := context.Background()
	for {
		_, b, err := r.conn.Read(ctx)
		if err != nil {
			select {
			case r.errs <- err:
			default:
			}
			return
		}
		var event struct {
			Type  string `json:"message_type"`
			Text  string `json:"text"`
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &event) != nil {
			continue
		}
		if event.Type == "committed_transcript" && strings.TrimSpace(event.Text) != "" {
			r.committed <- strings.TrimSpace(event.Text)
		}
		if event.Error != "" || strings.Contains(event.Type, "error") || strings.Contains(event.Type, "limited") {
			select {
			case r.errs <- retryable(errors.New(event.Error)):
			default:
			}
		}
	}
}
func (r *RealtimeScribe) Send(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	p := map[string]any{"message_type": "input_audio_chunk", "audio_base_64": encodeBase64(pcm)}
	b, _ := json.Marshal(p)
	return r.conn.Write(ctx, websocket.MessageText, b)
}
func (r *RealtimeScribe) Commit(ctx context.Context) (string, error) {
	b, _ := json.Marshal(map[string]any{"message_type": "input_audio_chunk", "audio_base_64": "", "commit": true})
	if err := r.conn.Write(ctx, websocket.MessageText, b); err != nil {
		return "", retryable(err)
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	var chunks []string
	for {
		select {
		case text := <-r.committed:
			chunks = append(chunks, text)
			// Give final network frames a tiny coalescing window before returning.
			t := time.NewTimer(250 * time.Millisecond)
			select {
			case more := <-r.committed:
				chunks = append(chunks, more)
			case <-t.C:
			}
			t.Stop()
			return strings.Join(chunks, " "), nil
		case err := <-r.errs:
			return "", retryable(err)
		case <-deadline.C:
			return "", retryable(errors.New("timed out waiting for committed transcript"))
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}
func (r *RealtimeScribe) Close() { _ = r.conn.Close(websocket.StatusNormalClosure, "done") }
