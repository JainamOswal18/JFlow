package dictation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLangfuseQueuePersistsUntilExportSucceeds(t *testing.T) {
	dir := t.TempDir()
	sink := &LangfuseSink{
		cfg:     LangfuseConfig{TimeoutSecs: 1},
		dir:     dir,
		trigger: make(chan struct{}, 1),
	}
	event := langfuseFormatterEvent{
		ID: "event", JobID: "job", CreatedAt: time.Now().UTC(), Output: "formatted text",
		Audit: FormattingInfo{Eligible: true, Model: "qwen3:1.7b", InputText: "input", RawResponse: `{"message":"raw"}`},
	}
	if err := sink.queue(event); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("queue files = %v, err = %v", files, err)
	}
	if info, err := os.Stat(files[0]); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("queue file permissions = %v, err = %v", info.Mode(), err)
	}
	sink.export = func(context.Context, langfuseFormatterEvent) error { return errors.New("offline") }
	sink.drain(context.Background())
	files, _ = filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 1 {
		t.Fatalf("offline drain lost queued event: %v", files)
	}
	called := false
	sink.export = func(_ context.Context, got langfuseFormatterEvent) error {
		called = true
		if got.Audit.RawResponse != event.Audit.RawResponse || got.Output != event.Output {
			t.Fatalf("exported event = %#v", got)
		}
		return nil
	}
	sink.drain(context.Background())
	files, _ = filepath.Glob(filepath.Join(dir, "*.json"))
	if !called || len(files) != 0 {
		t.Fatalf("successful drain called=%v files=%v", called, files)
	}
}

func TestLangfuseSpanContainsFormatterAudit(t *testing.T) {
	event := langfuseFormatterEvent{
		ID: "event", JobID: "job", CreatedAt: time.Now().UTC(), Output: "formatted output",
		Audit: FormattingInfo{Model: "qwen3:1.7b", InputText: "input", SystemPrompt: "prompt", RawResponse: `{"message":"raw"}`, LatencyMS: 12},
	}
	span, err := event.span()
	if err != nil {
		t.Fatal(err)
	}
	if span.Name() != "jflow.formatter" || !span.SpanContext().IsValid() || span.EndTime().Before(span.StartTime()) {
		t.Fatalf("invalid Langfuse span: name=%q context=%v start=%v end=%v", span.Name(), span.SpanContext(), span.StartTime(), span.EndTime())
	}
}
