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
	// Production keeps its selected model warm; this bounded test allowance
	// covers an explicit cold-model benchmark without changing production.
	cfg.TimeoutSecs = 30

	cases := []struct {
		name        string
		raw         string
		hint        string
		wantParts   []string
		wantMarkers []string
		forbid      string
		minBreaks   int
	}{
		{
			name:        "three-step AI request",
			raw:         "I need three things from you. First, help me understand the formatter issue. Second, identify the root cause. Third, propose a practical fix.",
			wantParts:   []string{"formatter issue", "root cause", "practical fix"},
			wantMarkers: []string{"1. ", "2. ", "3. "},
		},
		{
			name:        "current LinkedIn post from spoken story",
			raw:         "I lost my favorite writing tool the day I switched back to Linux. Here's what I built in the next three days. I had just left my last job and moved back to Arch Linux. First thing I missed, WisprFlow. Nothing like it existed for Hyprland or Wayland. So instead of waiting around, I built my own. Meet JFlow. Hold the key, speak, release. Clean text lands wherever you're typing. No live transcript cluttering your screen. No lost recording if a provider hiccups mid-transcription. Under the hood, one, ElevenLabs Scribe V2 for transcription, two, a local Qwen model running on my own GPU for handling long dictations. Three, everything stored locally. Audio auto-detected, deleted after an hour. Sometimes the fastest way to get a tool you need isn't waiting for someone to build it. It's a free weekend, free tier APIs, and enough annoyance to push through. Code's here if you're curious.",
			hint:        "Active style: LinkedIn post. Keep a confident first-person professional voice; use a short hook, 1 to 3 sentence paragraphs, and a standalone product/reveal line when natural. Use a list only for genuine takeaways; do not add claims or a call to action.",
			wantParts:   []string{"favorite writing tool", "WisprFlow", "Meet JFlow", "Clean text lands", "UNDER THE HOOD", "ElevenLabs Scribe", "local Qwen", "Everything stored locally", "Sometimes the fastest way", "Code's here"},
			wantMarkers: []string{"UNDER THE HOOD:", "1. ", "2. ", "3. "},
			minBreaks:   4,
		},
		{
			name:      "casual two-beat message",
			raw:       "I checked the new build this morning and it feels much smoother now. The only thing still bothering me is the retry button. Can you take a look when you have time? I am happy to test again after that.",
			hint:      "Active style: casual chat. Keep a friendly, direct tone. Use short paragraphs when they help readability.",
			wantParts: []string{"new build", "smoother", "retry button", "test again"},
			forbid:    "\n- ",
			minBreaks: 1,
		},
		{
			name:      "professional email with unordered requirements",
			raw:       "For the proposal, please include the pricing model, the rollout timeline, and the ownership plan. Keep the tone professional and send me a draft by Friday.",
			hint:      "Active style: professional email. Use a list only when it makes an actual set of requirements clearer.",
			wantParts: []string{"pricing model", "rollout timeline", "ownership plan", "Friday"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := tc.hint
			if hint == "" {
				hint = "Likely context: an AI-assistant request. Prefer clear structure."
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := FormatWithOllama(ctx, tc.raw, hint, cfg)
			if err != nil {
				t.Fatalf("formatter request failed: %v; audit=%+v", err, result.Audit)
			}
			t.Logf("model=%s latency=%dms output=%q raw=%s", model, result.Audit.LatencyMS, result.Text, result.Audit.RawResponse)
			for _, part := range tc.wantParts {
				if !strings.Contains(strings.ToLower(result.Text), strings.ToLower(part)) {
					t.Fatalf("formatted output %q is missing %q", result.Text, part)
				}
			}
			for _, marker := range tc.wantMarkers {
				if !strings.Contains(result.Text, marker) {
					t.Fatalf("formatted output %q is missing structural marker %q", result.Text, marker)
				}
			}
			if strings.Contains(result.Text, "```") {
				t.Fatalf("formatted output contains a code fence: %q", result.Text)
			}
			if strings.Contains(strings.ToLower(result.Text), "transcript data only") || strings.Contains(strings.ToLower(result.Text), "\"transcript\"") {
				t.Fatalf("formatted output leaked its data wrapper: %q", result.Text)
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
