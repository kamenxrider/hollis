// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

const (
	cloudCallSpacing    = 15 * time.Second
	cloudProCallSpacing = 45 * time.Second
)

var (
	// ErrFailedChoiceRequired prevents a resume from silently passing a known
	// failed item when the command has no explicit skip-failed operation.
	ErrFailedChoiceRequired = errors.New("failed items require retry failed")
	// ErrUncertainChoiceRequired prevents a resume from silently treating an
	// ambiguous provider attempt as pending work.
	ErrUncertainChoiceRequired = errors.New("uncertain items require either retry uncertain or skip uncertain")
)

// InvocationResult is the compact job summary returned to CLI callers. The
// status counts describe the whole job; Attempted counts provider calls made by
// this invocation only.
type InvocationResult struct {
	Attempted       int  `json:"attempted"`
	TotalAttempts   int  `json:"total_attempts"`
	Pending         int  `json:"pending"`
	Succeeded       int  `json:"succeeded"`
	Failed          int  `json:"failed"`
	Uncertain       int  `json:"uncertain"`
	Complete        bool `json:"complete"`
	BudgetExhausted bool `json:"budget_exhausted"`
}

// Run executes one bounded, exclusively locked batch invocation. It performs
// no automatic retries and stops after the first call or persistence error.
func Run(ctx context.Context, jobPath string, options InvocationOptions, executor Executor, store Store, clock Clock) (result InvocationResult, err error) {
	if err := options.Validate(); err != nil {
		return InvocationResult{}, err
	}
	if executor == nil {
		return InvocationResult{}, errors.New("batch executor is required")
	}
	if store == nil {
		return InvocationResult{}, errors.New("batch store is required")
	}
	if clock == nil {
		return InvocationResult{}, errors.New("batch clock is required")
	}
	if err := ctx.Err(); err != nil {
		return InvocationResult{}, err
	}

	lock, err := store.Lock(ctx, jobPath)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("lock batch job: %w", err)
	}
	if lock == nil {
		return InvocationResult{}, errors.New("lock batch job: store returned a nil lock")
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("unlock batch job: %w", unlockErr))
		}
	}()

	job, err := store.Load(ctx, jobPath)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("load batch job: %w", err)
	}
	if err := ValidateJob(job); err != nil {
		return summarize(job, result.Attempted, false), fmt.Errorf("validate batch job: %w", err)
	}

	if err := reconcileInterrupted(ctx, store, clock, jobPath, job); err != nil {
		return summarize(job, result.Attempted, false), err
	}
	if err := RevalidateSources(job); err != nil {
		return summarize(job, result.Attempted, false), fmt.Errorf("preflight batch sources: %w", err)
	}
	if hasFailed(job) && !options.RetryFailed {
		return summarize(job, result.Attempted, false), ErrFailedChoiceRequired
	}
	if hasUncertain(job) && !options.RetryUncertain && !options.SkipUncertain {
		return summarize(job, result.Attempted, false), ErrUncertainChoiceRequired
	}

	for index := range job.Items {
		item := &job.Items[index]
		if !eligible(*item, options) {
			continue
		}
		if result.Attempted == options.MaxCalls {
			return summarize(job, result.Attempted, true), nil
		}
		if err := ctx.Err(); err != nil {
			return summarize(job, result.Attempted, false), err
		}
		if err := waitForPacing(ctx, clock, job.NextCallNotBefore); err != nil {
			return summarize(job, result.Attempted, false), fmt.Errorf("wait for batch pacing: %w", err)
		}
		if err := RevalidateItem(job, *item); err != nil {
			return summarize(job, result.Attempted, false), fmt.Errorf("revalidate batch item %q: %w", item.Name, err)
		}
		if _, found, err := store.VerifyResult(ctx, jobPath, *item); err != nil {
			return summarize(job, result.Attempted, false), fmt.Errorf("recheck result destination for item %q: %w", item.Name, err)
		} else if found {
			return summarize(job, result.Attempted, false), fmt.Errorf("recheck result destination for item %q: unexpected committed result", item.Name)
		}

		request := nextRequest(job, *item)
		item.Attempts = request.Attempt
		item.CurrentRequestID = request.RequestID
		item.Status = StatusRunning
		item.Result = nil
		item.Failure = ""
		if err := store.Save(ctx, jobPath, job); err != nil {
			return summarize(job, result.Attempted, false), fmt.Errorf("persist attempt for %q: %w", item.Name, err)
		}

		result.Attempted++
		output, executeErr := executor.Execute(ctx, request)
		completedAt := clock.Now()
		job.NextCallNotBefore = completedAt.Add(spacingFor(job.Model))
		persistCtx := context.WithoutCancel(ctx)
		if executeErr == nil && output.ModelUsed != job.Model {
			executeErr = fmt.Errorf("executor used model %q instead of planned model %q", output.ModelUsed, job.Model)
		}
		if executeErr != nil {
			if ambiguousExecution(ctx, executeErr) {
				item.Status = StatusUncertain
				item.Failure = "provider attempt ended without a verifiable result"
			} else {
				item.Status = StatusFailed
				item.Failure = failureLabel(executeErr)
			}
			if saveErr := store.Save(persistCtx, jobPath, job); saveErr != nil {
				return summarize(job, result.Attempted, false), errors.Join(
					fmt.Errorf("execute item %q: %w", item.Name, executeErr),
					fmt.Errorf("persist failed attempt for %q: %w", item.Name, saveErr),
				)
			}
			return summarize(job, result.Attempted, false), fmt.Errorf("execute item %q: %w", item.Name, executeErr)
		}

		committed, commitErr := store.CommitResult(persistCtx, jobPath, request, output)
		if commitErr != nil {
			item.Status = StatusUncertain
			item.Failure = "provider returned output but result commit was not confirmed"
			if saveErr := store.Save(persistCtx, jobPath, job); saveErr != nil {
				return summarize(job, result.Attempted, false), errors.Join(
					fmt.Errorf("commit result for %q: %w", item.Name, commitErr),
					fmt.Errorf("persist uncertain attempt for %q: %w", item.Name, saveErr),
				)
			}
			return summarize(job, result.Attempted, false), fmt.Errorf("commit result for %q: %w", item.Name, commitErr)
		}

		item.Result = &committed
		item.Status = StatusSucceeded
		item.Failure = ""
		if err := store.Save(persistCtx, jobPath, job); err != nil {
			// The committed result remains recoverable while the durable manifest
			// still says running. A later Run verifies it instead of calling again.
			item.Result = nil
			item.Status = StatusRunning
			return summarize(job, result.Attempted, false), fmt.Errorf("persist successful item %q: %w", item.Name, err)
		}
	}

	return summarize(job, result.Attempted, false), nil
}

