// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

const (
	batchUsageModel = "cloud, cloud-pro, on-device, or chatgpt"
	batchPlanUsage  = "hollis batch plan --input-dir <dir> --prompt-file <file> --model <tier> --output-dir <dir> --job <file>"
)

// batchJobStore is the durable batch store plus the no-clobber creation
// operation used by plan. Keeping this small interface here lets CLI tests
// use a provider-free fake while LocalStore owns production persistence.
type batchJobStore interface {
	batch.Store
	Create(context.Context, string, *batch.Job) error
}

var newBatchStore = func() batchJobStore { return batch.NewLocalStore() }

// realBatchClock uses a cancellable timer for pacing. A sleep would delay
// cancellation until the full provider interval had elapsed.
type realBatchClock struct{}

func (realBatchClock) Now() time.Time { return time.Now() }

func (realBatchClock) Wait(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// newBatchCmd constructs the batch command tree. Root registration and the
// agent-context command are deliberately owned by the coordinator.
func newBatchCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "batch",
		Short: "Plan and run a finite, resumable input folder",
		Long: `Plan and process a finite, nonrecursive folder of text and image files.

plan makes an inspectable job without calling a model. run and resume require
an explicit per-invocation --max-calls budget (1 through 100). A concrete
model is recorded in the plan; batch jobs never use automatic fallback.

Use --retry-failed or --retry-uncertain only when you explicitly intend to
repeat an item. Use --skip-uncertain to retain the evidence without retrying
an ambiguous provider attempt.`,
		Args: noExtraArgs("batch"),
	}
	cmd.AddCommand(newBatchPlanCmd(flags), newBatchInvokeCmd(flags, newRunner, false), newBatchInvokeCmd(flags, newRunner, true))
	return cmd
}

func newBatchPlanCmd(flags *rootFlags) *cobra.Command {
	var options batch.PlanOptions
	var model string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Create a no-call batch job from an input folder",
		Long: `Create a deterministic, sorted, nonrecursive batch manifest.

Only direct regular .txt, .md, .png, .jpg, and .jpeg entries are inventory
items. Unsupported entries are recorded as skipped. The destination job file
is created without overwriting an existing file.`,
		Example: "  " + batchPlanUsage,
		Args:    noExtraArgs("batch plan"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("model") || strings.TrimSpace(model) == "" {
				return usageErr(errors.New("batch plan requires an explicit --model; auto is unavailable"))
			}
			selected := runner.Model(model)
			if selected == runner.ModelAuto {
				return usageErr(errors.New("batch plan requires an explicit concrete --model; auto is unavailable"))
			}
			if !selected.Valid() {
				return usageErr(fmt.Errorf("unknown batch model %q: choose %s", model, batchUsageModel))
			}
			options.Model = selected
			job, err := batch.Plan(options)
			if err != nil {
				return usageErr(err)
			}
			store := newBatchStore()
			if store == nil {
				return configErr(errors.New("batch store is unavailable"))
			}
			if err := store.Create(cmd.Context(), job.JobPath, job); err != nil {
				return configErr(fmt.Errorf("create batch job: %w", err))
			}
			return printBatchPlan(cmd, flags, job)
		},
	}
	cmd.Flags().StringVar(&options.InputDir, "input-dir", "", "Input folder (nonrecursive)")
	cmd.Flags().StringVar(&options.InstructionSource, "prompt-file", "", "Instruction text file")
	cmd.Flags().StringVar(&model, "model", "", "Concrete model tier: cloud, cloud-pro, on-device, or chatgpt")
	cmd.Flags().StringVar(&options.OutputDir, "output-dir", "", "Private result folder")
	cmd.Flags().StringVar(&options.JobPath, "job", "", "Manifest path to create")
	return cmd
}

