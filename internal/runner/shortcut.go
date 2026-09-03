// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ShortcutRunner invokes `/usr/bin/shortcuts run <bridge-reference>` with piped
// stdio and a hard deadline. It satisfies runner rules 1–7; rule 8 (concurrency
// policy) lives with callers. Run is safe for concurrent use — `shortcuts run`
// is stateless and 4 parallel invocations were proven clean.
type ShortcutRunner struct {
	// ShortcutsPath is the transport binary. Tests point this at a fake.
	ShortcutsPath string
	// BridgeRefs maps models to the identifier passed to `shortcuts run`.
	// New defaults to UUID candidates; callers should replace them with the
	// positively discovered names or explicit configured references.
	BridgeRefs map[Model]string
	// Timeout applies when the caller's context carries no deadline.
	// Measured p50 is ~1s; 30s default, 120s ceiling per plan §25.
	Timeout time.Duration
}

// New returns a ShortcutRunner with measured defaults: the system shortcuts
// CLI, compiled bridge candidates, and the 30s default timeout.
func New() *ShortcutRunner {
	return &ShortcutRunner{
		ShortcutsPath: DefaultShortcutsPath,
		BridgeRefs: map[Model]string{
			ModelCloud:    BridgeUUIDCloud,
			ModelCloudPro: BridgeUUIDCloudPro,
			ModelOnDevice: BridgeUUIDOnDevice,
			ModelChatGPT:  BridgeUUIDChatGPT,
		},
		Timeout: DefaultTimeout,
	}
}

// Run implements Runner. See the package doc for the rules this enforces.
// Callers that need to explain auto behavior should use RunWithFallback.
func (r *ShortcutRunner) Run(ctx context.Context, model Model, prompt string) (string, Model, error) {
	// Rule 4: reject empty prompts before spawning. Empty input hangs
	// `shortcuts run` forever; there is no exit code to map, only a kill.
	if strings.TrimSpace(prompt) == "" {
		return "", model, &Error{
			Kind:     KindEmptyPrompt,
			ExitCode: -1,
			Err:      fmt.Errorf("%w: shortcuts run hangs forever on empty input; refusing to spawn", ErrEmptyPrompt),
		}
	}
	if model == ModelAuto {
		text, used, _, err := r.runAuto(ctx, prompt)
		return text, used, err
	}
	text, _, _, err := r.RunWithFallback(ctx, model, prompt)
	return text, model, err
}

// RunWithFallback implements FallbackRunner. ModelAuto tries cloud once and,
// only for explicitly eligible transient failures, on-device once. Explicit
// tiers never fall back.
func (r *ShortcutRunner) RunWithFallback(ctx context.Context, model Model, prompt string) (string, Model, Fallback, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", model, Fallback{}, &Error{
			Kind: KindEmptyPrompt, ExitCode: -1,
			Err: fmt.Errorf("%w: shortcuts run hangs forever on empty input; refusing to spawn", ErrEmptyPrompt),
		}
	}
	if model != ModelAuto {
		text, err := r.runTier(ctx, model, prompt)
		return text, model, Fallback{}, err
	}
	return r.runAuto(ctx, prompt)
}

// runAuto tries the default tier (cloud) once, then the on-device model
// once. Empty prompts, usage errors, timeout/cancel, and process crashes are
// never retried: retrying them either produces the same local failure or
// risks another charge/usage event.
func (r *ShortcutRunner) runAuto(ctx context.Context, prompt string) (string, Model, Fallback, error) {
	text, err := r.runTier(ctx, ModelCloud, prompt)
	if err == nil {
		return text, ModelCloud, Fallback{}, nil
	}
	var re *Error
	if !errors.As(err, &re) || !FallbackEligible(re.Kind) {
		return "", ModelCloud, Fallback{}, err
	}
	primaryKind := re.Kind
	text, err = r.runTier(ctx, ModelOnDevice, prompt)
	return text, ModelOnDevice, Fallback{
		Used:   true,
		From:   ModelCloud,
		To:     ModelOnDevice,
		Reason: primaryKind,
	}, err
}