func reconcileInterrupted(ctx context.Context, store Store, clock Clock, jobPath string, job *Job) error {
	for index := range job.Items {
		item := &job.Items[index]
		result, found, err := store.VerifyResult(ctx, jobPath, *item)
		if err != nil {
			return fmt.Errorf("verify result for item %q: %w", item.Name, err)
		}
		switch item.Status {
		case StatusSucceeded:
			if !found {
				return fmt.Errorf("verify succeeded item %q: committed result is missing", item.Name)
			}
			if item.Result == nil || result != *item.Result {
				return fmt.Errorf("verify succeeded item %q: committed result identity changed", item.Name)
			}
			continue
		case StatusPending, StatusFailed:
			if found {
				return fmt.Errorf("verify result destination for item %q: unexpected committed result", item.Name)
			}
			continue
		}
		if found {
			item.Result = &result
			item.Status = StatusSucceeded
			item.Failure = ""
		} else if item.Status == StatusRunning {
			item.Status = StatusUncertain
			item.Failure = "interrupted provider attempt has no verifiable committed result"
		} else {
			// An older or partially persisted uncertain state may not contain a
			// pacing checkpoint. Restart the conservative interval before an
			// explicit retry rather than risking an adjacent provider call.
		}
		next := clock.Now().Add(spacingFor(job.Model))
		if next.After(job.NextCallNotBefore) {
			job.NextCallNotBefore = next
		}
		if err := store.Save(ctx, jobPath, job); err != nil {
			return fmt.Errorf("persist recovery for %q: %w", item.Name, err)
		}
	}
	return nil
}

func nextRequest(job *Job, item Item) ExecutionRequest {
	attempt := item.Attempts + 1
	return ExecutionRequest{
		JobID:             job.ID,
		ItemID:            item.ID,
		Attempt:           attempt,
		RequestID:         RequestID(job.ID, item.ID, attempt),
		Model:             job.Model,
		Kind:              item.Kind,
		SourcePath:        item.SourcePath,
		InputSHA256:       item.InputSHA256,
		InstructionSource: job.InstructionSource,
		InstructionSHA256: job.InstructionSHA256,
	}
}

func eligible(item Item, options InvocationOptions) bool {
	switch item.Status {
	case StatusPending:
		return true
	case StatusFailed:
		return options.RetryFailed
	case StatusUncertain:
		return options.RetryUncertain
	default:
		return false
	}
}

func hasUncertain(job *Job) bool {
	for _, item := range job.Items {
		if item.Status == StatusUncertain {
			return true
		}
	}
	return false
}

func hasFailed(job *Job) bool {
	for _, item := range job.Items {
		if item.Status == StatusFailed {
			return true
		}
	}
	return false
}

func waitForPacing(ctx context.Context, clock Clock, notBefore time.Time) error {
	if wait := notBefore.Sub(clock.Now()); wait > 0 {
		return clock.Wait(ctx, wait)
	}
	return nil
}

func spacingFor(model runner.Model) time.Duration {
	switch model {
	case runner.ModelCloudPro:
		return cloudProCallSpacing
	case runner.ModelCloud, runner.ModelChatGPT:
		return cloudCallSpacing
	default:
		return 0
	}
}

func ambiguousExecution(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var transportErr *runner.Error
	if !errors.As(err, &transportErr) {
		return false
	}
	switch transportErr.Kind {
	case runner.KindContextCanceled, runner.KindTimeout, runner.KindSignal, runner.KindSIGABRT:
		return true
	default:
		return false
	}
}

func failureLabel(err error) string {
	var transportErr *runner.Error
	if errors.As(err, &transportErr) {
		return "provider attempt failed: " + string(transportErr.Kind)
	}
	return "provider attempt failed"
}

func summarize(job *Job, attempted int, budgetExhausted bool) InvocationResult {
	result := InvocationResult{Attempted: attempted, BudgetExhausted: budgetExhausted}
	if job == nil {
		return result
	}
	for _, item := range job.Items {
		result.TotalAttempts += item.Attempts
		switch item.Status {
		case StatusPending:
			result.Pending++
		case StatusRunning:
			// A running manifest is always ambiguous across an invocation
			// boundary, even if the current process has just seen a save fail.
			result.Uncertain++
		case StatusSucceeded:
			result.Succeeded++
		case StatusFailed:
			result.Failed++
		case StatusUncertain:
			result.Uncertain++
		}
	}
	result.Complete = result.Pending == 0 && result.Failed == 0 && result.Uncertain == 0
	return result
}
