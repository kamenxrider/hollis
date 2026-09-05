// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package batch plans and executes finite, resumable folder-processing jobs.
package batch

import (
	"context"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

// SchemaVersion is the only manifest schema this release reads or writes.
const SchemaVersion = 1

// MaxCallsPerInvocation is the product ceiling for a single run or resume.
const MaxCallsPerInvocation = 100

// Status is an item's durable execution state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusUncertain Status = "uncertain"
)

// ItemKind identifies how an input is dispatched.
type ItemKind string

const (
	ItemText  ItemKind = "text"
	ItemImage ItemKind = "image"
)

// Job is the durable, inspectable description and recovery state of one batch.
// Paths are absolute and intentionally local metadata; prompt and result content
// are not embedded in the manifest.
type Job struct {
	SchemaVersion     int          `json:"schema_version"`
	ID                string       `json:"id"`
	CreatedAt         time.Time    `json:"created_at"`
	InputDir          string       `json:"input_dir"`
	OutputDir         string       `json:"output_dir"`
	JobPath           string       `json:"job_path"`
	InstructionSource string       `json:"instruction_source"`
	InstructionSHA256 string       `json:"instruction_sha256"`
	InstructionBytes  int64        `json:"instruction_bytes"`
	Model             runner.Model `json:"model"`
	NextCallNotBefore time.Time    `json:"next_call_not_before,omitzero"`
	Items             []Item       `json:"items"`
	Skipped           []Skipped    `json:"skipped"`
}

// Item is one immutable input and its mutable recovery state. Attempts is the
// lifetime count; invocation budgets are deliberately not persisted here.
type Item struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	SourcePath       string     `json:"source_path"`
	Kind             ItemKind   `json:"kind"`
	InputSHA256      string     `json:"input_sha256"`
	InputBytes       int64      `json:"input_bytes"`
	ResultName       string     `json:"result_name"`
	Status           Status     `json:"status"`
	Attempts         int        `json:"attempts"`
	CurrentRequestID string     `json:"current_request_id,omitempty"`
	Result           *ResultRef `json:"result,omitempty"`
	Failure          string     `json:"failure,omitempty"`
}

// Skipped records a non-batch entry instead of silently dropping it.
type Skipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ResultRef is the durable identity and checksum of a committed result.
type ResultRef struct {
	ID        string       `json:"id"`
	RequestID string       `json:"request_id"`
	Path      string       `json:"path"`
	SHA256    string       `json:"sha256"`
	Bytes     int64        `json:"bytes"`
	ModelUsed runner.Model `json:"model_used"`
}

// InvocationOptions controls one bounded run or resume. Failed and uncertain
// items are never retried unless their corresponding option is explicit.
type InvocationOptions struct {
	MaxCalls       int
	RetryFailed    bool
	RetryUncertain bool
	SkipUncertain  bool
}

// ExecutionRequest fully identifies one provider attempt. Attempt is
// one-based and RequestID changes for every explicit retry.
type ExecutionRequest struct {
	JobID             string
	ItemID            string
	Attempt           int
	RequestID         string
	Model             runner.Model
	Kind              ItemKind
	SourcePath        string
	InputSHA256       string
	InstructionSource string
	InstructionSHA256 string
}

// ExecutionOutput is returned privately to the store before an item may be
// marked succeeded in the manifest.
type ExecutionOutput struct {
	Content   []byte
	ModelUsed runner.Model
}

// Executor performs exactly one model request. It must not retry or persist.
type Executor interface {
	Execute(context.Context, ExecutionRequest) (ExecutionOutput, error)
}

// Clock makes provider pacing deterministic and cancellation-testable.
type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

// Lock holds exclusive ownership of one job until Unlock is called.
type Lock interface {
	Unlock() error
}

// Store owns private atomic manifest/result persistence and crash recovery.
type Store interface {
	// Every string argument below is the manifest's jobPath. VerifyResult
	// returns found=false with a nil error when no committed evidence exists;
	// malformed, mismatched, or corrupt evidence is an error.
	Lock(ctx context.Context, jobPath string) (Lock, error)
	Load(ctx context.Context, jobPath string) (*Job, error)
	Save(ctx context.Context, jobPath string, job *Job) error
	CommitResult(ctx context.Context, jobPath string, request ExecutionRequest, output ExecutionOutput) (ResultRef, error)
	VerifyResult(ctx context.Context, jobPath string, item Item) (result ResultRef, found bool, err error)
}
