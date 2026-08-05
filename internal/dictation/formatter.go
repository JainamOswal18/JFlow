package dictation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var formatterHTTPClient = &http.Client{}

const formatterInstruction = "You are a plain-text layout editor, not a writing assistant. Format this transcription for clarity while preserving its meaning, facts, names, constraints, and tone. Do not answer the request, add ideas, or remove requirements. Return plain text only: do not use Markdown, headings, bullets, numbered lists, code fences, or emphasis markers. Use ordinary sentences and line breaks only when they improve readability."

var (
	plainHeading = regexp.MustCompile(`^#{1,6}\s+`)
	plainBullet  = regexp.MustCompile(`^(?:[-*+]\s+|\d+[.)]\s+)`)
)

// FormatWithOllama makes one non-streaming local request. It never sends
// window metadata, credentials, or audio; only the already-transcribed text
// and a fixed, sanitized context hint can leave the daemon process.
func FormatWithOllama(ctx context.Context, raw, hint string, cfg FormatterConfig) (string, error) {
	if cfg.Mode != "auto" {
		return raw, errors.New("formatter is disabled")
	}
	if strings.TrimSpace(raw) == "" {
		return raw, errors.New("formatter received empty text")
	}
	system := formatterInstruction
	if hint != "" {
		system += "\n\n" + hint
	}
	payload := map[string]any{
		"model":      cfg.Model,
		"stream":     false,
		"think":      false,
		"keep_alive": cfg.KeepAlive,
		"options": map[string]any{
			"temperature": 0.1,
			"num_ctx":     cfg.ContextTokens,
			"num_predict": cfg.MaxOutputTokens,
		},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": raw},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return raw, err
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/") + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return raw, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := formatterHTTPClient.Do(req)
	if err != nil {
		return raw, fmt.Errorf("local formatter unavailable: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return raw, fmt.Errorf("local formatter HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return raw, fmt.Errorf("invalid local formatter response: %w", err)
	}
	if out.Error != "" {
		return raw, errors.New(out.Error)
	}
	formatted := normalizePlainText(out.Message.Content)
	if formatted == "" {
		return raw, errors.New("formatter returned empty text")
	}
	return formatted, nil
}

// normalizePlainText removes the small set of Markdown markers a model might
// still emit despite the formatter instruction. It deliberately does not
// rewrite wording or sentence structure.
func normalizePlainText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	clean := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			clean = append(clean, line)
			continue
		}
		trimmed = plainHeading.ReplaceAllString(trimmed, "")
		trimmed = plainBullet.ReplaceAllString(trimmed, "")
		trimmed = strings.ReplaceAll(trimmed, "**", "")
		trimmed = strings.ReplaceAll(trimmed, "__", "")
		trimmed = strings.ReplaceAll(trimmed, "~~", "")
		trimmed = strings.ReplaceAll(trimmed, "`", "")
		clean = append(clean, trimmed)
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}

// WarmOllama loads the local model after the daemon starts, outside any
// recording path. A cold CUDA runner can take several seconds to initialize;
// pre-warming prevents the first eligible dictation from needlessly falling
// back to the raw transcript. Failure is intentionally silent because normal
// dictation must remain usable when Ollama is absent.
func WarmOllama(ctx context.Context, cfg FormatterConfig) {
	if cfg.Mode != "auto" {
		return
	}
	payload := map[string]any{
		"model":      cfg.Model,
		"stream":     false,
		"keep_alive": cfg.KeepAlive,
		"options": map[string]any{
			"num_ctx": cfg.ContextTokens,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/") + "/api/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := formatterHTTPClient.Do(req)
	if err == nil && resp != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
	}
}
