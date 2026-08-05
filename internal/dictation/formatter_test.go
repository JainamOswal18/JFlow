package dictation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestFormatWithOllamaUsesSafeLocalPayload(t *testing.T) {
	var payload map[string]any
	oldClient := formatterHTTPClient
	formatterHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"message":{"content":"{\"text\":\"**Build a landing page.**\\n\\n- Use a dark theme\\n- Include pricing\"}"}}`)), Header: make(http.Header)}, nil
	})}
	defer func() { formatterHTTPClient = oldClient }()
	cfg := DefaultConfig().Formatter
	cfg.Endpoint = "http://formatter.test"
	result, err := FormatWithOllama(context.Background(), "Build a landing page use a dark theme and include pricing", "Likely context: an AI-assistant request. Prefer clear structure.", cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Text
	if !strings.Contains(got, "dark theme") {
		t.Fatalf("formatted text = %q", got)
	}
	if !strings.Contains(got, "**Build a landing page.**") || !strings.Contains(got, "- Use a dark theme") || strings.ContainsAny(got, "#`") {
		t.Fatalf("formatter did not preserve approved plain-text structure: %q", got)
	}
	if result.Audit.HTTPStatus != http.StatusOK || result.Audit.Model != cfg.Model || result.Audit.RawResponse == "" || result.Audit.SystemPrompt == "" {
		t.Fatalf("formatter audit is incomplete: %#v", result.Audit)
	}
	if result.Audit.InputText != "Build a landing page use a dark theme and include pricing" || result.Audit.ContextTokens != cfg.ContextTokens || result.Audit.MaxOutput != cfg.MaxOutputTokens {
		t.Fatalf("formatter audit does not describe the request: %#v", result.Audit)
	}
	if payload["think"] != false || payload["stream"] != false {
		t.Fatalf("expected non-thinking non-streaming payload: %#v", payload)
	}
	format := payload["format"].(map[string]any)
	if format["type"] != "object" || format["additionalProperties"] != false {
		t.Fatalf("expected a strict JSON formatter contract: %#v", format)
	}
	messages := payload["messages"].([]any)
	system := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "Likely context") || !strings.Contains(system, "unreviewed source text") || !strings.Contains(system, "Never answer") || !strings.Contains(system, "ALL-CAPS") {
		t.Fatalf("unexpected system prompt: %q", system)
	}
}

func TestNormalizePlainTextUsesReadableStructure(t *testing.T) {
	got := normalizePlainText("# Summary\n* First item\n+ Second item\n**Keep bold**")
	want := "SUMMARY\n- First item\n- Second item\n**Keep bold**"
	if got != want {
		t.Fatalf("normalized text = %q, want %q", got, want)
	}
}

func TestNormalizeSpokenOrdinals(t *testing.T) {
	got, applied := normalizeSpokenOrdinals("All right. The first thing I want you to do is help me with it. Second is to identify the issue, and third is to brainstorm it.")
	want := "1. Help me with it.\n2. Identify the issue.\n3. Brainstorm it."
	if !applied || got != want {
		t.Fatalf("ordinal normalization = %q, applied=%v; want %q", got, applied, want)
	}
	if _, applied := normalizeSpokenOrdinals("The first draft was better than the second draft."); applied {
		t.Fatal("comparison text must not be treated as a spoken task list")
	}
}

func TestFreshFormattingInfoClearsOldAttemptState(t *testing.T) {
	previous := FormattingInfo{
		Eligible: true, Applied: true, Changed: true, ContextHint: "Likely context: an AI-assistant request.",
		RawResponse: "old response", Skipped: "old error", LatencyMS: 100,
	}
	got := freshFormattingInfo(previous, true)
	if !got.Eligible || got.ContextHint != previous.ContextHint {
		t.Fatalf("retry formatting context = %#v", got)
	}
	if got.Applied || got.Changed || got.RawResponse != "" || got.Skipped != "" || got.LatencyMS != 0 {
		t.Fatalf("retry inherited stale formatter state: %#v", got)
	}
}

func TestWtypeCommandsUseShiftEnterForLineBreaks(t *testing.T) {
	got := wtypeCommands("first line\n\n-second line")
	want := [][]string{
		{"--", "first line"},
		{"-M", "shift", "-k", "Return"},
		{"-M", "shift", "-k", "Return"},
		{"--", "-second line"},
	}
	if len(got) != len(want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
	for i := range want {
		if strings.Join(got[i], "\x00") != strings.Join(want[i], "\x00") {
			t.Fatalf("command %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestExpectedPipeClose(t *testing.T) {
	if !isExpectedPipeClose(os.ErrClosed) {
		t.Fatal("os.ErrClosed should be treated as an expected close after stopping capture")
	}
	if !isExpectedPipeClose(errors.New("read |0: file already closed")) {
		t.Fatal("pw-record closed pipe error should be treated as expected after stopping capture")
	}
}

func TestWarmOllamaUsesLocalGenerateEndpoint(t *testing.T) {
	oldClient := formatterHTTPClient
	defer func() { formatterHTTPClient = oldClient }()
	called := false
	formatterHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.URL.Path != "/api/generate" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "qwen3:1.7b" || payload["keep_alive"] != "15m" {
			t.Fatalf("unexpected warm payload: %#v", payload)
		}
		options := payload["options"].(map[string]any)
		if options["num_ctx"] != float64(2048) {
			t.Fatalf("unexpected warm options: %#v", options)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	cfg := DefaultConfig().Formatter
	cfg.Endpoint = "http://formatter.test"
	WarmOllama(context.Background(), cfg)
	if !called {
		t.Fatal("warm endpoint was not called")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
