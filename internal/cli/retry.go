// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

type localRetryKey struct{}

type localRetry struct {
	extra    int
	attempts int
	notes    io.Writer
	wait     func(context.Context, time.Duration) error
}

const retryLocalOnly = "--retry works only with --model local; Cloud, Cloud Pro, on-device and ChatGPT requests are never retried"
const retryHelp = "Retry transient Local failures only; bare --retry means 3 extra attempts, --retry=N sets 0-5 (not --retry N); waits share --timeout"

// addLocalRetry keeps route validation ahead of discovery, runner construction,
// prompt reads and image generation. Continued chats retain their stored route.
func addLocalRetry(cmd *cobra.Command, flags *rootFlags) {
	var extra int
	cmd.Flags().IntVar(&extra, "retry", 0, retryHelp)
	cmd.Flags().Lookup("retry").NoOptDefVal = "3"
	validate := cmd.Args
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("retry") && (extra < 0 || extra > 5) {
			return usageErr(errors.New("--retry=N requires an extra-attempt count from 0 to 5"))
		}
		return validate(cmd, args)
	}
	flags.checkFlagsBeforeHelp(cmd, cmd.Args)
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("retry") {
			return run(cmd, args)
		}
		if image, _ := cmd.Flags().GetBool("generate-image"); image {
			return usageErr(errors.New("--retry cannot be used with image generation"))
		}
		if count, _ := cmd.Flags().GetBool("count-tokens"); count {
			return usageErr(errors.New("--retry cannot be used with --count-tokens; counting does not generate an answer"))
		}
		model, _ := cmd.Flags().GetString("model")
		posModel, _, hasPosModel := splitModelArgs(args)
		if cmd.Name() == "respond" {
			posModel, _, hasPosModel = splitRespondModelArgs(cmd, args)
		}
		var selected runner.Model
		if id, _ := cmd.Flags().GetString("continue"); id != "" {
			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			conv, err := st.GetConversation(id)
			st.Close()
			if err != nil {
				return notFoundErr(err)
			}
			selected = runner.Model(conv.Model)
		} else {
			var err error
			selected, err = effectiveModel(cmd, model, posModel, hasPosModel)
			if err != nil {
				return err
			}
		}
		if selected != runner.ModelLocal {
			return usageErr(errors.New(retryLocalOnly))
		}
		notes := cmd.ErrOrStderr()
		if flags.quiet {
			notes = io.Discard
		}
		state := &localRetry{extra: extra, attempts: 1, notes: notes, wait: waitLocalRetry}
		cmd.SetContext(context.WithValue(cmd.Context(), localRetryKey{}, state))
		return run(cmd, args)
	}
}

// mayRetryLocal is the replay safety rule. A classified transient error alone
// is insufficient: any output, captured callback or dispatched tool closes it.
func mayRetryLocal(model runner.Model, err error, outputStarted, toolDispatched, structuredStarted bool) bool {
	var failure *runner.Error
	if model != runner.ModelLocal || outputStarted || toolDispatched || structuredStarted ||
		!errors.As(err, &failure) || !runner.ErrorContractFor(failure.Kind).Retryable() {
		return false
	}
	return failure.NativeFailure == nil || failure.NativeFailure.CallbackCaptured == nil ||
		!*failure.NativeFailure.CallbackCaptured
}

func retryDelay(extraAttempt int) time.Duration {
	if extraAttempt > 3 {
		return 8 * time.Second
	}
	return time.Second << (extraAttempt - 1)
}

func waitLocalRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func addRetryAttempts(ctx context.Context, data map[string]any) {
	if ctx == nil {
		return
	}
	if retry, _ := ctx.Value(localRetryKey{}).(*localRetry); retry != nil {
		data["attempts"] = retry.attempts
	}
}

type localRetryRunner struct {
	runner.Runner
	retry          *localRetry
	outputStarted  bool
	toolDispatched bool
}

// Wrap only CLI generation, after extraction/counting. No server, batch,
// discovery, token counting or image-generation path can acquire this policy.
func retryRunnerForContext(ctx context.Context, r runner.Runner) runner.Runner {
	state, _ := ctx.Value(localRetryKey{}).(*localRetry)
	if state == nil {
		return r
	}
	state.attempts = 1 // A new chat turn has its own extra-attempt budget.
	wrapped := &localRetryRunner{Runner: r, retry: state}
	if checker, ok := r.(toolHistoryChecker); ok {
		return &checkedLocalRetryRunner{localRetryRunner: wrapped, toolHistoryChecker: checker}
	}
	return wrapped
}

type toolHistoryChecker interface {
	CheckToolHistory(context.Context, runner.NativeToolStep) error
}

// Preserve optional native preflight support without claiming it for fakes or
// other runners that cannot provide it. Preflight itself is never retried.
type checkedLocalRetryRunner struct {
	*localRetryRunner
	toolHistoryChecker
}

func (r *localRetryRunner) emit(emit func(string) error) func(string) error {
	if emit == nil {
		return nil
	}
	return func(delta string) error {
		r.outputStarted = r.outputStarted || delta != ""
		return emit(delta)
	}
}

