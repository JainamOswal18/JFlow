package dictation

import "time"

type JobStatus string

const (
	StatusRecording    JobStatus = "recording"
	StatusQueued       JobStatus = "queued"
	StatusTranscribing JobStatus = "transcribing"
	StatusCleaning     JobStatus = "cleaning"
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
}

type Job struct {
	ID                string       `json:"id"`
	Status            JobStatus    `json:"status"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
	AudioPath         string       `json:"audio_path"`
	Target            WindowTarget `json:"target"`
	Transcript        string       `json:"transcript,omitempty"`
	FinalText         string       `json:"final_text,omitempty"`
	Error             string       `json:"error,omitempty"`
	Attempts          int          `json:"attempts"`
	NextAttemptAt     time.Time    `json:"next_attempt_at,omitempty"`
	DeliveredAt       time.Time    `json:"delivered_at,omitempty"`
	DeliveryAttempted bool         `json:"delivery_attempted"`
	ClipboardBackup   bool         `json:"clipboard_backup"`
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
