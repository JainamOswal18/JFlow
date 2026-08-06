package dictation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise the complete durable release pipeline without a real
// microphone, cloud request, clipboard write, or desktop typing. The fake
// executables are deliberately reached through PATH, exactly as production
// reaches wtype and hyprctl.
func TestPipelineE2EFormatsAuditsDeliversAndAcceptsFeedback(t *testing.T) {
	server := pipelineServer(t, http.StatusOK)
	defer server.Close()
	d, cfg, cleanup := newPipelineDaemon(t, server.URL)
	defer cleanup()

	job := enqueuePipelineJob(t, d, cfg, 16)
	got := waitForJob(t, d.store, job.ID, StatusDelivered)
	if got.Transcript != "please help me with the issue and brainstorm it" {
		t.Fatalf("transcript = %q", got.Transcript)
	}
	if got.FinalText != "1. Help me with the issue.\n2. Brainstorm it." {
		t.Fatalf("final text = %q", got.FinalText)
	}
	if !got.Formatting.Eligible || !got.Formatting.Applied || got.Formatting.RawResponse == "" || got.Formatting.SystemPrompt == "" {
		t.Fatalf("incomplete formatter audit: %#v", got.Formatting)
	}
	if !got.Usage.Cloud || got.Usage.Provider != "elevenlabs_batch" || got.Usage.AudioSeconds != 16 {
		t.Fatalf("usage = %#v", got.Usage)
	}

	resp, err := SendCommand(cfg.SocketPath(), Command{Action: "formatter-feedback", JobID: job.ID, Text: "helpful"})
	if err != nil || !resp.OK {
		t.Fatalf("feedback response=%#v err=%v", resp, err)
	}
	got, err = d.store.Get(job.ID)
	if err != nil || got.Formatting.Feedback != "helpful" {
		t.Fatalf("feedback was not stored: job=%#v err=%v", got, err)
	}

	history, err := SendCommand(cfg.SocketPath(), Command{Action: "history"})
	if err != nil || !history.OK || len(history.Jobs) != 1 || history.Jobs[0].ID != job.ID {
		t.Fatalf("history response=%#v err=%v", history, err)
	}
	dataset, err := SendCommand(cfg.SocketPath(), Command{Action: "formatter-dataset"})
	if err != nil || !dataset.OK || len(dataset.Dataset) != 1 || dataset.Dataset[0].JobID != job.ID {
		t.Fatalf("dataset response=%#v err=%v", dataset, err)
	}
	benchmark, err := SendCommand(cfg.SocketPath(), Command{Action: "formatter-benchmark", Text: "qwen3:1.7b"})
	if err != nil || !benchmark.OK || len(benchmark.Benchmarks) != 1 || len(benchmark.Benchmarks[0].Cases) != 1 || benchmark.Benchmarks[0].Cases[0].Error != "" {
		t.Fatalf("benchmark response=%#v err=%v", benchmark, err)
	}
	inserted, err := os.ReadFile(os.Getenv("JFLOW_TEST_WTYPE_LOG"))
	if err != nil || !strings.Contains(string(inserted), "1. Help me with the issue.") || !strings.Contains(string(inserted), "2. Brainstorm it.") {
		t.Fatalf("wtype log=%q err=%v", inserted, err)
	}
}

// This locks in the user-selected formatter behavior: if local Qwen fails,
// JFlow delivers the original Scribe text. It does not perform a second model
// rewrite, retry the formatter, or issue a second transcription request.
func TestPipelineE2EFormatterFailureKeepsScribeText(t *testing.T) {
	server := pipelineServer(t, http.StatusServiceUnavailable)
	defer server.Close()
	d, cfg, cleanup := newPipelineDaemon(t, server.URL)
	defer cleanup()

	job := enqueuePipelineJob(t, d, cfg, 16)
	got := waitForJob(t, d.store, job.ID, StatusDelivered)
	if got.FinalText != got.Transcript {
		t.Fatalf("formatter failure changed text: transcript=%q final=%q", got.Transcript, got.FinalText)
	}
	if got.Formatting.Applied || got.Formatting.Skipped == "" || got.Formatting.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("formatter failure audit = %#v", got.Formatting)
	}
}

func TestPipelineE2ELinkedInFooterIsPersistedAndDeliveredLast(t *testing.T) {
	server := pipelineServer(t, http.StatusOK)
	defer server.Close()
	d, cfg, cleanup := newPipelineDaemon(t, server.URL)
	defer cleanup()

	target := WindowTarget{Address: "0x1", Class: "brave-browser", Title: "Feed | LinkedIn - Brave"}
	job := enqueuePipelineJobForTarget(t, d, cfg, 16, target, InferContextHint(target))
	got := waitForJob(t, d.store, job.ID, StatusDelivered)
	if !strings.HasSuffix(got.FinalText, "\n\n— Written using JFlow") {
		t.Fatalf("LinkedIn footer is not the final text: %q", got.FinalText)
	}
	inserted, err := os.ReadFile(os.Getenv("JFLOW_TEST_WTYPE_LOG"))
	if err != nil || !strings.Contains(string(inserted), "— Written using JFlow") {
		t.Fatalf("wtype log=%q err=%v", inserted, err)
	}
}

