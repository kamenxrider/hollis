// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package subproc runs one child process under a single lifecycle contract.
//
// Every call owns its own stdout and stderr pipes, closes the parent copies of
// the write ends immediately after the child starts, and closes the read ends
// when the governing deadline expires. That ordering is what makes the wall
// clock the deadline's, not a leftover descendant's: exec.Cmd's built-in pipe
// copier keeps a capture open while any process still holds the write end, and
// a detached holder has to be disconnected by closing the read end because no
// signal can reach it.
//
// The child runs in its own process group. The group is SIGKILLed when the
// deadline expires, and again once the leader has been reaped so a descendant
// that outlived its leader cannot survive the call. A successful exit is not a
// completed run while the deadline has expired: the context is consulted after
// Wait returns, so partial output is never handed back as a result.
//
// Classification stays with the caller. This package reports what happened
// (exit status, captured bytes, truncation, read and callback failures) and
// never invents a user-facing error message of its own.
//
// Accepted limitation: a holder that leaves the child's process group survives
// the call. The deadline still bounds it, because closing the read ends
// disconnects the pipe instead of signalling the holder; no test here claims to
// kill one, and the fixtures assert the holder is still alive when the budget
// is observed to hold. Every process that stays in the group is killed.
package subproc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// defaultLineCap bounds one scanned line when Spec.LineCap is zero.
const defaultLineCap = 1 << 20

// Spec describes one invocation.
//
// Capture bounds follow one convention: a negative Cap discards the stream, a
// zero Cap captures it without a bound, and a positive Cap captures at most
// that many bytes while still draining the pipe so the child never blocks.
type Spec struct {
	// Name is the program path. It is never resolved through a shell.
	Name string
	Args []string
	// Env replaces the child environment exactly when non-nil. A nil Env
	// inherits the parent environment.
	Env []string
	Dir string
	// Stdin, when non-nil, is written to the child's standard input.
	Stdin []byte
	// Timeout bounds the run from the moment it starts. Zero means the
	// caller's deadline is the only bound.
	Timeout time.Duration

	StdoutCap int64
	StderrCap int64
	// StdoutSink and StderrSink replace capture with a caller-owned writer,
	// for call sites that already keep their own bounded buffer. The sink is
	// written from a single goroutine and is not touched after Run or Scan
	// returns.
	StdoutSink io.Writer
	StderrSink io.Writer

	// LineCap bounds a single stdout line in Scan.
	LineCap int

	// Builder substitutes command construction. The returned command must not
	// have been started; this package overrides its pipes, process group,
	// cancellation and wait delay, and keeps any Env and Dir the builder set.
	Builder func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// Result reports what the child did. It is populated even when Run or Scan
// returns an error, so a caller can include the captured diagnostics in its
// own classification.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte

	StdoutTruncated bool
	StderrTruncated bool
	// LineOverflow reports that Scan stopped reading a stdout line longer
	// than the line cap.
	LineOverflow bool

	// Started is false when the child never ran.
	Started bool
	// StartErr is the failure returned by starting the command.
	StartErr error
	// WaitErr is the leader's wait status: nil for a clean exit, an
	// *exec.ExitError otherwise.
	WaitErr error
	// ReadErr is a capture failure other than the read end being closed by
	// an expired deadline.
	ReadErr error
	// EmitErr is the error returned by the Scan callback.
	EmitErr error
	// Expired reports that the governing context ended before the run
	// finished. No captured output from such a run may be used as a result.
	Expired bool
}

// Run executes the spec and returns once the child has been reaped, its pipes
// are closed, and its process group has been killed.
//
// The returned error is a lifecycle failure only: a start failure, a read
// failure, a callback failure (Scan), or expiry of the governing context. A
// child that ran and exited, whatever its status, returns a nil error with the
// status in Result so the caller can classify it.
func Run(ctx context.Context, spec Spec) (Result, error) {
	return execute(ctx, spec, nil)
}

// Scan is Run with stdout delivered line by line to onEvent, for protocols
// that must be validated while they stream. A one-line latency is expected;
// the callback runs on the reading goroutine and must not call back into the
// same Spec.
//
// An error from onEvent stops the run: the process group is killed, the pipes
// are closed, and the callback's error is returned verbatim so callers keep
// their own protocol diagnostics. A line longer than the line cap sets
// Result.LineOverflow and stops reading without inventing an error.
func Scan(ctx context.Context, spec Spec, onEvent func([]byte) error) (Result, error) {
	if onEvent == nil {
		return Result{ExitCode: -1}, errors.New("subproc: Scan requires a callback")
	}
	return execute(ctx, spec, onEvent)
}

