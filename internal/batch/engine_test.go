// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestRunFullSourcePreflightRejectsLaterItemBeforeAnyCall(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt", "b.txt")
	if err := os.WriteFile(job.Items[1].SourcePath, []byte("changed after planning"), 0o600); err != nil {
		t.Fatalf("change second input: %v", err)
	}
	executor := &fakeBatchExecutor{}
	store := newFakeBatchStore(job)

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), `input "b.txt" changed`) {
		t.Fatalf("Run error = %v, want second-item preflight failure", err)
	}
	if result.Attempted != 0 || len(executor.requests) != 0 || store.saves != 0 {
		t.Fatalf("preflight spent work: result=%+v calls=%d saves=%d", result, len(executor.requests), store.saves)
	}
}

func TestRunFullResultPreflightRejectsLaterCollisionBeforeAnyCall(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt", "b.txt")
	store := newFakeBatchStore(job)
	store.verifyErrAt = 2
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), `verify result for item "b.txt"`) {
		t.Fatalf("Run error = %v, want later destination collision", err)
	}
	if result.Attempted != 0 || len(executor.requests) != 0 || store.saves != 0 {
		t.Fatalf("result preflight spent work: result=%+v calls=%d saves=%d", result, len(executor.requests), store.saves)
	}
}

func TestRunRechecksResultDestinationImmediatelyBeforeAttempt(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	store := newFakeBatchStore(job)
	store.verifyErrAt = 2
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), "recheck result destination") {
		t.Fatalf("Run error = %v, want immediate destination collision", err)
	}
	if result.Attempted != 0 || len(executor.requests) != 0 || store.saves != 0 {
		t.Fatalf("destination recheck spent work: result=%+v calls=%d saves=%d", result, len(executor.requests), store.saves)
	}
}

func TestRunAttemptSaveFailureMakesNoProviderCall(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	executor := &fakeBatchExecutor{}
	store := newFakeBatchStore(job)
	store.failSaveAt = 1

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), "persist attempt") {
		t.Fatalf("Run error = %v, want attempt persistence failure", err)
	}
	if result.Attempted != 0 || len(executor.requests) != 0 {
		t.Fatalf("attempt save failure called provider: result=%+v calls=%d", result, len(executor.requests))
	}
	stored := store.snapshot()
	if stored.Items[0].Attempts != 0 || stored.Items[0].Status != StatusPending {
		t.Fatalf("durable item = %+v, want untouched pending item", stored.Items[0])
	}
}

func TestRunPersistsRunningAttemptBeforeCallingExecutor(t *testing.T) {
	job := engineJob(t, runner.ModelOnDevice, "a.txt")
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{beforeExecute: func(request ExecutionRequest) {
		stored := store.snapshot().Items[0]
		if stored.Status != StatusRunning || stored.Attempts != 1 || stored.CurrentRequestID != request.RequestID {
			t.Fatalf("durable item at provider call = %+v, request=%+v", stored, request)
		}
	}}

	if _, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, newFakeClock()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunRecoversCommittedResultAfterFinalSaveFailureWithoutRecall(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	executor := &fakeBatchExecutor{}
	store := newFakeBatchStore(job)
	store.failSaveAt = 2
	clock := newFakeClock()

	first, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, clock)
	if err == nil || !strings.Contains(err.Error(), "persist successful item") {
		t.Fatalf("first Run error = %v, want final save failure", err)
	}
	if first.Attempted != 1 || len(executor.requests) != 1 {
		t.Fatalf("first invocation = %+v, calls=%d", first, len(executor.requests))
	}
	stored := store.snapshot()
	if stored.Items[0].Status != StatusRunning || stored.Items[0].Attempts != 1 {
		t.Fatalf("stored item = %+v, want durable running attempt", stored.Items[0])
	}

	store.failSaveAt = 0
	second, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, clock)
	if err != nil {
		t.Fatalf("recovery Run: %v", err)
	}
	if second.Attempted != 0 || second.Succeeded != 1 || !second.Complete {
		t.Fatalf("recovery summary = %+v", second)
	}
	if len(executor.requests) != 1 {
		t.Fatalf("provider calls = %d, want original call only", len(executor.requests))
	}
	stored = store.snapshot()
	if stored.Items[0].Status != StatusSucceeded || stored.Items[0].Result == nil {
		t.Fatalf("recovered item = %+v, want succeeded result", stored.Items[0])
	}
	if want := clock.Now().Add(cloudCallSpacing); !stored.NextCallNotBefore.Equal(want) {
		t.Fatalf("next call = %s, want conservative recovered spacing %s", stored.NextCallNotBefore, want)
	}

	third, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, clock)
	if err != nil || third.Attempted != 0 || len(executor.requests) != 1 {
		t.Fatalf("repeat resume = %+v, err=%v, calls=%d; succeeded item was recalled", third, err, len(executor.requests))
	}
}