func newBatchInvokeCmd(flags *rootFlags, newRunner newRunnerFunc, resume bool) *cobra.Command {
	var (
		jobPath        string
		maxCalls       int
		retryFailed    bool
		retryUncertain bool
		skipUncertain  bool
	)
	name := "run"
	short := "Process pending batch items within an explicit call budget"
	if resume {
		name = "resume"
		short = "Resume a batch, skipping verified successes"
	}
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Long: `Process one item at a time from a stored batch job.

--max-calls is required for every invocation and counts provider attempts,
including explicit retries. The command stops at the first provider,
persistence, timeout, or cancellation error.`,
		Example: fmt.Sprintf("  hollis batch %s --job ./job.json --max-calls 12", name),
		Args:    noExtraArgs("batch " + name),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("max-calls") {
				return usageErr(errors.New("--max-calls is required for every batch run or resume invocation"))
			}
			options := batch.InvocationOptions{
				MaxCalls:       maxCalls,
				RetryFailed:    retryFailed,
				RetryUncertain: retryUncertain,
				SkipUncertain:  skipUncertain,
			}
			if err := options.Validate(); err != nil {
				return usageErr(err)
			}
			if strings.TrimSpace(jobPath) == "" {
				return usageErr(errors.New("--job is required"))
			}
			store := newBatchStore()
			if store == nil {
				return configErr(errors.New("batch store is unavailable"))
			}
			executor := &hollisBatchExecutor{newRunner: newRunner}
			result, err := batch.Run(cmd.Context(), jobPath, options, executor, store, realBatchClock{})
			if err != nil {
				return batchRunCLIError(err)
			}
			return printBatchResult(cmd, flags, jobPath, result)
		},
	}
	cmd.Flags().StringVar(&jobPath, "job", "", "Batch manifest path")
	cmd.Flags().IntVar(&maxCalls, "max-calls", 0, "Required provider-attempt budget for this invocation (1-100)")
	cmd.Flags().BoolVar(&retryFailed, "retry-failed", false, "Explicitly retry items recorded as failed")
	cmd.Flags().BoolVar(&retryUncertain, "retry-uncertain", false, "Explicitly retry items whose provider outcome is uncertain")
	cmd.Flags().BoolVar(&skipUncertain, "skip-uncertain", false, "Explicitly skip uncertain items without retrying them")
	return cmd
}

func printBatchPlan(cmd *cobra.Command, flags *rootFlags, job *batch.Job) error {
	data := map[string]any{
		"job_path":           job.JobPath,
		"schema_version":     job.SchemaVersion,
		"model":              string(job.Model),
		"input_dir":          job.InputDir,
		"output_dir":         job.OutputDir,
		"instruction_source": job.InstructionSource,
		"instruction_sha256": job.InstructionSHA256,
		"items":              job.Items,
		"item_count":         len(job.Items),
		"skipped":            job.Skipped,
		"skipped_count":      len(job.Skipped),
	}
	if flags.asJSON {
		return printJSONFilteredTo(cmd.OutOrStdout(), data, flags)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "planned batch job %s\n", job.JobPath)
	fmt.Fprintf(cmd.OutOrStdout(), "  model: %s\n", job.Model)
	fmt.Fprintf(cmd.OutOrStdout(), "  items: %d\n", len(job.Items))
	for _, item := range job.Items {
		fmt.Fprintf(cmd.OutOrStdout(), "    %s (%s)\n", item.Name, item.Kind)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  skipped: %d\n", len(job.Skipped))
	for _, skipped := range job.Skipped {
		fmt.Fprintf(cmd.OutOrStdout(), "    %s: %s\n", skipped.Name, skipped.Reason)
	}
	return nil
}

func printBatchResult(cmd *cobra.Command, flags *rootFlags, jobPath string, result batch.InvocationResult) error {
	data := map[string]any{
		"job_path":         jobPath,
		"attempted":        result.Attempted,
		"total_attempts":   result.TotalAttempts,
		"pending":          result.Pending,
		"succeeded":        result.Succeeded,
		"failed":           result.Failed,
		"uncertain":        result.Uncertain,
		"complete":         result.Complete,
		"budget_exhausted": result.BudgetExhausted,
	}
	if flags.asJSON {
		return printJSONFilteredTo(cmd.OutOrStdout(), data, flags)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "batch job %s\n", jobPath)
	fmt.Fprintf(cmd.OutOrStdout(), "  attempted: %d\n", result.Attempted)
	fmt.Fprintf(cmd.OutOrStdout(), "  total attempts: %d\n", result.TotalAttempts)
	fmt.Fprintf(cmd.OutOrStdout(), "  succeeded: %d\n", result.Succeeded)
	fmt.Fprintf(cmd.OutOrStdout(), "  pending: %d\n", result.Pending)
	fmt.Fprintf(cmd.OutOrStdout(), "  failed: %d\n", result.Failed)
	fmt.Fprintf(cmd.OutOrStdout(), "  uncertain: %d\n", result.Uncertain)
	if result.BudgetExhausted {
		fmt.Fprintln(cmd.OutOrStdout(), "  status: paused at call budget; resume with a new --max-calls budget")
	} else if result.Complete {
		fmt.Fprintln(cmd.OutOrStdout(), "  status: complete")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "  status: paused; unresolved items remain")
	}
	return nil
}

