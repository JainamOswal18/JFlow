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
	"unicode/utf8"
)

var formatWords = regexp.MustCompile(`[\pL\pN][\pL\pN'’-]*`)
var formatterHTTPClient = &http.Client{}

const formatterInstruction = "You are a layout editor, not a writing assistant. Format this transcription for clarity while preserving every meaningful requirement, fact, name, number, and constraint. Never answer the request, infer missing details, create titles, labels, examples, greetings, or new content. Do not remove requirements. Every alphabetic word in the output must already appear in the transcription; you may add only whitespace, punctuation, and Markdown markers. If structure is uncertain, return the transcription unchanged. Return only the final text."

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
	formatted := strings.TrimSpace(out.Message.Content)
	if !validFormattedText(raw, formatted) {
		return raw, errors.New("formatter response did not safely preserve the transcription")
	}
	return formatted, nil
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

func validFormattedText(raw, formatted string) bool {
	if formatted == "" || !utf8.ValidString(formatted) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(formatted))
	if strings.HasPrefix(lower, "here is") || strings.HasPrefix(lower, "formatted transcript") || strings.HasPrefix(lower, "formatted text") {
		return false
	}
	rawLen, outLen := utf8.RuneCountInString(strings.TrimSpace(raw)), utf8.RuneCountInString(formatted)
	if outLen < max(8, rawLen/3) || outLen > rawLen*3+120 {
		return false
	}
	rawWords := meaningfulWordSet(raw)
	if len(rawWords) < 3 {
		return true
	}
	formattedWords := meaningfulWordSet(formatted)
	overlap := 0
	for word := range rawWords {
		if formattedWords[word] {
			overlap++
		}
	}
	if overlap*5 < len(rawWords)*4 { // preserve at least 80% of meaningful terms
		return false
	}
	// A layout pass may introduce a handful of structural words, but should not
	// expand a request into an answer or invented copy.
	novel := 0
	for word := range formattedWords {
		if !rawWords[word] {
			novel++
		}
	}
	if novel > max(4, len(rawWords)/4) {
		return false
	}
	return len(formattedWords) <= len(rawWords)+max(5, len(rawWords)/3)
}

func meaningfulWordSet(text string) map[string]bool {
	words := map[string]bool{}
	for _, word := range formatWords.FindAllString(strings.ToLower(text), -1) {
		if utf8.RuneCountInString(word) >= 4 {
			words[word] = true
		}
	}
	return words
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