func execute(parent context.Context, spec Spec, onEvent func([]byte) error) (Result, error) {
	res := Result{ExitCode: -1}
	if spec.Name == "" {
		return res, errors.New("subproc: a program path is required")
	}
	if onEvent != nil && spec.StdoutSink != nil {
		return res, errors.New("subproc: Scan cannot also capture into a stdout sink")
	}

	ctx := parent
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	// An already-expired deadline must never reach Start: exec would report
	// the context error and the caller could only misclassify it as a spawn
	// failure.
	if err := ctx.Err(); err != nil {
		res.Expired = true
		return res, err
	}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return res, fmt.Errorf("subproc: open stdout pipe: %w", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return res, fmt.Errorf("subproc: open stderr pipe: %w", err)
	}
	defer stderrR.Close()
	defer stderrW.Close()

	cmd := buildCommand(ctx, spec)
	if cmd == nil {
		return res, errors.New("subproc: command builder returned nil")
	}
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	cmd.SysProcAttr = groupAttr(cmd.SysProcAttr)
	// The pipes above are the deadline's to close. exec.Cmd's own watcher and
	// wait delay would only race that ownership, so both are disabled.
	cmd.Cancel = nil
	cmd.WaitDelay = 0

	if err := cmd.Start(); err != nil {
		res.StartErr = err
		if ctxErr := ctx.Err(); ctxErr != nil {
			res.Expired = true
			return res, ctxErr
		}
		return res, err
	}
	res.Started = true
	// The child holds the write ends now. Keeping the parent's copies open
	// would keep our own readers from ever seeing EOF.
	_ = stdoutW.Close()
	_ = stderrW.Close()

	pgid := cmd.Process.Pid
	reads := &readEnds{files: []*os.File{stdoutR, stderrR}}
	stopWatcher := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		select {
		case <-ctx.Done():
			// Kill so a leader that is still running cannot outlive the
			// deadline and Wait returns promptly. The capture readers are
			// closed below, on the same signal, once Wait has returned.
			killGroup(pgid)
		case <-stopWatcher:
		}
	}()

	var stdoutResult, stderrResult drainResult
	var drains sync.WaitGroup
	drains.Add(2)
	// An aborted read is finished reading: once the callback rejects a line the
	// caller has no use for the rest of the stream, so the run kills the group
	// and disconnects the pipes instead of waiting for an EOF that a killed
	// writer does not always make pending.
	abort := func() {
		killGroup(pgid)
		reads.Close()
	}
	go func() {
		defer drains.Done()
		stdoutResult = drainStdout(stdoutR, spec, onEvent, pgid, abort)
	}()
	go func() {
		defer drains.Done()
		stderrResult = drainStderr(stderrR, spec)
	}()
	drained := make(chan struct{})
	go func() {
		drains.Wait()
		close(drained)
	}()

	waitErr := cmd.Wait()
	res.WaitErr = waitErr
	res.ExitCode = exitCode(waitErr)

	// A leader exit is not the end of the capture: a descendant may still
	// hold the write ends. The deadline bounds that wait: closing the read
	// ends is the only way to end a read that a detached holder — one no
	// signal can reach — would otherwise keep alive.
	select {
	case <-drained:
	case <-ctx.Done():
		reads.Close()
		<-drained
	}
	close(stopWatcher)
	watcher.Wait()

	// Every exit path kills the group, including a clean leader exit: the
	// leader has been reaped but any surviving group member keeps the group
	// identifier allocated until it is killed, so this cannot reach an
	// unrelated group.
	killGroup(pgid)
	reads.Close()

	res.Stdout, res.StdoutTruncated = stdoutResult.data, stdoutResult.truncated
	res.Stderr, res.StderrTruncated = stderrResult.data, stderrResult.truncated
	res.LineOverflow = stdoutResult.lineOverflow
	res.EmitErr = stdoutResult.emitErr
	res.ReadErr = stdoutResult.readErr
	if res.ReadErr == nil {
		res.ReadErr = stderrResult.readErr
	}

	// A successful return after the deadline expired would present partial
	// output as a complete result. The context gets the last word.
	if err := ctx.Err(); err != nil {
		res.Expired = true
		return res, err
	}
	if res.EmitErr != nil {
		return res, res.EmitErr
	}
	if res.ReadErr != nil {
		return res, res.ReadErr
	}
	return res, nil
}

