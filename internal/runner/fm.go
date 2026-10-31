// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

// Experimental text-only fm transport (draft, isolated): invokes Apple's
// /usr/bin/fm CLI as a subprocess. This file deliberately does NOT register a
// public model: ModelFmPCC is absent from Models, nothing routes to it, and no
// fallback exists. Production use requires separate approval and the FM1
// terms gate; see EVIDENCE.md and the workstream qualification ledger.
//
// Measured transport facts (live qualification receipts, macOS 27.2/26B123,
// recorded in the workstream evidence ledger):
//   - one-shot `fm respond --model pcc --no-stream` reads the prompt on stdin
//     and writes plain text to stdout; it reports no usage of any kind.
//   - outside the contexts fm trusts, PCC is refused deterministically with
//     "Private Cloud Compute is not available in this context".
//   - no streaming, schema, tool, image or usage capability is claimed here.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// fmBinaryPath is Apple's shipped CLI. fm exposes no --version; identity is
// established by the executable path plus its SHA-256 recorded at
// qualification time, not by a version string.
const fmBinaryPath = "/usr/bin/fm"

// fmMaxOutputBytes bounds captured stdout; exceeding it is a failure, never a
// silent truncation. fmMaxStderrBytes bounds the captured diagnostics.
const (
	fmMaxOutputBytes = 8 << 20
	fmMaxStderrBytes = 1 << 20
)

// ModelFmPCC requests Apple's fm PCC model through the experimental fm
// transport. It is intentionally NOT a member of Models and MUST NOT be added
// there, routed, or aliased to cloud/cloud-pro without separate approval.
const ModelFmPCC Model = "fm-pcc"

// Fm-specific failure kinds. The context refusal is the measured, stable
// client-side gate ("Please use the Terminal app"); it is classified exactly
// and is never treated as a rate limit or a retryable state.
const (
	KindFmMissing        Kind = "fm_missing"
	KindFmLicenseNeeded  Kind = "fm_license_needed"
	KindFmContextRefused Kind = "fm_context_refused"
	KindFmModelNotReady  Kind = "fm_model_not_ready"
	KindFmBadOutput      Kind = "fm_bad_output"
	KindFmOutputLimit    Kind = "fm_output_limit"
)

// FmRunner is the experimental one-shot fm subprocess transport. Like
// ShortcutRunner it satisfies Runner; it performs exactly one spawn per Run,
// never retries, and never falls back. The returned Model is the REQUESTED
// tier: fm exposes no machine-verifiable backend identity, so callers must
// not treat it as proof of which server model answered.
type FmRunner struct {
	// BinaryPath is the fm executable. The default is the Apple-shipped path;
	// tests point it at fake executables. Must be absolute.
	BinaryPath string
	// Timeout applies when the caller's context carries no deadline. The
	// caller's deadline is authoritative; MaxTimeout still clamps.
	Timeout time.Duration
}

// NewFm returns an FmRunner with the measured defaults.
func NewFm() *FmRunner { return &FmRunner{BinaryPath: fmBinaryPath, Timeout: DefaultTimeout} }

func fmError(kind Kind, ref string, err error) *Error {
	return &Error{Kind: kind, Ref: ref, ExitCode: -1, Err: err}
}

