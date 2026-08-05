package dictation

import "time"

type JobStatus string

const (
	StatusRecording    JobStatus = "recording"
	StatusQueued       JobStatus = "queued"
	StatusTranscribing JobStatus = "transcribing"
	StatusCleaning     JobStatus = "cleaning"
	StatusFormatting   JobStatus = "formatting"
	StatusDelivering   JobStatus = "delivering"
	StatusRetryWait    JobStatus = "retry_wait"
	StatusDelivered    JobStatus = "delivered"
	StatusFailed       JobStatus = "failed"
	StatusCancelled    JobStatus = "cancelled"
)

type WindowTarget struct {
	Address string `json:"address,omitempty"`
	Class   string `json:"class,omitempty"`
	Title   string `json:"title,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

type FormattingInfo struct {
	Eligible        bool     `json:"eligible,omitempty"`
	Applied         bool     `json:"applied,omitempty"`
	Changed         bool     `json:"changed,omitempty"`
	ContextHint     string   `json:"context_hint,omitempty"`
	PreprocessRules []string `json:"preprocess_rules,omitempty"`
	InputText       string   `json:"input_text,omitempty"`
	SystemPrompt    string   `json:"system_prompt,omitempty"`
	Model           string   `json:"model,omitempty"`
	Endpoint        string   `json:"endpoint,omitempty"`
	ContextTokens   int      `json:"context_tokens,omitempty"`
	MaxOutput       int      `json:"max_output_tokens,omitempty"`
	HTTPStatus      int      `json:"http_status,omitempty"`
	LatencyMS       int64    `json:"latency_ms,omitempty"`
	RawResponse     string   `json:"raw_response,omitempty"`
	Skipped         string   `json:"skipped,omitempty"`
	// Feedback is a local user signal for reviewing formatter quality. It never
	// changes delivery behavior and is intentionally separate from Skipped.
	Feedback string `json:"feedback,omitempty"`
}

// ASRUsage records observable provider usage, not a guessed currency amount.
// Provider pricing and plan allowances change independently, so cloud seconds
// are the only honest, useful local cost-control metric.
type ASRUsage struct {
	Provider     string  `json:"provider,omitempty"`
	Model        string  `json:"model,omitempty"`
	Cloud        bool    `json:"cloud"`
	AudioSeconds float64 `json:"audio_seconds,omitempty"`
}

// FormatterDatasetEntry is a user-reviewed local example suitable for a
// manual local-model comparison. It never contains audio or credentials and
// is not uploaded by JFlow.
type FormatterDatasetEntry struct {
	JobID       string `json:"job_id"`
	Input       string `json:"input"`
	Expected    string `json:"expected"`
	ContextHint string `json:"context_hint,omitempty"`
	Feedback    string `json:"feedback"`
}

type FormatterBenchmarkCase struct {
	JobID     string `json:"job_id"`
	Output    string `json:"output,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type FormatterBenchmark struct {
	Model            string                   `json:"model"`
	Cases            []FormatterBenchmarkCase `json:"cases"`
	AverageLatencyMS int64                    `json:"average_latency_ms,omitempty"`
}

type Job struct {
	ID                string         `json:"id"`
	Status            JobStatus      `json:"status"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	AudioPath         string         `json:"audio_path"`
	Target            WindowTarget   `json:"target"`
	Transcript        string         `json:"transcript,omitempty"`
	FinalText         string         `json:"final_text,omitempty"`
	Error             string         `json:"error,omitempty"`
	Attempts          int            `json:"attempts"`
	NextAttemptAt     time.Time      `json:"next_attempt_at,omitempty"`
	DeliveredAt       time.Time      `json:"delivered_at,omitempty"`
	DeliveryAttempted bool           `json:"delivery_attempted"`
	ClipboardBackup   bool           `json:"clipboard_backup"`
	RecordingSeconds  float64        `json:"recording_seconds,omitempty"`
	Usage             ASRUsage       `json:"usage,omitempty"`
	Formatting        FormattingInfo `json:"formatting,omitempty"`
}

// VocabularyEntry is a user-owned canonical word or phrase. Canonical terms
// are sent to Scribe as keyterms; aliases are learned locally from corrections
// and never leave the machine.
type VocabularyEntry struct {
	ID        string    `json:"id"`
	Canonical string    `json:"canonical,omitempty"`
	Aliases   []string  `json:"aliases,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Legacy fields are read so existing heard-as entries migrate safely on
	// their next update. New entries never write them.
	Heard       string `json:"heard,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

type Status struct {
	Phase       string `json:"phase"`
	ActiveJobID string `json:"active_job_id,omitempty"`
	ActionJobID string `json:"action_job_id,omitempty"`
	Message     string `json:"message,omitempty"`
	CanCopy     bool   `json:"can_copy,omitempty"`
	CanRetry    bool   `json:"can_retry,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}