func TestRunInterruptedAttemptBecomesUncertainAndRequiresChoice(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	item := &job.Items[0]
	item.Attempts = 1
	item.CurrentRequestID = RequestID(job.ID, item.ID, 1)
	item.Status = StatusRunning
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, newFakeClock())
	if !errors.Is(err, ErrUncertainChoiceRequired) {
		t.Fatalf("Run error = %v, want ErrUncertainChoiceRequired", err)
	}
	if result.Uncertain != 1 || result.Pending != 0 || result.Attempted != 0 {
		t.Fatalf("summary = %+v", result)
	}
	if got := store.snapshot().Items[0].Status; got != StatusUncertain {
		t.Fatalf("stored status = %q, want uncertain", got)
	}
	if got := store.snapshot().NextCallNotBefore; !got.Equal(newFakeClock().Now().Add(cloudCallSpacing)) {
		t.Fatalf("recovery pacing = %s, want conservative restart spacing", got)
	}
	if len(executor.requests) != 0 {
		t.Fatalf("provider calls = %d, want zero", len(executor.requests))
	}
}

func TestRunExplicitUncertainRetryWaitsAndUsesNextAttempt(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	item := &job.Items[0]
	item.Attempts = 1
	item.CurrentRequestID = RequestID(job.ID, item.ID, 1)
	item.Status = StatusRunning
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{}
	clock := newFakeClock()

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1, RetryUncertain: true}, executor, store, clock)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Attempted != 1 || result.Succeeded != 1 || len(executor.requests) != 1 {
		t.Fatalf("summary=%+v requests=%+v", result, executor.requests)
	}
	if executor.requests[0].Attempt != 2 || len(clock.waits) != 1 || clock.waits[0] != cloudCallSpacing {
		t.Fatalf("request=%+v waits=%v, want attempt 2 after %s", executor.requests[0], clock.waits, cloudCallSpacing)
	}
}

func TestRunRefusesMissingSucceededResultWithoutRecall(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	item := &job.Items[0]
	item.Attempts = 1
	item.CurrentRequestID = RequestID(job.ID, item.ID, 1)
	digest := sha256.Sum256([]byte("missing"))
	checksum := hex.EncodeToString(digest[:])
	item.Result = &ResultRef{
		ID: ResultID(item.CurrentRequestID, checksum), RequestID: item.CurrentRequestID,
		Path: filepath.Join(job.OutputDir, item.ResultName), SHA256: checksum,
		Bytes: int64(len("missing")), ModelUsed: job.Model,
	}
	item.Status = StatusSucceeded
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), "committed result is missing") {
		t.Fatalf("Run error = %v, want missing result error", err)
	}
	if result.Attempted != 0 || len(executor.requests) != 0 {
		t.Fatalf("missing success was recalled: summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunBudgetIncludesExplicitRetry(t *testing.T) {
	job := engineJob(t, runner.ModelOnDevice, "a.txt", "b.txt")
	failed := &job.Items[0]
	failed.Attempts = 1
	failed.CurrentRequestID = RequestID(job.ID, failed.ID, 1)
	failed.Status = StatusFailed
	failed.Failure = "provider attempt failed"
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1, RetryFailed: true}, executor, store, newFakeClock())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Attempted != 1 || !result.BudgetExhausted || result.Succeeded != 1 || result.Pending != 1 {
		t.Fatalf("summary = %+v", result)
	}
	if len(executor.requests) != 1 || executor.requests[0].ItemID != failed.ID || executor.requests[0].Attempt != 2 {
		t.Fatalf("requests = %+v, want one explicit retry", executor.requests)
	}
}