func buildCommand(ctx context.Context, spec Spec) *exec.Cmd {
	if spec.Builder != nil {
		return spec.Builder(ctx, spec.Name, spec.Args...)
	}
	return exec.Command(spec.Name, spec.Args...)
}

func groupAttr(existing *syscall.SysProcAttr) *syscall.SysProcAttr {
	if existing == nil {
		existing = &syscall.SysProcAttr{}
	}
	attr := *existing
	attr.Setpgid = true
	return &attr
}

func killGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// readEnds closes the capture readers exactly once. Closing them is the only
// way to end a read that a detached holder would otherwise keep alive.
type readEnds struct {
	once  sync.Once
	files []*os.File
}

func (r *readEnds) Close() {
	r.once.Do(func() {
		for _, file := range r.files {
			_ = file.Close()
		}
	})
}

type drainResult struct {
	data         []byte
	truncated    bool
	lineOverflow bool
	readErr      error
	emitErr      error
}

func drainStdout(r *os.File, spec Spec, onEvent func([]byte) error, pgid int, abort func()) drainResult {
	if onEvent != nil {
		return scanStdout(r, spec.LineCap, onEvent, abort)
	}
	if spec.StdoutSink != nil {
		var result drainResult
		if _, err := io.Copy(spec.StdoutSink, r); err != nil && !isClosed(err) {
			result.readErr = err
		}
		return result
	}
	capture := &capture{limit: spec.StdoutCap}
	_, err := io.Copy(capture, r)
	result := drainResult{data: capture.data, truncated: capture.truncated}
	if err != nil && !isClosed(err) {
		result.readErr = err
	}
	return result
}

func drainStderr(r *os.File, spec Spec) drainResult {
	if spec.StderrSink != nil {
		var result drainResult
		if _, err := io.Copy(spec.StderrSink, r); err != nil && !isClosed(err) {
			result.readErr = err
		}
		return result
	}
	if spec.StderrCap < 0 {
		var result drainResult
		if _, err := io.Copy(io.Discard, r); err != nil && !isClosed(err) {
			result.readErr = err
		}
		return result
	}
	capture := &capture{limit: spec.StderrCap}
	_, err := io.Copy(capture, r)
	result := drainResult{data: capture.data, truncated: capture.truncated}
	if err != nil && !isClosed(err) {
		result.readErr = err
	}
	return result
}

func scanStdout(r io.Reader, lineCap int, onEvent func([]byte) error, abort func()) drainResult {
	var result drainResult
	if lineCap <= 0 {
		lineCap = defaultLineCap
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), lineCap)
	for scanner.Scan() {
		line := scanner.Bytes()
		if err := onEvent(line); err != nil {
			result.emitErr = err
			// A rejected protocol must not keep running: kill the group and
			// stop reading so a callback failure cannot outlive the call.
			abort()
			return result
		}
	}
	if err := scanner.Err(); err != nil {
		switch {
		case errors.Is(err, bufio.ErrTooLong):
			result.lineOverflow = true
		case isClosed(err):
			// The deadline closed the read end; the caller reports expiry.
		default:
			result.readErr = err
		}
	}
	return result
}

// capture accumulates at most limit bytes and keeps draining afterwards, so a
// flooding child grows the parent heap only to the cap. A zero limit captures
// without a bound; a negative limit discards.
type capture struct {
	limit     int64
	data      []byte
	truncated bool
}

func (c *capture) Write(p []byte) (int, error) {
	written := len(p)
	if c.limit < 0 {
		return written, nil
	}
	if c.limit == 0 {
		c.data = append(c.data, p...)
		return written, nil
	}
	if room := c.limit - int64(len(c.data)); room > 0 {
		if int64(len(p)) > room {
			c.data = append(c.data, p[:room]...)
			c.truncated = true
		} else {
			c.data = append(c.data, p...)
		}
	} else if written > 0 {
		c.truncated = true
	}
	return written, nil
}

func isClosed(err error) bool {
	return errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EBADF)
}