func (r *localRetryRunner) complete(ctx context.Context, model runner.Model, toolRound bool, run func() (runner.Completion, error)) (runner.Completion, error) {
	for {
		if err := ctx.Err(); err != nil {
			return runner.Completion{}, retryContextError(err)
		}
		result, err := run()
		// Callback capture is a stronger barrier than dispatch: a successful
		// tool decision can never be replayed by this wrapper, even if later
		// approval, execution or synthesis fails.
		r.toolDispatched = r.toolDispatched || len(result.NativeCalls) > 0
		eligible := mayRetryLocal(model, err, r.outputStarted || result.Text != "", r.toolDispatched, len(result.Structured) > 0)
		if toolRound {
			var failure *runner.Error
			// Tool decisions need affirmative evidence of no capture.
			eligible = eligible && errors.As(err, &failure) && failure.NativeFailure != nil &&
				failure.NativeFailure.CallbackCaptured != nil && !*failure.NativeFailure.CallbackCaptured
		}
		if err == nil || !eligible || r.retry.attempts > r.retry.extra {
			if toolRound && err == nil {
				r.toolDispatched = true
			}
			return result, err
		}
		delay := retryDelay(r.retry.attempts)
		if err := ctx.Err(); err != nil {
			return runner.Completion{}, retryContextError(err)
		}
		fmt.Fprintf(r.retry.notes, "hollis: Local model busy; retrying in %d s (attempt %d of %d)\n",
			int(delay/time.Second), r.retry.attempts+1, r.retry.extra+1)
		if err := r.retry.wait(ctx, delay); err != nil {
			return runner.Completion{}, retryContextError(err)
		}
		r.retry.attempts++
	}
}

func retryContextError(err error) error {
	kind := runner.KindContextCanceled
	if errors.Is(err, context.DeadlineExceeded) {
		kind = runner.KindTimeout
	}
	return &runner.Error{Kind: kind, ExitCode: -1, Err: err}
}

func (r *localRetryRunner) RunComplete(ctx context.Context, model runner.Model, prompt string) (runner.Completion, error) {
	return r.complete(ctx, model, false, func() (runner.Completion, error) {
		if complete, ok := r.Runner.(runner.CompleteRunner); ok {
			return complete.RunComplete(ctx, model, prompt)
		}
		text, used, err := r.Runner.Run(ctx, model, prompt)
		return runner.Completion{Text: text, Model: used}, err
	})
}

func (r *localRetryRunner) Stream(ctx context.Context, model runner.Model, prompt string, emit func(string) error) (runner.Completion, error) {
	stream, ok := r.Runner.(runner.StreamRunner)
	if !ok {
		return runner.Completion{}, errors.New("configured runner does not support native-local streaming")
	}
	return r.complete(ctx, model, false, func() (runner.Completion, error) {
		return stream.Stream(ctx, model, prompt, r.emit(emit))
	})
}

func (r *localRetryRunner) RunSchema(ctx context.Context, model runner.Model, prompt string, schema *runner.Schema) (runner.Completion, error) {
	guided, ok := r.Runner.(runner.SchemaRunner)
	if !ok {
		return runner.Completion{}, errors.New("configured runner does not support guided output")
	}
	return r.complete(ctx, model, false, func() (runner.Completion, error) {
		return guided.RunSchema(ctx, model, prompt, schema)
	})
}

func (r *localRetryRunner) RunGeneration(ctx context.Context, model runner.Model, request runner.GenerationRequest, emit func(string) error) (runner.Completion, error) {
	generation, ok := r.Runner.(runner.GenerationRunner)
	if !ok {
		return runner.Completion{}, errors.New("configured runner does not support native-local sampling")
	}
	return r.complete(ctx, model, false, func() (runner.Completion, error) {
		return generation.RunGeneration(ctx, model, request, r.emit(emit))
	})
}

func (r *localRetryRunner) RunCompleteWithImages(ctx context.Context, model runner.Model, prompt string, images []string) (runner.Completion, error) {
	imagesRunner, ok := r.Runner.(runner.ImageCompleteRunner)
	if !ok {
		return runner.Completion{}, errors.New("configured runner does not support native-local image completion")
	}
	return r.complete(ctx, model, false, func() (runner.Completion, error) {
		return imagesRunner.RunCompleteWithImages(ctx, model, prompt, images)
	})
}

func (r *localRetryRunner) StreamWithImages(ctx context.Context, model runner.Model, prompt string, images []string, emit func(string) error) (runner.Completion, error) {
	stream, ok := r.Runner.(runner.ImageStreamRunner)
	if !ok {
		return runner.Completion{}, errors.New("configured runner does not support native-local image streaming")
	}
	return r.complete(ctx, model, false, func() (runner.Completion, error) {
		return stream.StreamWithImages(ctx, model, prompt, images, r.emit(emit))
	})
}

func (r *localRetryRunner) RunToolRound(ctx context.Context, model runner.Model, request runner.ToolRoundRequest) (runner.Completion, error) {
	tools, ok := r.Runner.(runner.ToolRoundRunner)
	if !ok {
		return runner.Completion{}, errors.New("configured runner does not support native-local tool calling")
	}
	return r.complete(ctx, model, true, func() (runner.Completion, error) {
		return tools.RunToolRound(ctx, model, request)
	})
}