func TestRunFailedItemRequiresExplicitRetryBeforePendingCalls(t *testing.T) {
	job := engineJob(t, runner.ModelOnDevice, "a.txt", "b.txt")
	failed := &job.Items[0]
	failed.Attempts = 1
	failed.CurrentRequestID = RequestID(job.ID, failed.ID, 1)
	failed.Status = StatusFailed
	failed.Failure = "provider attempt failed"
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if !errors.Is(err, ErrFailedChoiceRequired) {
		t.Fatalf("Run error = %v, want ErrFailedChoiceRequired", err)
	}
	if result.Attempted != 0 || result.Failed != 1 || result.Pending != 1 || len(executor.requests) != 0 {
		t.Fatalf("summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunPersistsAndHonorsPacing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   runner.Model
		spacing time.Duration
	}{
		{name: "cloud", model: runner.ModelCloud, spacing: 15 * time.Second},
		{name: "chatgpt", model: runner.ModelChatGPT, spacing: 15 * time.Second},
		{name: "cloud pro", model: runner.ModelCloudPro, spacing: 45 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := engineJob(t, tc.model, "a.txt", "b.txt")
			store := newFakeBatchStore(job)
			executor := &fakeBatchExecutor{}
			clock := newFakeClock()

			result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, clock)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Attempted != 2 || len(clock.waits) != 1 || clock.waits[0] != tc.spacing {
				t.Fatalf("summary=%+v waits=%v, want one %s wait", result, clock.waits, tc.spacing)
			}
			stored := store.snapshot()
			if want := clock.Now().Add(tc.spacing); !stored.NextCallNotBefore.Equal(want) {
				t.Fatalf("persisted next call = %s, want %s", stored.NextCallNotBefore, want)
			}
		})
	}
}

func TestRunHonorsPersistedPacingAcrossResume(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	clock := newFakeClock()
	job.NextCallNotBefore = clock.Now().Add(9 * time.Second)
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{}

	if _, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, clock); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(clock.waits) != 1 || clock.waits[0] != 9*time.Second {
		t.Fatalf("waits = %v, want persisted 9s wait", clock.waits)
	}
}

func TestRunCancellationDuringPacingStartsNoCalls(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt", "b.txt")
	clock := newFakeClock()
	job.NextCallNotBefore = clock.Now().Add(time.Minute)
	clock.waitErr = context.Canceled
	store := newFakeBatchStore(job)
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, clock)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context canceled", err)
	}
	if result.Attempted != 0 || len(executor.requests) != 0 {
		t.Fatalf("canceled pacing started calls: summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunStopsAfterFirstProviderErrorAndDoesNotAutoRetry(t *testing.T) {
	job := engineJob(t, runner.ModelOnDevice, "a.txt", "b.txt")
	executor := &fakeBatchExecutor{executeErr: errors.New("provider unavailable")}
	store := newFakeBatchStore(job)

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("Run error = %v, want provider failure", err)
	}
	if result.Attempted != 1 || result.Failed != 1 || result.Pending != 1 || len(executor.requests) != 1 {
		t.Fatalf("summary=%+v calls=%d", result, len(executor.requests))
	}
	if got := store.snapshot().Items[0].Attempts; got != 1 {
		t.Fatalf("attempts = %d, want exactly one", got)
	}
}

