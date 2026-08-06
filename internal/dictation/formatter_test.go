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
	"time"
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
		document := `{"blocks":[{"type":"bullets","items":["Use a dark theme","Include pricing"]}]}`
		body, err := json.Marshal(map[string]any{"message": map[string]string{"content": document}})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
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
	if properties["blocks"] == nil || properties["layout"] != nil || properties["content"] != nil || properties["break_after"] != nil {
		t.Fatalf("expected strict block document schema: %#v", format)
	}
	messages := payload["messages"].([]any)
	system := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "Likely context") || !strings.Contains(system, "unreviewed source data") || !strings.Contains(system, "Never answer") || !strings.Contains(system, "JSON block document") || !strings.Contains(system, "MUST NOT collapse") || !strings.Contains(system, "grammatical person") {
		t.Fatalf("unexpected system prompt: %q", system)
	}
	user := messages[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "Transcript data only") || !strings.Contains(user, "\"transcript\"") || !strings.Contains(user, "Build a landing page use a dark theme") {
		t.Fatalf("source text was not safely wrapped: %q", user)
	}
	source := formatterSourceMessage("1. First task\n2. Second task")
	if strings.Contains(source, "STRUCTURE REQUIREMENT") || !strings.Contains(source, "\"transcript\"") {
		t.Fatalf("source data must not become hidden formatting instructions: %q", source)
	}
}

func TestFormatWithOllamaRendersMixedBlockDocument(t *testing.T) {
	oldClient := formatterHTTPClient
	formatterHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		document := `{"blocks":[{"type":"paragraph","text":"Hold a key, speak, release."},{"type":"heading","text":"Under the hood"},{"type":"numbered","items":["ElevenLabs Scribe v2 for transcription","A local Qwen model running on my own GPU for formatting longer dictations","Everything stored locally, audio auto deleted after an hour"]},{"type":"paragraph","text":"Sometimes the fastest way to get a tool you need isn't waiting for someone to build it."},{"type":"paragraph","text":"Code's here if you're curious: GitHub link."}]}`
		body, err := json.Marshal(map[string]any{"message": map[string]string{"content": document}})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
	defer func() { formatterHTTPClient = oldClient }()

	result, err := FormatWithOllama(context.Background(), "Hold a key, speak, release. Under the hood: ElevenLabs Scribe v2 for transcription, a local Qwen model for formatting, and everything stored locally. Sometimes the fastest way to get a tool you need isn't waiting for someone to build it. Code's here if you're curious: GitHub link.", "Active style: LinkedIn post.", DefaultConfig().Formatter)
	if err != nil {
		t.Fatal(err)
	}
	want := "Hold a key, speak, release.\n\nUNDER THE HOOD:\n\n1. ElevenLabs Scribe v2 for transcription\n2. A local Qwen model running on my own GPU for formatting longer dictations\n3. Everything stored locally, audio auto deleted after an hour\n\nSometimes the fastest way to get a tool you need isn't waiting for someone to build it.\n\nCode's here if you're curious: GitHub link."
	if result.Text != want {
		t.Fatalf("rendered document = %q, want %q", result.Text, want)
	}
}

func TestFormatterDeadlineScalesWithTranscriptSize(t *testing.T) {
	cfg := DefaultConfig().Formatter
	if got := formatterDeadline("a short dictation", cfg); got != 10*time.Second {
		t.Fatalf("short deadline = %s, want 10s", got)
	}
	medium := strings.Repeat("word ", 101)
	if got := formatterDeadline(medium, cfg); got != 30*time.Second {
		t.Fatalf("medium deadline = %s, want 30s", got)
	}
	long := strings.Repeat("word ", 251)
	if got := formatterDeadline(long, cfg); got != 60*time.Second {
		t.Fatalf("long deadline = %s, want 60s", got)
	}
}

func TestDefaultFormatterAllowsStructuredPostOutput(t *testing.T) {
	if got := DefaultConfig().Formatter.MaxOutputTokens; got < 512 {
		t.Fatalf("formatter output cap = %d, want at least 512 tokens for block documents", got)
	}
}

func TestRenderFormatterDocument(t *testing.T) {
	document := formatterDocument{Blocks: []formatterBlock{
		{Type: "paragraph", Text: "A concise opening."},
		{Type: "heading", Text: "Implementation details"},
		{Type: "numbered", Items: []string{"Capture speech", "Format the transcript", "Insert it"}},
		{Type: "bullets", Items: []string{"Local processing", "Retry support"}},
	}}
	got, err := renderFormatterDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	want := "A concise opening.\n\nIMPLEMENTATION DETAILS:\n\n1. Capture speech\n2. Format the transcript\n3. Insert it\n\n- Local processing\n- Retry support"
	if got != want {
		t.Fatalf("rendered document = %q, want %q", got, want)
	}
	if _, err := renderFormatterDocument(formatterDocument{Blocks: []formatterBlock{{Type: "numbered", Items: []string{"only one"}}}}); err == nil {
		t.Fatal("single-item list must be rejected")
	}
	if _, err := renderFormatterDocument(formatterDocument{Blocks: []formatterBlock{{Type: "paragraph", Text: "x", Items: []string{"not allowed"}}}}); err == nil {
		t.Fatal("mixed paragraph block must be rejected")
	}
}