func (r *ShortcutRunner) runTier(ctx context.Context, model Model, prompt string) (string, error) {
	if !model.Valid() || model == ModelAuto {
		return "", &Error{Kind: KindUsage, ExitCode: -1, Err: fmt.Errorf("%w: %q", ErrUnknownModel, model)}
	}
	ref, ok := r.BridgeRefs[model]
	if !ok || strings.TrimSpace(ref) == "" {
		return "", &Error{
			Kind: KindShortcutMissing, ExitCode: -1,
			Err: fmt.Errorf("bridge for %q was not positively discovered or explicitly configured", model),
		}
	}
	if strings.HasPrefix(strings.TrimSpace(ref), "-") {
		return "", &Error{Kind: KindUsage, Ref: ref, ExitCode: -1, Err: errors.New("bridge reference must not begin with '-'")}
	}

	// Rule 3: always a deadline. The caller's deadline is authoritative in
	// BOTH directions when present; r.Timeout is only the default for
	// callers that set none. Anything above the ceiling is clamped (plan
	// §25).
	//
	// This used to take the caller's deadline only when it was SHORTER than
	// r.Timeout, which silently capped every run at the 30s default: a
	// `--timeout 120s` died at 30s, MaxTimeout was unreachable dead code,
	// and the timeout error's own "hint: raise --timeout" was advice that
	// could not work.
	effective := r.Timeout
	if deadline, has := ctx.Deadline(); has {
		effective = time.Until(deadline)
	}
	if effective > MaxTimeout {
		effective = MaxTimeout
	}
	if effective <= 0 {
		// The caller's deadline already passed. Spawning anyway would hand
		// exec an expired context, Start would fail with ctx.Err(), and that
		// lands in KindTransport rather than KindTimeout — with a message
		// quoting a negative duration.
		return "", &Error{
			Kind:     KindTimeout,
			Ref:      ref,
			ExitCode: -1,
			Err:      errors.New("deadline already passed before the shortcut could be spawned"),
		}
	}
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, effective)
	defer cancel()

	// Rule 1: plain text, never the RTF default. Rule 7: reference by UUID.
	cmd := exec.CommandContext(ctx, r.ShortcutsPath, "run", ref, "--output-type", "public.plain-text")
	cmd.Stdin = strings.NewReader(prompt)

	// Rule 2: capture via pipes. bytes.Buffer forces exec to create an
	// os.Pipe for stdout — the child never sees a TTY, which is the one
	// form that reliably produces output.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Rule 3: deadline semantics. exec.CommandContext kills only the direct
	// child on cancel; put the child in its own process group and SIGKILL
	// the whole group so no orphaned `shortcuts run` survives the deadline.
	// SIGALRM was measured to terminate the hung child cleanly; SIGKILL on
	// the group is at least as decisive.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// WaitDelay guarantees Wait returns even if the killed child's pipe
	// goroutines would otherwise linger.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		switch parentCtx.Err() {
		case context.Canceled:
			return "", &Error{
				Kind:     KindContextCanceled,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("context canceled before %s could start: %w", r.ShortcutsPath, err),
			}
		case context.DeadlineExceeded:
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline already exceeded before %s could start: %w", r.ShortcutsPath, err),
			}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline exceeded before %s could start: %w", r.ShortcutsPath, err),
			}
		}
		return "", &Error{
			Kind:     KindTransport,
			Ref:      ref,
			ExitCode: -1,
			Err:      fmt.Errorf("spawn %s: %w", r.ShortcutsPath, err),
		}
	}

	type result struct {
		err      error
		deadline bool
	}
	done := make(chan result, 1)
	go func() {
		err := cmd.Wait()
		// Buffers are filled before Wait returns (exec drains them).
		done <- result{err: err}
	}()

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		// Rule 3: kill the process group, then reap.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		res = <-done
		res.deadline = true
	}

	out := stdout.String()
	if res.deadline {
		if errors.Is(parentCtx.Err(), context.Canceled) {
			return "", &Error{
				Kind: KindContextCanceled, Ref: ref, ExitCode: -1,
				Err: errors.New("shortcut run canceled by caller"),
			}
		}
		return "", &Error{
			Kind:     KindTimeout,
			Ref:      ref,
			ExitCode: -1,
			Err:      fmt.Errorf("shortcut run exceeded %s and was killed", effective),
		}
	}

	exitCode := exitOf(res.err)
	stderrText := stderr.String()

	// Rule 5: exit 0 + empty stdout is shortcut_no_output, never a response.
	// On a TTY this is exactly what suppressed-but-successful output looks
	// like, so it must always be treated as failure here.
	if res.err == nil && strings.TrimSpace(out) == "" {
		if isRateLimited(stderr.String()) {
			return "", &Error{
				Kind:     KindRateLimited,
				Ref:      ref,
				ExitCode: 0,
				Stderr:   stderr.String(),
				Err:      errors.New("rate limit reported"),
			}
		}
		return "", &Error{
			Kind:     KindNoOutput,
			Ref:      ref,
			ExitCode: 0,
			Err:      errors.New("exit 0 with empty stdout; treated as failure, not an empty response"),
		}
	}

	if res.err != nil {
		switch parentCtx.Err() {
		case context.Canceled:
			return "", &Error{
				Kind:     KindContextCanceled,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("context canceled while running %s: %w", r.ShortcutsPath, res.err),
			}
		case context.DeadlineExceeded:
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline exceeded while running %s: %w", r.ShortcutsPath, res.err),
			}
		}
		// The runner adds its own default/ceiling deadline when the caller has
		// none. cmd.Wait can win the select race against ctx.Done after the
		// process is killed; consult the derived context before treating that
		// SIGKILL as an ordinary process crash.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("shortcut run exceeded %s and was killed", effective),
			}
		}
		signal, signaled := processSignal(res.err)
		kind := KindTransport
		switch {
		case signaled && signal == syscall.SIGABRT:
			kind = KindSIGABRT
		case signaled:
			kind = KindSignal
		case isRateLimited(stderrText):
			kind = KindRateLimited
		case isMissingShortcut(stderrText):
			kind = KindShortcutMissing
		case exitCode == 64:
			kind = KindUsage
		}
		err := res.err
		if exitCode >= 0 {
			if se := strings.TrimSpace(stderrText); se != "" {
				err = fmt.Errorf("%s: %w", se, err)
			}
		}
		return "", &Error{
			Kind:     kind,
			Ref:      ref,
			ExitCode: exitCode,
			Signal:   signal,
			Stderr:   stderrText,
			Err:      err,
		}
	}

	// Rule 6: return stdout exactly as produced. Apple omits the trailing
	// newline; we neither expect nor add one here.
	return out, nil
}

func (r *ShortcutRunner) ListShortcuts(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		kind := KindTransport
		switch err {
		case context.Canceled:
			kind = KindContextCanceled
		case context.DeadlineExceeded:
			kind = KindTimeout
		}
		return nil, &Error{Kind: kind, ExitCode: -1, Err: err}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, r.ShortcutsPath, "list").Output()
	if err != nil {
		kind := KindListFailure
		signal, _ := processSignal(err)
		switch cctx.Err() {
		case context.Canceled:
			kind = KindContextCanceled
		case context.DeadlineExceeded:
			kind = KindTimeout
		}
		return nil, &Error{Kind: kind, ExitCode: exitOf(err), Signal: signal, Err: err}
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

func exitOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func processSignal(err error) (syscall.Signal, bool) {
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ProcessState == nil {
		return 0, false
	}
	ws, ok := ee.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0, false
	}
	return ws.Signal(), true
}

func isRateLimited(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "too many incoming requests") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate-limit")
}

func isMissingShortcut(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "could not be found") ||
		strings.Contains(lower, "shortcut not found")
}