func TestPipelineE2EExplicitSelectionLearnsVocabulary(t *testing.T) {
	server := pipelineServer(t, http.StatusOK)
	defer server.Close()
	d, cfg, cleanup := newPipelineDaemon(t, server.URL)
	defer cleanup()
	t.Setenv("JFLOW_TEST_SELECTION", "Jainam Oswal")

	job := &Job{
		ID:         "e2e-selection",
		Status:     StatusDelivered,
		CreatedAt:  time.Now().UTC(),
		Transcript: "Hi, I'm Jay Nam Oswal",
		FinalText:  "Hi, I'm Jainam Oswal",
	}
	if err := d.store.Save(job); err != nil {
		t.Fatal(err)
	}
	resp, err := SendCommand(cfg.SocketPath(), Command{Action: "learn-selection"})
	if err != nil || !resp.OK {
		t.Fatalf("selection response=%#v err=%v", resp, err)
	}
	entries, err := d.vocabulary.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("vocabulary=%#v err=%v", entries, err)
	}
	if entries[0].Canonical != "Jainam Oswal" || len(entries[0].Aliases) != 1 || entries[0].Aliases[0] != "Jay Nam Oswal" {
		t.Fatalf("learned entry=%#v", entries[0])
	}
}

func pipelineServer(t *testing.T, formatterStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/speech-to-text":
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Fatalf("parse ASR form: %v", err)
			}
			if r.FormValue("model_id") != "scribe_v2" {
				t.Fatalf("ASR model = %q", r.FormValue("model_id"))
			}
			_, _ = w.Write([]byte(`{"text":"please help me with the issue and brainstorm it"}`))
		case "/api/generate": // daemon pre-warm
			_, _ = w.Write([]byte(`{}`))
		case "/api/chat":
			if formatterStatus != http.StatusOK {
				w.WriteHeader(formatterStatus)
				_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(`{"message":{"content":"{\"blocks\":[{\"type\":\"numbered\",\"items\":[\"Help me with the issue.\",\"Brainstorm it.\"]}]}"}}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
}

func newPipelineDaemon(t *testing.T, endpoint string) (*Daemon, Config, func()) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "wtype.log")
	writePipelineExecutable(t, filepath.Join(bin, "hyprctl"), "#!/bin/sh\nprintf '%s' '{\"address\":\"0x1\",\"class\":\"brave\",\"title\":\"ChatGPT\"}'\n")
	writePipelineExecutable(t, filepath.Join(bin, "wtype"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$JFLOW_TEST_WTYPE_LOG\"\n")
	writePipelineExecutable(t, filepath.Join(bin, "wl-paste"), "#!/bin/sh\nprintf '%s' \"$JFLOW_TEST_SELECTION\"\n")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("JFLOW_TEST_WTYPE_LOG", logPath)
	t.Setenv("ELEVENLABS_API_KEY", "test-key")

	cfg := DefaultConfig()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.StateDir = filepath.Join(dir, "state")
	cfg.RuntimeDir = filepath.Join(dir, "runtime")
	cfg.VocabularyFile = filepath.Join(dir, "vocabulary.json")
	cfg.ASR = ASRConfig{Provider: "elevenlabs_batch", APIKeyEnv: "ELEVENLABS_API_KEY", Language: "eng", Model: "scribe_v2", Endpoint: endpoint + "/v1/speech-to-text"}
	cfg.Cleanup.Enabled = false
	cfg.Formatter = FormatterConfig{Mode: "auto", Endpoint: endpoint, Model: "qwen3:1.7b", MinRecordingSecs: 15, TimeoutSecs: 2, KeepAlive: "1s", ContextTokens: 256, MaxOutputTokens: 64}
	cfg.Langfuse.Enabled = false
	cfg.Sound.Enabled = false
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(cfg.SocketPath()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return d, cfg, func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("daemon stopped with error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop")
		}
	}
}

func enqueuePipelineJob(t *testing.T, d *Daemon, cfg Config, seconds float64) *Job {
	return enqueuePipelineJobForTarget(t, d, cfg, seconds, WindowTarget{Address: "0x1", Class: "brave", Title: "ChatGPT"}, "Likely context: an AI-assistant request.")
}

func enqueuePipelineJobForTarget(t *testing.T, d *Daemon, cfg Config, seconds float64, target WindowTarget, hint string) *Job {
	t.Helper()
	job := &Job{ID: fmt.Sprintf("e2e-%d", time.Now().UnixNano()), Status: StatusQueued, CreatedAt: time.Now().UTC(), AudioPath: d.store.AudioPath(fmt.Sprintf("e2e-%d", time.Now().UnixNano())), Target: target, RecordingSeconds: seconds, Formatting: FormattingInfo{ContextHint: hint}}
	// Keep ID and audio path in the same durable job directory.
	job.AudioPath = d.store.AudioPath(job.ID)
	if err := os.MkdirAll(filepath.Dir(job.AudioPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job.AudioPath, []byte("fake-wav-audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.store.Save(job); err != nil {
		t.Fatal(err)
	}
	d.enqueue(job.ID)
	return job
}

func waitForJob(t *testing.T, store *Store, id string, wanted JobStatus) *Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := store.Get(id)
		if err == nil && job.Status == wanted {
			return job
		}
		time.Sleep(15 * time.Millisecond)
	}
	job, err := store.Get(id)
	t.Fatalf("job %s did not reach %s: job=%#v err=%v", id, wanted, job, err)
	return nil
}

func writePipelineExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
}