func TestNormalizeFormatterDocumentUsesGeneralEnumerationGrammar(t *testing.T) {
	document := formatterDocument{Blocks: []formatterBlock{
		{Type: "paragraph", Text: "The work has three parts. Implementation details, one, capture audio, two, transcribe it, three, insert the cleaned text. Then verify the result."},
	}}
	got := normalizeFormatterDocument(document, "The work has three parts. Implementation details, one, capture audio, two, transcribe it, three, insert the cleaned text. Then verify the result.")
	want := []formatterBlock{
		{Type: "paragraph", Text: "The work has three parts."},
		{Type: "heading", Text: "Implementation details"},
		{Type: "numbered", Items: []string{"capture audio", "transcribe it", "insert the cleaned text."}},
		{Type: "paragraph", Text: "Then verify the result."},
	}
	if !equalFormatterBlocks(got.Blocks, want) {
		t.Fatalf("normalized blocks = %#v, want %#v", got.Blocks, want)
	}
	withoutInventedHeading := normalizeFormatterDocument(formatterDocument{Blocks: []formatterBlock{{Type: "heading", Text: "Request for assistance"}, {Type: "paragraph", Text: "Please identify the issue."}}}, "Please identify the issue.")
	if len(withoutInventedHeading.Blocks) != 1 || withoutInventedHeading.Blocks[0].Text != "Please identify the issue." {
		t.Fatalf("invented heading must be removed: %#v", withoutInventedHeading.Blocks)
	}
	paragraphs := normalizeFormatterDocument(formatterDocument{Blocks: []formatterBlock{{Type: "paragraph", Text: "One. Two. Three. Four."}}}, "One. Two. Three. Four.")
	if len(paragraphs.Blocks) != 2 || paragraphs.Blocks[0].Text != "One. Two." || paragraphs.Blocks[1].Text != "Three. Four." {
		t.Fatalf("long paragraph should be split mechanically: %#v", paragraphs.Blocks)
	}
	unchanged := normalizeFormatterDocument(formatterDocument{Blocks: []formatterBlock{{Type: "paragraph", Text: "The first draft was better than the second draft."}}}, "The first draft was better than the second draft.")
	if len(unchanged.Blocks) != 1 || unchanged.Blocks[0].Type != "paragraph" {
		t.Fatalf("ordinary comparison must remain prose: %#v", unchanged.Blocks)
	}
}

func equalFormatterBlocks(got, want []formatterBlock) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index].Type != want[index].Type || got[index].Text != want[index].Text || strings.Join(got[index].Items, "\x00") != strings.Join(want[index].Items, "\x00") {
			return false
		}
	}
	return true
}

func TestNormalizePlainTextUsesReadableStructure(t *testing.T) {
	got := normalizePlainText("# Summary\n* First item\n+ Second item\n**Keep bold**")
	want := "SUMMARY\n- First item\n- Second item\n**Keep bold**"
	if got != want {
		t.Fatalf("normalized text = %q, want %q", got, want)
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

func TestWtypeCommandsUseOneProcessForMultilineText(t *testing.T) {
	got := wtypeCommands("first line\n\n-second line")
	want := [][]string{
		{"first line", "-M", "shift", "-k", "Return", "-m", "shift", "-M", "shift", "-k", "Return", "-m", "shift", "-k", "minus", "second line"},
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

func TestTypingDeadlineHasTenSecondFloorAndScalesForLongPosts(t *testing.T) {
	if got := typingDeadline("short note"); got != 10*time.Second {
		t.Fatalf("short insertion deadline = %s, want 10s", got)
	}
	if got := typingDeadline(strings.Repeat("x", 1201)); got <= 10*time.Second {
		t.Fatalf("long insertion deadline = %s, want more than 10s", got)
	}
	if got := typingDeadline(strings.Repeat("x", 10000)); got != 30*time.Second {
		t.Fatalf("very long insertion deadline = %s, want 30s cap", got)
	}
}

func TestAppendLinkedInFooterIsFinalAndIdempotent(t *testing.T) {
	linkedin := WindowTarget{Class: "brave-browser", Title: "Feed | LinkedIn - Brave"}
	got := appendLinkedInFooter("A post about JFlow.", linkedin)
	want := "A post about JFlow.\n\n— Written using JFlow"
	if got != want {
		t.Fatalf("LinkedIn footer = %q, want %q", got, want)
	}
	if gotAgain := appendLinkedInFooter(got, linkedin); gotAgain != want {
		t.Fatalf("LinkedIn footer duplicated: %q", gotAgain)
	}
	nonLinkedIn := appendLinkedInFooter("A post about JFlow.", WindowTarget{Class: "kitty", Title: "terminal"})
	if nonLinkedIn != "A post about JFlow." {
		t.Fatalf("non-LinkedIn text changed: %q", nonLinkedIn)
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
