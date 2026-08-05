package dictation

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// LangfuseSink persists formatter traces before attempting any network I/O.
// A failed export leaves the event untouched for a later background retry.
type LangfuseSink struct {
	cfg       LangfuseConfig
	dir       string
	publicKey string
	secretKey string
	trigger   chan struct{}
	export    func(context.Context, langfuseFormatterEvent) error
}

type langfuseFormatterEvent struct {
	ID        string         `json:"id"`
	JobID     string         `json:"job_id"`
	CreatedAt time.Time      `json:"created_at"`
	Audit     FormattingInfo `json:"audit"`
	Output    string         `json:"output"`
}

func NewLangfuseSink(cfg Config) *LangfuseSink {
	if !cfg.Langfuse.Enabled {
		return nil
	}
	publicKey := strings.TrimSpace(os.Getenv(cfg.Langfuse.PublicKeyEnv))
	secretKey := strings.TrimSpace(os.Getenv(cfg.Langfuse.SecretKeyEnv))
	if publicKey == "" || secretKey == "" {
		return nil
	}
	if baseURL := strings.TrimSpace(os.Getenv("LANGFUSE_BASE_URL")); baseURL != "" {
		cfg.Langfuse.BaseURL = baseURL
	}
	l := &LangfuseSink{
		cfg:       cfg.Langfuse,
		dir:       cfg.LangfuseQueueDir(),
		publicKey: publicKey,
		secretKey: secretKey,
		trigger:   make(chan struct{}, 1),
	}
	l.export = l.exportEvent
	return l
}

func (l *LangfuseSink) QueueFormatter(job *Job) {
	if l == nil || !job.Formatting.Eligible {
		return
	}
	event := langfuseFormatterEvent{
		ID:        newLangfuseEventID(),
		JobID:     job.ID,
		CreatedAt: time.Now().UTC(),
		Audit:     job.Formatting,
		Output:    job.FinalText,
	}
	if err := l.queue(event); err != nil {
		return
	}
	select {
	case l.trigger <- struct{}{}:
	default:
	}
}

func newLangfuseEventID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

func (l *LangfuseSink) queue(event langfuseFormatterEvent) error {
	if err := os.MkdirAll(l.dir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", event.CreatedAt.UnixNano(), event.ID)
	path := filepath.Join(l.dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (l *LangfuseSink) Run(ctx context.Context) {
	l.drain(ctx)
	ticker := time.NewTicker(time.Duration(l.cfg.SyncIntervalSecs) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.trigger:
			l.drain(ctx)
		case <-ticker.C:
			l.drain(ctx)
		}
	}
}

func (l *LangfuseSink) drain(ctx context.Context) {
	entries, err := os.ReadDir(l.dir)
	if os.IsNotExist(err) || err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(l.dir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var event langfuseFormatterEvent
		if json.Unmarshal(b, &event) != nil {
			continue // retain malformed data for manual inspection rather than deleting it
		}
		exportCtx, cancel := context.WithTimeout(ctx, time.Duration(l.cfg.TimeoutSecs)*time.Second)
		err = l.export(exportCtx, event)
		cancel()
		if err != nil {
			return // offline or remote failure: preserve this and later events
		}
		_ = os.Remove(path)
	}
}

func (l *LangfuseSink) Cleanup(maxAge time.Duration) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(l.dir, entry.Name()))
		}
	}
}

func (l *LangfuseSink) exportEvent(ctx context.Context, event langfuseFormatterEvent) error {
	auth := base64.StdEncoding.EncodeToString([]byte(l.publicKey + ":" + l.secretKey))
	endpoint := strings.TrimRight(l.cfg.BaseURL, "/") + "/api/public/otel/v1/traces"
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization":                "Basic " + auth,
			"x-langfuse-ingestion-version": "4",
		}),
	)
	if err != nil {
		return err
	}
	defer exporter.Shutdown(context.Background())
	span, err := event.span()
	if err != nil {
		return err
	}
	return exporter.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span})
}

func (event langfuseFormatterEvent) span() (sdktrace.ReadOnlySpan, error) {
	traceID, spanID := trace.TraceID{}, trace.SpanID{}
	if _, err := rand.Read(traceID[:]); err != nil {
		return nil, err
	}
	if _, err := rand.Read(spanID[:]); err != nil {
		return nil, err
	}
	input, _ := json.Marshal(map[string]any{
		"text":          event.Audit.InputText,
		"system_prompt": event.Audit.SystemPrompt,
		"context_hint":  event.Audit.ContextHint,
	})
	output, _ := json.Marshal(map[string]any{
		"text":           event.Output,
		"raw_response":   event.Audit.RawResponse,
		"http_status":    event.Audit.HTTPStatus,
		"latency_ms":     event.Audit.LatencyMS,
		"formatter_skip": event.Audit.Skipped,
	})
	end := event.CreatedAt
	start := end.Add(-time.Duration(event.Audit.LatencyMS) * time.Millisecond)
	if !start.Before(end) {
		start = end.Add(-time.Millisecond)
	}
	return tracetest.SpanStub{
		Name:      "jflow.formatter",
		SpanKind:  trace.SpanKindInternal,
		StartTime: start,
		EndTime:   end,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
		}),
		Attributes: []attribute.KeyValue{
			attribute.String("langfuse.observation.type", "generation"),
			attribute.String("langfuse.observation.input", string(input)),
			attribute.String("langfuse.observation.output", string(output)),
			attribute.String("gen_ai.system", "ollama"),
			attribute.String("gen_ai.request.model", event.Audit.Model),
			attribute.String("gen_ai.response.model", event.Audit.Model),
			attribute.String("langfuse.observation.metadata.jflow_job_id", event.JobID),
			attribute.String("langfuse.observation.metadata.formatter_endpoint", event.Audit.Endpoint),
			attribute.Int("langfuse.observation.metadata.context_tokens", event.Audit.ContextTokens),
			attribute.Int("langfuse.observation.metadata.max_output_tokens", event.Audit.MaxOutput),
		},
		Resource: resource.NewWithAttributes("", attribute.String("service.name", "jflow")),
	}.Snapshot(), nil
}
