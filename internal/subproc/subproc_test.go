// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package subproc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	helperModeEnv  = "HOLLIS_SUBPROC_HELPER"
	helperPIDEnv   = "HOLLIS_SUBPROC_HELPER_PID"
	helperWriteEnv = "HOLLIS_SUBPROC_HELPER_WRITE"
)

// lateOutput is a complete-looking response. A process that emits this after the
// deadline and exits 0 must not be able to turn a killed run into a success.
const lateOutput = `{"protocol":1,"version":"0.4.0","model":"local","event":"complete","text":"late"}`

// TestSubprocHelperProcess is an offline fixture: it is inert unless a test
// selects a mode. The detached holder deliberately leaves the leader's process
// group, so no signal can close its inherited pipe.
func TestSubprocHelperProcess(t *testing.T) {
	mode := os.Getenv(helperModeEnv)
	if mode == "" {
		t.Skip("offline fixture only")
	}
	switch mode {
	case "detached-leader":
		holder := exec.Command(os.Args[0], "-test.run=^TestSubprocHelperProcess$")
		holder.Env = append(os.Environ(), helperModeEnv+"=detached-holder")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv(helperPIDEnv)); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(0)
	case "detached-holder":
		if err := os.WriteFile(os.Getenv(helperPIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(8 * time.Second)
		os.Exit(0)
	case "late-leader":
		// A detached writer keeps the inherited stdout and is therefore not
		// reachable by the deadline's group kill; only closing the read ends can
		// disconnect it.
		writer := exec.Command(os.Args[0], "-test.run=^TestSubprocHelperProcess$")
		writer.Env = append(os.Environ(), helperModeEnv+"=late-writer")
		writer.Stdout, writer.Stderr = os.Stdout, os.Stderr
		writer.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := writer.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv(helperPIDEnv)); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "late-writer":
		// A Go program that writes to a broken stdout is killed by the runtime's
		// SIGPIPE handling. Ignore it so the fixture can report whether the late
		// write was refused, which is the observation this test needs.
		signal.Ignore(syscall.SIGPIPE)
		if err := os.WriteFile(os.Getenv(helperPIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		// Write after the test's deadline has certainly expired, then attempt a
		// clean exit: exactly the shape that must never become a result.
		time.Sleep(700 * time.Millisecond)
		_, writeErr := os.Stdout.WriteString(lateOutput)
		note := "write=ok"
		if writeErr != nil {
			note = "write=error:" + writeErr.Error()
		}
		_ = os.WriteFile(os.Getenv(helperWriteEnv), []byte(note), 0o600)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func waitForExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d survived the call", pid)
}

func killPID(t *testing.T, pid int) {
	t.Helper()
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func TestRunReportsExitStatusAndStreams(t *testing.T) {
	result, err := Run(context.Background(), Spec{
		Name: "/bin/sh",
		Args: []string{"-c", `printf out; printf err >&2; exit 3`},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Started {
		t.Error("Started = false")
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}
	if result.WaitErr == nil {
		t.Error("WaitErr = nil for a failing exit")
	}
	if string(result.Stdout) != "out" || string(result.Stderr) != "err" {
		t.Errorf("streams = %q/%q, want out/err", result.Stdout, result.Stderr)
	}
}

func TestRunSuccessIsNotAnError(t *testing.T) {
	result, err := Run(context.Background(), Spec{Name: "/bin/sh", Args: []string{"-c", "exit 0"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 || result.WaitErr != nil {
		t.Errorf("exit=%d wait=%v, want clean exit", result.ExitCode, result.WaitErr)
	}
}

func TestRunBoundsCaptureAndKeepsDraining(t *testing.T) {
	// The child writes more than the cap and then exits; the run must return
	// on its own, so the drain cannot stop at the cap.
	start := time.Now()
	result, err := Run(context.Background(), Spec{
		Name:      "/bin/sh",
		Args:      []string{"-c", `head -c 200000 /dev/zero | tr '\0' 'x'; printf 'tail' >&2`},
		StdoutCap: 16,
		StderrCap: 16,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Stdout) != 16 || !result.StdoutTruncated {
		t.Errorf("stdout=%d bytes truncated=%v, want 16/true", len(result.Stdout), result.StdoutTruncated)
	}
	if string(result.Stderr) != "tail" || result.StderrTruncated {
		t.Errorf("stderr=%q truncated=%v, want tail/false", result.Stderr, result.StderrTruncated)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("bounded capture did not keep draining: elapsed=%v", elapsed)
	}
}

func TestRunStdinAndEnvironment(t *testing.T) {
	result, err := Run(context.Background(), Spec{
		Name:  "/bin/sh",
		Args:  []string{"-c", `printf '%s:' "$HOLLIS_SUBPROC_TEST"; cat`},
		Env:   []string{"HOLLIS_SUBPROC_TEST=value"},
		Stdin: []byte("stdin"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(result.Stdout) != "value:stdin" {
		t.Errorf("stdout = %q", result.Stdout)
	}
}

func TestRunStdoutSinkReplacesCapture(t *testing.T) {
	var sink bytes.Buffer
	result, err := Run(context.Background(), Spec{
		Name:       "/bin/sh",
		Args:       []string{"-c", "printf sunk"},
		StdoutSink: &sink,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sink.String() != "sunk" {
		t.Errorf("sink = %q", sink.String())
	}
	if len(result.Stdout) != 0 {
		t.Errorf("capture = %q, want empty with a sink", result.Stdout)
	}
}

func TestRunDoesNotSpawnAfterExpiry(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Run(ctx, Spec{Name: "/bin/sh", Args: []string{"-c", "touch " + marker}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if result.Started || !result.Expired {
		t.Errorf("started=%v expired=%v, want false/true", result.Started, result.Expired)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("expired context still spawned the child")
	}
}

func TestRunReportsStartFailure(t *testing.T) {
	result, err := Run(context.Background(), Spec{Name: filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		t.Fatal("Run succeeded for a missing program")
	}
	if result.Started || result.StartErr == nil || result.Expired {
		t.Errorf("started=%v startErr=%v expired=%v", result.Started, result.StartErr, result.Expired)
	}
}

func TestRunDeadlineKillsProcessGroup(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "holder.pid")
	start := time.Now()
	result, err := Run(context.Background(), Spec{
		Name:    "/bin/sh",
		Args:    []string{"-c", "sleep 30 & echo $! > " + pidPath + "; sleep 30"},
		Timeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if !result.Expired || !result.Started {
		t.Errorf("expired=%v started=%v", result.Expired, result.Started)
	}
	if elapsed > 3*time.Second {
		t.Errorf("deadline did not bound the call: elapsed=%v", elapsed)
	}
	pid := readPID(t, pidPath)
	waitForExit(t, pid)
}

func TestRunDeadlineDisconnectsDetachedPipes(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "holder.pid")
	t.Setenv(helperModeEnv, "detached-leader")
	t.Setenv(helperPIDEnv, pidPath)
	start := time.Now()
	result, err := Run(context.Background(), Spec{
		Name:    os.Args[0],
		Args:    []string{"-test.run=^TestSubprocHelperProcess$"},
		Timeout: 500 * time.Millisecond,
	})
	elapsed := time.Since(start)
	pid := readPID(t, pidPath)
	t.Cleanup(func() { killPID(t, pid) })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Errorf("detached holder did not survive until observation: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if elapsed < 400*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Errorf("detached pipes escaped the deadline: elapsed=%v", elapsed)
	}
	if len(result.Stdout) != 0 {
		t.Errorf("partial output escaped the deadline: %q", result.Stdout)
	}
}

func TestRunSuccessKillsSurvivingGroupMembers(t *testing.T) {
	// The leader exits successfully while a background child stays in the
	// group but holds neither captured pipe, so only the post-exit group kill
	// can end it.
	result, err := Run(context.Background(), Spec{
		Name:    "/bin/sh",
		Args:    []string{"-c", `sleep 30 >/dev/null 2>&1 & echo $!`},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit = %d", result.ExitCode)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(result.Stdout)))
	if convErr != nil {
		t.Fatalf("holder pid = %q", result.Stdout)
	}
	waitForExit(t, pid)
}

func TestRunCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	result, err := Run(ctx, Spec{Name: "/bin/sh", Args: []string{"-c", "sleep 30"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if !result.Expired {
		t.Error("Expired = false for a canceled run")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("cancellation did not bound the call: elapsed=%v", elapsed)
	}
}

func TestScanDeliversLines(t *testing.T) {
	var lines []string
	var mu sync.Mutex
	result, err := Scan(context.Background(), Spec{
		Name: "/bin/sh",
		Args: []string{"-c", `printf 'one\ntwo\r\n'`},
	}, func(line []byte) error {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, string(line))
		return nil
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Stdout) != 0 {
		t.Errorf("Scan also captured stdout: %q", result.Stdout)
	}
	if strings.Join(lines, ",") != "one,two" {
		t.Errorf("lines = %q", lines)
	}
}

func TestScanCallbackErrorKillsTheChild(t *testing.T) {
	rejected := errors.New("synthetic protocol rejection")
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	start := time.Now()
	_, err := Scan(context.Background(), Spec{
		Name:    "/bin/sh",
		Args:    []string{"-c", "echo $$ > " + pidPath + "; echo bad; sleep 30; echo never"},
		Timeout: 10 * time.Second,
	}, func(line []byte) error {
		if strings.TrimSpace(string(line)) == "bad" {
			return rejected
		}
		return nil
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("err = %v, want the callback error verbatim", err)
	}
	// The fixture keeps writing for 30s, so ending promptly proves the run
	// disconnects on rejection instead of waiting for the pipe to close.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("callback rejection did not end the run: elapsed=%v", elapsed)
	}
	waitForExit(t, readPID(t, pidPath))
}

func TestScanReportsLineOverflow(t *testing.T) {
	// A line longer than the cap is reported, not invented into a read error.
	result, err := Scan(context.Background(), Spec{
		Name:    "/bin/sh",
		Args:    []string{"-c", `head -c 4096 /dev/zero | tr '\0' 'x'`},
		LineCap: 64,
	}, func([]byte) error { return nil })
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !result.LineOverflow {
		t.Error("LineOverflow = false for an oversized line")
	}
}

func TestRunBuilderOwnsCommandConstruction(t *testing.T) {
	spec := Spec{
		Name: "synthetic-program",
		Args: []string{"ignored"},
		Builder: func(_ context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.Command("/bin/sh", "-c", `printf '%s' "$HOLLIS_SUBPROC_BUILDER"`)
			cmd.Env = []string{"HOLLIS_SUBPROC_BUILDER=built"}
			return cmd
		},
	}
	result, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(result.Stdout) != "built" {
		t.Errorf("stdout = %q, want the builder's environment to survive", result.Stdout)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr != nil {
				t.Fatalf("invalid pid file %q: %v", data, convErr)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pid was recorded at %s", path)
	return 0
}

func TestRunIgnoresSIGTERMIgnoringChild(t *testing.T) {
	// Only the SIGKILL escalation can end a child that traps TERM; a
	// SIGTERM-only killer would hang past this bound.
	start := time.Now()
	_, err := Run(context.Background(), Spec{
		Name:    "/bin/sh",
		Args:    []string{"-c", `trap '' TERM; sleep 30`},
		Timeout: 300 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("SIGTERM-ignoring child outlived the escalation: elapsed=%v", elapsed)
	}
}

// A killed run cannot be rescued by output that arrives later. The fixture
// writes a complete-looking response after the deadline and then exits 0; the
// call must already have returned an error, and the late bytes must never reach
// the result.
func TestRunDiscardsLateOutputAfterDeadline(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "late.pid")
	writePath := filepath.Join(t.TempDir(), "late-write.txt")
	t.Setenv(helperModeEnv, "late-leader")
	t.Setenv(helperPIDEnv, pidPath)
	t.Setenv(helperWriteEnv, writePath)

	start := time.Now()
	result, err := Run(context.Background(), Spec{
		Name:    os.Args[0],
		Args:    []string{"-test.run=^TestSubprocHelperProcess$"},
		Timeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)

	writerPID := readPID(t, pidPath)
	t.Cleanup(func() { killPID(t, writerPID) })

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if !result.Expired {
		t.Error("Expired = false for a run that outlived its deadline")
	}
	if result.WaitErr == nil {
		t.Error("WaitErr = nil for a killed run")
	}
	if elapsed > 3*time.Second {
		t.Errorf("late output extended the call: elapsed=%v", elapsed)
	}

	// Wait for the writer to prove it really attempted the late write.
	var note string
	var writeObservedAt time.Time
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(writePath)
		if readErr == nil {
			note, writeObservedAt = string(data), time.Now()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if note == "" {
		t.Fatal("fixture never attempted its late write")
	}
	if since := writeObservedAt.Sub(start); since < 400*time.Millisecond {
		t.Fatalf("late write was observed at %v, before the deadline", since)
	}
	if strings.Contains(string(result.Stdout), "late") {
		t.Errorf("late output reached the result: %q", result.Stdout)
	}
	if strings.Contains(string(result.Stdout), "complete") {
		t.Errorf("late completion reached the result: %q", result.Stdout)
	}
	t.Logf("deadline=%v elapsed=%v late write outcome=%q captured=%q", 300*time.Millisecond, elapsed, note, result.Stdout)
}

func TestRunCancellationReapsDescendants(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "holder.pid")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	_, err := Run(ctx, Spec{
		Name: "/bin/sh",
		Args: []string{"-c", "sleep 30 & echo $! > " + pidPath + "; wait"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	waitForExit(t, readPID(t, pidPath))
}
