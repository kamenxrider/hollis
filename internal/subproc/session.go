// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package subproc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// SessionSpec describes a long-lived child that speaks a protocol over its
// standard input and output, such as a stdio tool server.
//
// Run and Scan own a child for one bounded call. A session child instead
// stays alive across many requests, so the caller owns the protocol on the
// pipes and this package owns everything else: the exact environment, the
// process group, the stderr sink, and the escalating shutdown that ends with
// a group kill on every path.
type SessionSpec struct {
	// Name is the program path. It is never resolved through a shell.
	Name string
	Args []string
	// Env replaces the child environment exactly. A nil Env gives the child
	// an empty environment, never the parent's.
	Env []string
	Dir string
	// StderrSink receives every stderr byte from a single goroutine. A nil
	// sink discards stderr. The sink is not touched after Close returns.
	StderrSink io.Writer
}

// Session is a started child. Its methods are safe for concurrent use.
type Session struct {
	cmd    *exec.Cmd
	pgid   int
	stdin  *os.File
	stdout *os.File
	stderr *os.File

	exited  chan struct{}
	waitErr error

	stderrDone chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// Start launches the child in its own process group with deadline-owned
// pipes. The returned session must be closed.
func Start(spec SessionSpec) (*Session, error) {
	if spec.Name == "" {
		return nil, errors.New("subproc: a program path is required")
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("subproc: open stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW)
		return nil, fmt.Errorf("subproc: open stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW)
		return nil, fmt.Errorf("subproc: open stderr pipe: %w", err)
	}

	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Env = spec.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Dir = spec.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stderrW
	cmd.SysProcAttr = groupAttr(nil)
	cmd.Cancel = nil
	cmd.WaitDelay = 0
	if err := cmd.Start(); err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW)
		return nil, err
	}
	// The child holds its ends now; the parent's copies would keep our own
	// readers from ever seeing EOF and the child from seeing stdin close.
	closeAll(stdinR, stdoutW, stderrW)

	s := &Session{
		cmd:        cmd,
		pgid:       cmd.Process.Pid,
		stdin:      stdinW,
		stdout:     stdoutR,
		stderr:     stderrR,
		exited:     make(chan struct{}),
		stderrDone: make(chan struct{}),
	}
	sink := spec.StderrSink
	if sink == nil {
		sink = io.Discard
	}
	go func() {
		defer close(s.stderrDone)
		_, _ = io.Copy(sink, stderrR)
	}()
	// Wait does not touch our pipes because none of them came from
	// StdoutPipe/StdinPipe, so reaping cannot race the protocol reader.
	go func() {
		s.waitErr = cmd.Wait()
		close(s.exited)
	}()
	return s, nil
}

// Stdin is the child's standard input. Closing it is the polite request to
// exit.
func (s *Session) Stdin() io.WriteCloser { return s.stdin }

// Stdout is the child's standard output, the protocol channel.
func (s *Session) Stdout() io.ReadCloser { return s.stdout }

// PID reports the leader's process ID, which is also its process group ID.
func (s *Session) PID() int { return s.pgid }

// Exited is closed once the leader has been reaped.
func (s *Session) Exited() <-chan struct{} { return s.exited }

// ExitCode reports the leader's exit status once Exited is closed, or -1.
func (s *Session) ExitCode() int {
	select {
	case <-s.exited:
		return exitCode(s.waitErr)
	default:
		return -1
	}
}

// Close shuts the child down: it closes stdin, waits up to grace for the
// leader to exit, sends SIGTERM to the group, waits up to grace again, then
// SIGKILLs the group and reaps the leader. The group is killed once more
// after reaping so a descendant that outlived its leader cannot survive, and
// the read ends are closed so a detached holder cannot keep a reader alive.
// Close is idempotent; every call returns the first call's result.
func (s *Session) Close(grace time.Duration) error {
	s.closeOnce.Do(func() {
		_ = s.stdin.Close()
		if !s.waitFor(grace) {
			_ = syscall.Kill(-s.pgid, syscall.SIGTERM)
			if !s.waitFor(grace) {
				killGroup(s.pgid)
				<-s.exited
			}
		}
		killGroup(s.pgid)
		_ = s.stdout.Close()
		_ = s.stderr.Close()
		<-s.stderrDone
		s.closeErr = s.waitErr
	})
	return s.closeErr
}

// Kill skips the polite stages: it SIGKILLs the group, reaps the leader and
// closes the pipes. It is the deadline path.
func (s *Session) Kill() error {
	killGroup(s.pgid)
	return s.Close(0)
}

func (s *Session) waitFor(d time.Duration) bool {
	if d <= 0 {
		select {
		case <-s.exited:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-s.exited:
		return true
	case <-timer.C:
		return false
	}
}

func closeAll(files ...*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
