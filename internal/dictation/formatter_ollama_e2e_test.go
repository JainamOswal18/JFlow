package dictation

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestOllamaFormatterE2E is intentionally opt-in: it exercises a real local
// Ollama model with the exact production request, JSON contract, and renderer.
// It sends only synthetic text, never audio, credentials, job history, or a
// cloud request. Run it with JFLOW_OLLAMA_E2E=1.
func TestOllamaFormatterE2E(t *testing.T) {
	if os.Getenv("JFLOW_OLLAMA_E2E") != "1" {
		t.Skip("set JFLOW_OLLAMA_E2E=1 to run against a local Ollama service")
	}
	model := os.Getenv("JFLOW_OLLAMA_MODEL")
	if model == "" {
		model = "qwen3:1.7b"
	}
	cfg := DefaultConfig().Formatter
	cfg.Model = model
	// A comparison run may have to load a model after another one was used.
	// Production keeps its selected model warm and retains its seven-second
	// deadline; this test gives the explicit benchmark a bounded cold-start
	// allowance without changing production behavior.
	cfg.TimeoutSecs = 30

	cases := []struct {
		name      string
		raw       string
		wantParts []string
		forbid    string
		minBreaks int
	}{
		{
			name:      "explicit ordered tasks",
			raw:       "1. Help me with it.\n2. Identify the issue.\n3. Brainstorm it.",
			wantParts: []string{"1. ", "2. ", "3. "},
		},
		{
			name:      "separate requests",
			raw:       "Show me the system prompt. Also, list the next phases and explain how I can test everything.",
			wantParts: []string{"system prompt", "next phases", "test"},
		},
		{
			name:      "ordinary narrative",
			raw:       "I spoke to the team today and we agreed to revisit the launch timeline next week.",
			wantParts: []string{"team", "launch timeline", "next week"},
			forbid:    "\n- ",
		},
		{
			name: "story with distinct beats",
			raw: "I lost my favorite writing tool the day I switched back to Linux. Here's what I built in the next three days. " +
				"I'd just left my last job and moved back to Arch Linux. First thing I missed, Wispr Flow. Nothing like it existed for Hyprland or Wayland. " +
				"So instead of waiting around, I built my own. Meet JFlow. Hold a key, speak, release. Clean text lands wherever you were typing. " +
				"No live transcript cluttering your screen. No lost recordings if a provider hiccups mid-transcription.",
			wantParts: []string{"favorite writing tool", "Wispr Flow", "Meet JFlow", "Hold a key", "No lost recordings"},
			minBreaks: 2,
		},
		{
			name:      "flattened product post with implementation list",
			raw:       "I lost my favorite writing tool the day I switched back to Linux. Here's what I built in the next 3 days. I'd just left my last job and moved back to Arch Linux. First thing I missed? Wispr Flow. Nothing like it existed for Hyprland or Wayland, so instead of waiting around, I built my own. Meet JFlow. Hold a key, speak, release. Clean text lands wherever you were typing. No live transcript cluttering your screen. No lost recordings if a provider hiccups mid transcription. Under the hood: 1. ElevenLabs Scribe v2 for transcription 2. A local Qwen model running on my own GPU for formatting longer dictations 3. Everything stored locally, audio auto deleted after an hour.",
			wantParts: []string{"favorite writing tool", "Wispr Flow", "Meet JFlow", "UNDER THE HOOD", "1. ElevenLabs Scribe", "2. A local Qwen", "3. Everything stored locally"},
			minBreaks: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := FormatWithOllama(ctx, tc.raw, "Likely context: an AI-assistant request. Prefer clear structure.", cfg)
			if err != nil {
				t.Fatalf("formatter request failed: %v; audit=%+v", err, result.Audit)
			}
			t.Logf("model=%s latency=%dms output=%q raw=%s", model, result.Audit.LatencyMS, result.Text, result.Audit.RawResponse)
			for _, part := range tc.wantParts {
				if !strings.Contains(strings.ToLower(result.Text), strings.ToLower(part)) {
					t.Fatalf("formatted output %q is missing %q", result.Text, part)
				}
			}
			if tc.forbid != "" && strings.Contains(result.Text, tc.forbid) {
				t.Fatalf("formatted output %q unexpectedly contains %q", result.Text, tc.forbid)
			}
			if breaks := strings.Count(result.Text, "\n\n"); breaks < tc.minBreaks {
				t.Fatalf("formatted output %q has %d paragraph breaks, want at least %d", result.Text, breaks, tc.minBreaks)
			}
		})
	}
}
