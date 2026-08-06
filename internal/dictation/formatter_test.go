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
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"message":{"content":"{\"layout\":\"bullets\",\"content\":[\"- Use a dark theme\",\"Include pricing\"],\"break_after\":[]}"}}`)), Header: make(http.Header)}, nil
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
	if !strings.Contains(got, "- Use a dark theme") || strings.ContainsAny(got, "#`") {
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
	properties := format["properties"].(map[string]any)
	if properties["layout"] == nil || properties["content"] == nil || properties["break_after"] == nil {
		t.Fatalf("expected structural formatter plan: %#v", format)
	}
	messages := payload["messages"].([]any)
	system := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "Likely context") || !strings.Contains(system, "unreviewed source data") || !strings.Contains(system, "Never answer") || !strings.Contains(system, "JSON layout plan") || !strings.Contains(system, "break_after") || !strings.Contains(system, "grammatical person") {
		t.Fatalf("unexpected system prompt: %q", system)
	}
	user := messages[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "FORMAT ONLY THE QUOTED SOURCE DATA") || !strings.Contains(user, "<SOURCE>") || !strings.Contains(user, "Build a landing page use a dark theme") {
		t.Fatalf("source text was not safely wrapped: %q", user)
	}
	ordered := formatterSourceMessage("1. First task\n2. Second task")
	if !strings.Contains(ordered, "STRUCTURE REQUIREMENT") || !strings.Contains(ordered, "layout to numbered") {
		t.Fatalf("explicit source ordering was not preserved in metadata: %q", ordered)
	}
	narrative := formatterSourceMessage("I moved to Linux. I missed a writing tool. Nothing comparable existed. So I built one. Meet JFlow.")
	if !strings.Contains(narrative, "multi-beat spoken monologue") || !strings.Contains(narrative, "layout to paragraph") || !strings.Contains(narrative, "break_after") {
		t.Fatalf("long narrative was not given paragraph metadata: %q", narrative)
	}
}

func TestRenderFormatterPlan(t *testing.T) {
	got, err := renderFormatterPlan(formatterPlan{Layout: "bullets", Content: []string{"- a fast local formatter", "accurate transcription", "reliable retries"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "- a fast local formatter\n- accurate transcription\n- reliable retries"
	if got != want {
		t.Fatalf("rendered plan = %q, want %q", got, want)
	}
	if _, err := renderFormatterPlan(formatterPlan{Layout: "numbered", Content: []string{"only one"}}); err == nil {
		t.Fatal("single-item list must be rejected")
	}
	got, err = renderFormatterPlan(formatterPlan{Layout: "paragraph", Content: []string{"I switched back to Linux. Meet JFlow. Hold a key, speak, release."}, BreakAfter: []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	want = "I switched back to Linux.\n\nMeet JFlow.\n\nHold a key, speak, release."
	if got != want {
		t.Fatalf("rendered paragraph group = %q, want %q", got, want)
	}
	got, err = renderFormatterPlan(formatterPlan{Layout: "paragraph", Content: []string{"First sentence.", "Second sentence.", "Third sentence."}, BreakAfter: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	want = "First sentence. Second sentence.\n\nThird sentence."
	if got != want {
		t.Fatalf("rendered paragraph boundaries = %q, want %q", got, want)
	}
	got, err = renderFormatterPlan(formatterPlan{Layout: "paragraph", Content: []string{"I moved to Linux. Nothing comparable existed. Meet JFlow. Hold a key, speak, release."}, BreakAfter: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	want = "I moved to Linux. Nothing comparable existed.\n\nMeet JFlow.\n\nHold a key, speak, release."
	if got != want {
		t.Fatalf("rendered standalone callout = %q, want %q", got, want)
	}
	got, err = renderFormatterPlan(formatterPlan{Layout: "paragraph", Content: []string{"Meet JFlow. Hold a key, speak, release. Clean text lands here."}, BreakAfter: []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	want = "Meet JFlow.\n\nHold a key, speak, release. Clean text lands here."
	if got != want {
		t.Fatalf("rendered action paragraph = %q, want %q", got, want)
	}
	got, err = renderFormatterPlan(formatterPlan{Layout: "paragraph", Content: []string{"I built JFlow. It works locally. Under the hood: 1. ElevenLabs Scribe v2 for transcription 2. A local Qwen model for formatting 3. Audio auto deleted after an hour."}, BreakAfter: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	want = "I built JFlow. It works locally.\n\nUNDER THE HOOD:\n\n1. ElevenLabs Scribe v2 for transcription\n2. A local Qwen model for formatting\n3. Audio auto deleted after an hour."
	if got != want {
		t.Fatalf("rendered inline numbered section = %q, want %q", got, want)
	}
	got, err = renderFormatterPlan(formatterPlan{Layout: "numbered", Content: []string{"1. First task\n2. Second task"}})
	if err != nil {
		t.Fatal(err)
	}
	want = "1. First task\n2. Second task"
	if got != want {
		t.Fatalf("rendered packed list = %q, want %q", got, want)
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
