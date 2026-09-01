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

// ShortcutRunner invokes `/usr/bin/shortcuts run <bridge-UUID>` with piped
// stdio and a hard deadline. It satisfies runner rules 1–7; rule 8 (concurrency
// policy) lives with callers. Run is safe for concurrent use — `shortcuts run`
// is stateless and 4 parallel invocations were proven clean.
type ShortcutRunner struct {
	// ShortcutsPath is the transport binary. Tests point this at a fake.
	ShortcutsPath string
	// BridgeRefs maps models to the identifier passed to `shortcuts run`.
	// Defaults to UUIDs; names collide and get renamed (plan §36 rule 7).
	BridgeRefs map[Model]string
	// Timeout applies when the caller's context carries no deadline.
	// Measured p50 is ~1s; 30s default, 120s ceiling per plan §25.
	Timeout time.Duration
}

// New returns a ShortcutRunner with measured defaults: the system shortcuts
// CLI, bridge references by UUID, and the 30s default timeout.
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
// ModelAuto is resolved here: cloud first, one on-device retry on any
// transport-class failure (Apple's documented PCC fallback pattern).
// Explicit tier selections are passed through untouched.
func (r *ShortcutRunner) Run(ctx context.Context, model Model, prompt string) (string, error) {
	// Rule 4: reject empty prompts before spawning. Empty input hangs
	// `shortcuts run` forever; there is no exit code to map, only a kill.
	if strings.TrimSpace(prompt) == "" {
		return "", &Error{
			Kind:     KindEmptyPrompt,
			ExitCode: -1,
			Err:      fmt.Errorf("%w: shortcuts run hangs forever on empty input; refusing to spawn", ErrEmptyPrompt),
		}
	}
	if model == ModelAuto {
		return r.runAuto(ctx, prompt)
	}
	return r.runTier(ctx, model, prompt)
}

// runAuto tries the default tier (cloud) once, then the on-device model
// once. Empty-prompt errors are never retried: both tiers would refuse
// the same way, and the caller was already told before any spawn.
func (r *ShortcutRunner) runAuto(ctx context.Context, prompt string) (string, error) {
	text, err := r.runTier(ctx, ModelCloud, prompt)
	if err == nil {
		return text, nil
	}
	var re *Error
	if !errors.As(err, &re) || re.Kind == KindEmptyPrompt {
		return "", err
	}
	return r.runTier(ctx, ModelOnDevice, prompt)
}

func (r *ShortcutRunner) runTier(ctx context.Context, model Model, prompt string) (string, error) {
	ref, ok := r.BridgeRefs[model]
	if !ok {
		return "", &Error{Kind: KindUsage, ExitCode: -1, Err: fmt.Errorf("%w: %q", ErrUnknownModel, model)}
	}

	// Rule 3: always a deadline. Use the caller's when present, else the
	// default; anything above the 120s ceiling is clamped to it (plan §25).
	effective := r.Timeout
	if deadline, has := ctx.Deadline(); has {
		if wait := time.Until(deadline); wait < effective {
			effective = wait
		}
	}
	if effective > MaxTimeout {
		effective = MaxTimeout
	}
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
	if res.err == nil && len(out) == 0 {
		return "", &Error{
			Kind:     KindNoOutput,
			Ref:      ref,
			ExitCode: 0,
			Err:      errors.New("exit 0 with empty stdout; treated as failure, not an empty response"),
		}
	}

	if res.err != nil {
		kind := KindTransport
		switch exitCode {
		case 1:
			kind = KindShortcutMissing
		case 64:
			kind = KindUsage
		case 134:
			kind = KindSIGABRT
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
			Stderr:   stderrText,
			Err:      err,
		}
	}

	// Rule 6: return stdout exactly as produced. Apple omits the trailing
	// newline; we neither expect nor add one here.
	return out, nil
}

func (r *ShortcutRunner) ListShortcuts(ctx context.Context) ([]string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, r.ShortcutsPath, "list").Output()
	if err != nil {
		return nil, &Error{Kind: KindTransport, ExitCode: exitOf(err), Err: err}
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
