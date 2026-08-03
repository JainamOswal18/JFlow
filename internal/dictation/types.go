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

type Status struct {
	Phase       string `json:"phase"`
	ActiveJobID string `json:"active_job_id,omitempty"`
	ActionJobID string `json:"action_job_id,omitempty"`
	Message     string `json:"message,omitempty"`
	CanCopy     bool   `json:"can_copy,omitempty"`
	CanUndo     bool   `json:"can_undo,omitempty"`
	CanRetry    bool   `json:"can_retry,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}