func TestRunTimeoutIsUncertainAndCancellationPreventsFutureCalls(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt", "b.txt")
	executor := &fakeBatchExecutor{executeErr: context.DeadlineExceeded}
	store := newFakeBatchStore(job)

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want deadline exceeded", err)
	}
	if result.Attempted != 1 || result.Uncertain != 1 || result.Pending != 1 || len(executor.requests) != 1 {
		t.Fatalf("summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunCancellationAfterCompletedCallStartsNoFutureCall(t *testing.T) {
	job := engineJob(t, runner.ModelOnDevice, "a.txt", "b.txt")
	ctx, cancel := context.WithCancel(t.Context())
	executor := &fakeBatchExecutor{afterExecute: func(ExecutionRequest) { cancel() }}
	store := newFakeBatchStore(job)

	result, err := Run(ctx, job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context canceled", err)
	}
	if result.Attempted != 1 || result.Succeeded != 1 || result.Pending != 1 || len(executor.requests) != 1 {
		t.Fatalf("summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunRejectsUnexpectedModelAndStops(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt", "b.txt")
	executor := &fakeBatchExecutor{modelUsed: runner.ModelOnDevice}
	store := newFakeBatchStore(job)

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), "instead of planned model") {
		t.Fatalf("Run error = %v, want model mismatch", err)
	}
	if result.Attempted != 1 || result.Failed != 1 || result.Pending != 1 || len(executor.requests) != 1 {
		t.Fatalf("summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunRevalidatesSourcesBeforeEveryCall(t *testing.T) {
	job := engineJob(t, runner.ModelOnDevice, "a.txt", "b.txt")
	executor := &fakeBatchExecutor{afterExecute: func(request ExecutionRequest) {
		if request.ItemID == job.Items[0].ID {
			if err := os.WriteFile(job.Items[1].SourcePath, []byte("changed"), 0o600); err != nil {
				t.Fatalf("change second input: %v", err)
			}
		}
	}}
	store := newFakeBatchStore(job)

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), `input "b.txt" changed`) {
		t.Fatalf("Run error = %v, want source change", err)
	}
	if result.Attempted != 1 || len(executor.requests) != 1 || result.Succeeded != 1 || result.Pending != 1 {
		t.Fatalf("summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunCommitFailurePersistsUncertainAndStops(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt", "b.txt")
	store := newFakeBatchStore(job)
	store.commitErr = errors.New("disk full")
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 2}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), "commit result") {
		t.Fatalf("Run error = %v, want commit failure", err)
	}
	if result.Attempted != 1 || result.Uncertain != 1 || result.Pending != 1 || len(executor.requests) != 1 {
		t.Fatalf("summary=%+v calls=%d", result, len(executor.requests))
	}
}

func TestRunLockFailureStartsNoWork(t *testing.T) {
	job := engineJob(t, runner.ModelCloud, "a.txt")
	store := newFakeBatchStore(job)
	store.lockErr = errors.New("job already locked")
	executor := &fakeBatchExecutor{}

	result, err := Run(t.Context(), job.JobPath, InvocationOptions{MaxCalls: 1}, executor, store, newFakeClock())
	if err == nil || !strings.Contains(err.Error(), "job already locked") {
		t.Fatalf("Run error = %v, want lock failure", err)
	}
	if result.Attempted != 0 || len(executor.requests) != 0 {
		t.Fatalf("lock failure started work: result=%+v calls=%d", result, len(executor.requests))
	}
}