// batchRunCLIError preserves the existing exit-code conventions while
// distinguishing local manifest/store problems from provider transport
// failures. The batch package intentionally keeps its contracts provider
// neutral, so this boundary classifies its phase labels.
func batchRunCLIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutErr(err)
	}
	if errors.Is(err, context.Canceled) {
		return transportErr(err)
	}
	var classified *cliError
	if errors.As(err, &classified) {
		return err
	}
	var providerErr *runner.Error
	if errors.As(err, &providerErr) {
		return toCLIError(err)
	}
	message := err.Error()
	for _, prefix := range []string{
		"lock batch job:", "load batch job:", "persist ", "commit result", "verify interrupted", "unlock batch job:",
	} {
		if strings.Contains(message, prefix) {
			return configErr(err)
		}
	}
	return usageErr(err)
}

// hollisBatchExecutor is the in-process adapter from durable batch requests
// to the existing runner and bridge-resolution path. It deliberately reads
// source bytes only after the engine has completed its whole-job preflight,
// then verifies the exact bytes it is about to dispatch against the recorded
// hashes in the request.
type hollisBatchExecutor struct {
	newRunner newRunnerFunc
}

func (e *hollisBatchExecutor) Execute(ctx context.Context, request batch.ExecutionRequest) (batch.ExecutionOutput, error) {
	if err := ctx.Err(); err != nil {
		return batch.ExecutionOutput{}, err
	}
	if e == nil || e.newRunner == nil {
		return batch.ExecutionOutput{}, errors.New("batch runner is unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, runner.MaxTimeout)
	defer cancel()
	r := e.newRunner()
	if r == nil {
		return batch.ExecutionOutput{}, errors.New("batch runner is unavailable")
	}
	resolved, err := resolveForRunner(callCtx, func() runner.Runner { return r })
	if err != nil && !canAttemptAfterDiscoveryFailure(resolved, request.Model) {
		return batch.ExecutionOutput{}, resolutionCLIError(err)
	}
	if err := checkModelAvailable(resolved, request.Model); err != nil {
		return batch.ExecutionOutput{}, err
	}
	applyResolvedRefs(r, resolved)

	instruction, err := docinput.ReadPromptFile(request.InstructionSource, batch.DefaultMaxInstructionBytes)
	if err != nil {
		return batch.ExecutionOutput{}, fmt.Errorf("read batch instruction: %w", err)
	}
	if digestBytes([]byte(instruction)) != request.InstructionSHA256 {
		return batch.ExecutionOutput{}, errors.New("instruction source changed since planning")
	}

	switch request.Kind {
	case batch.ItemText:
		document, err := docinput.ReadDocument(request.SourcePath, batch.DefaultMaxTextInputBytes)
		if err != nil {
			return batch.ExecutionOutput{}, fmt.Errorf("read batch input: %w", err)
		}
		if digestBytes([]byte(document.Text)) != request.InputSHA256 {
			return batch.ExecutionOutput{}, errors.New("batch input changed since planning")
		}
		prompt, err := docinput.Prepare(instruction, []docinput.Document{document}, batch.MaxPreparedPromptBytes)
		if err != nil {
			return batch.ExecutionOutput{}, fmt.Errorf("prepare batch prompt: %w", err)
		}
		text, used, err := r.Run(callCtx, request.Model, prompt)
		if err != nil {
			return batch.ExecutionOutput{}, err
		}
		if used != request.Model {
			return batch.ExecutionOutput{}, fmt.Errorf("runner used model %q instead of requested %q", used, request.Model)
		}
		return batch.ExecutionOutput{Content: []byte(text), ModelUsed: used}, nil

	case batch.ItemImage:
		staged, cleanup, err := stageBatchImage(request.SourcePath, request.InputSHA256)
		if err != nil {
			return batch.ExecutionOutput{}, err
		}
		defer cleanup()
		imageRunner, ok := r.(runner.ImageRunner)
		if !ok {
			return batch.ExecutionOutput{}, errors.New("configured runner does not support image input")
		}
		text, used, err := imageRunner.RunWithImages(callCtx, request.Model, instruction, []string{staged})
		if err != nil {
			return batch.ExecutionOutput{}, err
		}
		if used != request.Model {
			return batch.ExecutionOutput{}, fmt.Errorf("runner used model %q instead of requested %q", used, request.Model)
		}
		return batch.ExecutionOutput{Content: []byte(text), ModelUsed: used}, nil
	default:
		return batch.ExecutionOutput{}, fmt.Errorf("unsupported batch item kind %q", request.Kind)
	}
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// stageBatchImage copies one verified image into a private, short-lived
// directory. O_NONBLOCK prevents a raced FIFO replacement from hanging, and
// O_NOFOLLOW prevents a raced symlink from redirecting the read.
func stageBatchImage(path, expectedHash string) (string, func(), error) {
	label := filepath.Base(path)
	before, err := os.Lstat(path)
	if err != nil {
		return "", func() {}, fmt.Errorf("inspect image %q: %w", label, batchFileError(err))
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return "", func() {}, fmt.Errorf("image %q must be a direct regular file", label)
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", func() {}, fmt.Errorf("open image %q: %w", label, batchFileError(err))
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", func() {}, errors.New("validate opened image failed")
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return "", func() {}, fmt.Errorf("image %q must be a direct regular file", label)
	}

	stageDir, err := os.MkdirTemp("", "hollis-batch-images-")
	if err != nil {
		_ = file.Close()
		return "", func() {}, errors.New("create private image staging directory failed")
	}
	cleanup := func() { _ = os.RemoveAll(stageDir) }
	if err := os.Chmod(stageDir, 0o700); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, errors.New("protect private image staging directory failed")
	}
	ext := strings.ToLower(filepath.Ext(label))
	stagedPath := filepath.Join(stageDir, "input"+ext)
	staged, err := os.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, errors.New("create private image staging file failed")
	}
	if err := staged.Chmod(0o600); err != nil {
		_ = staged.Close()
		_ = file.Close()
		cleanup()
		return "", func() {}, errors.New("protect private image staging file failed")
	}
	hash := sha256.New()
	limited := io.LimitReader(file, batch.DefaultMaxImageInputBytes+1)
	read, copyErr := io.Copy(io.MultiWriter(hash, staged), limited)
	var probe [1]byte
	extra, probeErr := file.Read(probe[:])
	closeStageErr := staged.Close()
	closeSourceErr := file.Close()
	if copyErr != nil || probeErr != nil && !errors.Is(probeErr, io.EOF) || closeStageErr != nil || closeSourceErr != nil {
		cleanup()
		return "", func() {}, errors.New("read image for batch dispatch failed")
	}
	if read > batch.DefaultMaxImageInputBytes || extra != 0 {
		cleanup()
		return "", func() {}, fmt.Errorf("image %q exceeds the %d-byte limit", label, batch.DefaultMaxImageInputBytes)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != expectedHash {
		cleanup()
		return "", func() {}, errors.New("batch input changed since planning")
	}
	return stagedPath, cleanup, nil
}

// batchFileError retains useful sentinel matching while avoiding absolute
// source paths in diagnostics produced by os.PathError.
func batchFileError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, os.ErrPermission):
		return os.ErrPermission
	case errors.Is(err, os.ErrClosed):
		return os.ErrClosed
	case errors.Is(err, os.ErrInvalid):
		return os.ErrInvalid
	default:
		return errors.New("file access failed")
	}
}