// Run implements Runner for the fm transport. Prompt transport is stdin only:
// the prompt never appears in argv, logs, or error text.
func (r *FmRunner) Run(ctx context.Context, model Model, prompt string) (string, Model, error) {
	if model != ModelFmPCC {
		return "", model, fmError(KindUsage, "", fmt.Errorf("%w: fm transport only supports model %q", ErrUnknownModel, ModelFmPCC))
	}
	if strings.TrimSpace(prompt) == "" {
		return "", model, fmError(KindEmptyPrompt, "", fmt.Errorf("%w: refusing to spawn fm respond without a prompt", ErrEmptyPrompt))
	}
	if !utf8.ValidString(prompt) {
		return "", model, fmError(KindUsage, "", errors.New("fm prompt must be valid UTF-8"))
	}

	path := r.BinaryPath
	if path == "" {
		path = fmBinaryPath
	}
	if !filepath.IsAbs(path) {
		return "", model, fmError(KindUsage, "", errors.New("fm binary path must be absolute"))
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", model, fmError(KindFmMissing, path, errors.New("fm executable is missing or not executable"))
	}

	// Deadline semantics identical to ShortcutRunner: caller deadline is
	// authoritative in both directions; r.Timeout is only the default; the
	// hard ceiling clamps.
	effective := r.Timeout
	if effective <= 0 {
		effective = DefaultTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		effective = time.Until(deadline)
	}
	if effective > MaxTimeout {
		effective = MaxTimeout
	}
	if effective <= 0 {
		return "", model, fmError(KindTimeout, path, errors.New("deadline already passed before fm could be spawned"))
	}
	runCtx, cancel := context.WithTimeout(ctx, effective)
	defer cancel()

	// Exactly one physical call: the prompt on stdin, plain-text non-streaming
	// output. --no-stream is deliberate: omitted/absent, fm serve and fm
	// respond default to SSE or interactive behavior, and a TTY suppresses
	// output entirely.
	cmd := exec.CommandContext(runCtx, path, "respond", "--model", "pcc", "--no-stream")
	cmd.Stdin = strings.NewReader(prompt)
	stdout := &limitBuffer{cap: fmMaxOutputBytes}
	stderr := &limitBuffer{cap: fmMaxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// WaitDelay guarantees Wait returns even when a descendant inherits the
	// stdout pipe and would otherwise hold it open forever.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		switch ctx.Err() {
		case context.Canceled:
			return "", model, fmError(KindContextCanceled, path, err)
		case context.DeadlineExceeded:
			return "", model, fmError(KindTimeout, path, err)
		}
		return "", model, fmError(KindTransport, path, fmt.Errorf("spawn %s: %w", path, err))
	}

	type result struct{ err error }
	done := make(chan result, 1)
	go func() { done <- result{err: cmd.Wait()} }()
	var res result
	select {
	case res = <-done:
	case <-runCtx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		res = <-done
	}

	if stdout.exceeded || stderr.exceeded {
		return "", model, fmError(KindFmOutputLimit, path, errors.New("fm output exceeded the capture bound; killed, never truncated"))
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "", model, fmError(KindContextCanceled, path, errors.New("fm call canceled by caller"))
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", model, fmError(KindTimeout, path, fmt.Errorf("fm call exceeded %s and was killed; no partial output is returned", effective))
	}

	out := stdout.String()
	errText := stderr.String()
	if res.err == nil && strings.TrimSpace(out) == "" {
		if isRateLimited(errText) {
			return "", model, &Error{Kind: KindRateLimited, Ref: path, ExitCode: 0, Stderr: errText, Err: errors.New("rate limit reported")}
		}
		return "", model, &Error{Kind: KindNoOutput, Ref: path, ExitCode: 0, Stderr: errText, Err: errors.New("exit 0 with empty stdout; treated as failure, not an empty response")}
	}
	if res.err != nil {
		exitCode := exitOf(res.err)
		// A descendant holding the capture pipe forced WaitDelay to expire:
		// the child may have exited 0, but captured output may be incomplete.
		// Never return it as a success.
		if errors.Is(res.err, exec.ErrWaitDelay) {
			return "", model, fmError(KindFmBadOutput, path, errors.New("fm output ended uncleanly; a descendant held the capture pipe open past the wait delay"))
		}
		switch {
		case strings.Contains(errText, "Private Cloud Compute is not available in this context"):
			return "", model, &Error{Kind: KindFmContextRefused, Ref: path, ExitCode: exitCode, Stderr: errText, Err: errors.New("fm refused PCC for this execution context")}
		case strings.Contains(strings.ToLower(errText), "license"):
			return "", model, &Error{Kind: KindFmLicenseNeeded, Ref: path, ExitCode: exitCode, Stderr: errText, Err: errors.New("fm reports the license terms have not been accepted")}
		case strings.Contains(errText, "not ready"), strings.Contains(errText, "does not support"):
			return "", model, &Error{Kind: KindFmModelNotReady, Ref: path, ExitCode: exitCode, Stderr: errText, Err: errors.New("fm reports the model is not ready")}
		case isRateLimited(errText):
			return "", model, &Error{Kind: KindRateLimited, Ref: path, ExitCode: exitCode, Stderr: errText, Err: errors.New("rate limit reported")}
		}
		if signal, signaled := processSignal(res.err); signaled {
			kind := KindSignal
			if signal == syscall.SIGABRT {
				kind = KindSIGABRT
			}
			return "", model, &Error{Kind: kind, Ref: path, ExitCode: exitCode, Stderr: errText, Err: res.err}
		}
		return "", model, &Error{Kind: KindShortcutFailed, Ref: path, ExitCode: exitCode, Stderr: errText, Err: fmt.Errorf("fm exited %d", exitCode)}
	}
	if !utf8.ValidString(out) {
		return "", model, fmError(KindFmBadOutput, path, errors.New("fm output was not valid UTF-8; refusing to return it"))
	}
	// The response is the child's stdout exactly as produced: no trimming,
	// repair, retry, or invented usage. Usage is unknown for this transport
	// and must stay unknown.
	return out, model, nil
}

// limitBuffer is a bytes.Buffer that fails closed at a hard cap: exceeding it
// marks the buffer and drops further bytes so the caller classifies the run
// as a failure instead of receiving silently truncated output.
type limitBuffer struct {
	buf      bytes.Buffer
	cap      int
	exceeded bool
}

func (l *limitBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.cap {
		l.exceeded = true
		return 0, errors.New("capture limit exceeded")
	}
	return l.buf.Write(p)
}

func (l *limitBuffer) String() string { return l.buf.String() }