func engineJob(t *testing.T, model runner.Model, names ...string) *Job {
	t.Helper()
	root := t.TempDir()
	input := filepath.Join(root, "input")
	output := filepath.Join(root, "output")
	if err := os.Mkdir(input, 0o700); err != nil {
		t.Fatalf("mkdir input: %v", err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(input, name), []byte("source "+name), 0o600); err != nil {
			t.Fatalf("write input: %v", err)
		}
	}
	instruction := filepath.Join(root, "instruction.txt")
	if err := os.WriteFile(instruction, []byte("Summarize this source."), 0o600); err != nil {
		t.Fatalf("write instruction: %v", err)
	}
	job, err := Plan(PlanOptions{
		InputDir: input, OutputDir: output, JobPath: filepath.Join(root, "job.json"),
		InstructionSource: instruction, Model: model, CreatedAt: time.Unix(1_000, 0),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return job
}

type fakeBatchExecutor struct {
	executeErr    error
	modelUsed     runner.Model
	beforeExecute func(ExecutionRequest)
	afterExecute  func(ExecutionRequest)
	requests      []ExecutionRequest
}

func (executor *fakeBatchExecutor) Execute(_ context.Context, request ExecutionRequest) (ExecutionOutput, error) {
	if executor.beforeExecute != nil {
		executor.beforeExecute(request)
	}
	executor.requests = append(executor.requests, request)
	if executor.afterExecute != nil {
		executor.afterExecute(request)
	}
	if executor.executeErr != nil {
		return ExecutionOutput{}, executor.executeErr
	}
	modelUsed := executor.modelUsed
	if modelUsed == "" {
		modelUsed = request.Model
	}
	return ExecutionOutput{Content: []byte("result:" + request.ItemID), ModelUsed: modelUsed}, nil
}

type fakeBatchClock struct {
	now     time.Time
	waits   []time.Duration
	waitErr error
}

func newFakeClock() *fakeBatchClock {
	return &fakeBatchClock{now: time.Unix(10_000, 0).UTC()}
}

func (clock *fakeBatchClock) Now() time.Time { return clock.now }

func (clock *fakeBatchClock) Wait(_ context.Context, duration time.Duration) error {
	clock.waits = append(clock.waits, duration)
	if clock.waitErr != nil {
		return clock.waitErr
	}
	clock.now = clock.now.Add(duration)
	return nil
}

type fakeBatchLock struct {
	store *fakeBatchStore
}

func (lock *fakeBatchLock) Unlock() error {
	lock.store.locked = false
	return lock.store.unlockErr
}

type fakeBatchStore struct {
	job         *Job
	results     map[string]ResultRef
	locked      bool
	lockErr     error
	unlockErr   error
	commitErr   error
	failSaveAt  int
	verifyErrAt int
	saves       int
	verifyCalls int
}

func newFakeBatchStore(job *Job) *fakeBatchStore {
	return &fakeBatchStore{job: cloneEngineJob(job), results: make(map[string]ResultRef)}
}

func (store *fakeBatchStore) Lock(_ context.Context, _ string) (Lock, error) {
	if store.lockErr != nil {
		return nil, store.lockErr
	}
	if store.locked {
		return nil, errors.New("already locked")
	}
	store.locked = true
	return &fakeBatchLock{store: store}, nil
}

func (store *fakeBatchStore) Load(_ context.Context, _ string) (*Job, error) {
	if !store.locked {
		return nil, errors.New("load without lock")
	}
	return cloneEngineJob(store.job), nil
}

func (store *fakeBatchStore) Save(_ context.Context, _ string, job *Job) error {
	if !store.locked {
		return errors.New("save without lock")
	}
	store.saves++
	if store.failSaveAt == store.saves {
		return errors.New("injected save failure")
	}
	store.job = cloneEngineJob(job)
	return nil
}

func (store *fakeBatchStore) CommitResult(_ context.Context, _ string, request ExecutionRequest, output ExecutionOutput) (ResultRef, error) {
	if !store.locked {
		return ResultRef{}, errors.New("commit without lock")
	}
	if store.commitErr != nil {
		return ResultRef{}, store.commitErr
	}
	digest := sha256.Sum256(output.Content)
	checksum := hex.EncodeToString(digest[:])
	item := store.item(request.ItemID)
	result := ResultRef{
		ID: ResultID(request.RequestID, checksum), RequestID: request.RequestID,
		Path: filepath.Join(store.job.OutputDir, item.ResultName), SHA256: checksum,
		Bytes: int64(len(output.Content)), ModelUsed: output.ModelUsed,
	}
	store.results[request.RequestID] = result
	return result, nil
}

func (store *fakeBatchStore) VerifyResult(_ context.Context, _ string, item Item) (ResultRef, bool, error) {
	if !store.locked {
		return ResultRef{}, false, errors.New("verify without lock")
	}
	store.verifyCalls++
	if store.verifyErrAt == store.verifyCalls {
		return ResultRef{}, false, errors.New("result destination collision")
	}
	result, found := store.results[item.CurrentRequestID]
	return result, found, nil
}

func (store *fakeBatchStore) snapshot() *Job { return cloneEngineJob(store.job) }

func (store *fakeBatchStore) item(id string) Item {
	for _, item := range store.job.Items {
		if item.ID == id {
			return item
		}
	}
	panic("unknown item " + id)
}

func cloneEngineJob(job *Job) *Job {
	if job == nil {
		return nil
	}
	copy := *job
	copy.Items = append([]Item(nil), job.Items...)
	copy.Skipped = append([]Skipped(nil), job.Skipped...)
	for index := range copy.Items {
		if job.Items[index].Result != nil {
			result := *job.Items[index].Result
			copy.Items[index].Result = &result
		}
	}
	return &copy
}
