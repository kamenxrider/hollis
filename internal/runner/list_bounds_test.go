// Regression tests for bounded `shortcuts list` subprocess behavior.
//
// Defect, reproduced on unmodified main: ListShortcuts applies a fixed 10s
// deadline but sets no process group and no WaitDelay, and classifies the
// deadline only inside the error branch of .Output(). A direct child that
// exits 0 while a pipe-holding descendant keeps the stdout write end open
// blocks .Output() past the deadline and then returns err == nil, so the call
// ends in silent success carrying bytes written after the deadline —
// "deadline exceeded yet reported success, with descendant-written bytes
// treated as results".
//
// Three properties:
//
//	TIMING — returns within the bound when a descendant holds the pipe.
//	NO FABRICATED SUCCESS — the deadline yields the timeout kind, never
//	    err = nil plus bytes.
//	NO ABSENCE CLAIM — the deadline outcome is never representable as a
//	    completed (possibly empty) listing.
//
// Fixtures are offline self-re-execs of the test binary plus a generated
// wrapper script; /usr/bin/shortcuts is never invoked. The descendant's sleep
// is a fixed finite value, so the test itself is bounded even against the
// unfixed defect.
package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// listGrandchildSleep is finite so the observation remains bounded even
// against a broken process-group kill. The list call must not wait for it.
const listGrandchildSleep = 16 * time.Second

// listReturnBound is the context deadline plus scheduler slack. WaitDelay is
// also set to the deadline as a fallback, but a separate group killer must
// stop descendants at the context deadline instead of waiting another
// deadline-sized interval after the leader exits.
const listReturnBound = listDeadline + 2*time.Second

func listFixturePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "list-shortcuts")
	script := "#!/bin/sh\nexec \"" + os.Args[0] + "\" -test.run='^TestListFixture$' list\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write list wrapper: %v", err)
	}
	return path
}

// TestListFixture is the fixture entrypoint selected by GAPFIX_MODE. Without
// the env var it skips, so normal suite runs are unaffected.
func TestListFixture(t *testing.T) {
	switch os.Getenv("GAPFIX_MODE") {
	case "":
		t.Skip("fixture only")
	case "list-child":
		// The list shape: a direct child that exits 0 quickly after
		// spawning a grandchild that inherits the stdout write end.
		spawnListGrandchild(t, "list-grandchild", false, listGrandchildSleep)
		os.Exit(0)
	case "list-late-leader":
		// The leader stays alive for 8s, then exits 0. WaitDelay starts
		// only after that exit; the descendant keeps stdout open longer.
		spawnListGrandchild(t, "list-grandchild", false, 30*time.Second)
		time.Sleep(8 * time.Second)
		os.Exit(0)
	case "list-hang-group":
		// Both leader and grandchild remain alive. The grandchild holds
		// stdout, so the process-group cancellation must stop both.
		writeFixturePID(t, os.Getenv("GAPFIX_LEADER_PIDFILE"), os.Getpid())
		spawnListGrandchild(t, "list-grandchild", false, 30*time.Second)
		time.Sleep(30 * time.Second)
	case "list-late-output":
		// Isolate the writer from the process group so this test exercises
		// the post-success context check independently of group killing.
		spawnListGrandchild(t, "list-fake-grandchild", true, 11*time.Second)
		time.Sleep(3 * time.Second)
		os.Exit(0)
	case "list-detached-hold":
		// A detached pipe-holder exercises WaitDelay itself. It is
		// deliberately outside the group, and exits after a finite time.
		spawnListGrandchild(t, "list-grandchild", true, listGrandchildSleep)
		os.Exit(0)
	case "list-grandchild":
		writeFixturePID(t, os.Getenv("GAPFIX_PIDFILE"), os.Getpid())
		sleep := listGrandchildSleep
		if raw := os.Getenv("GAPFIX_SLEEP"); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil {
				t.Fatalf("parse GAPFIX_SLEEP: %v", err)
			}
			sleep = parsed
		}
		time.Sleep(sleep)
	case "list-fake-grandchild":
		time.Sleep(11 * time.Second)
		_, _ = os.Stdout.WriteString("FAKE\n")
		// The fixture's pipe lifetime ends with its write, not with the
		// race detector's additional process-shutdown delay.
		_ = os.Stdout.Close()
		os.Exit(0)
	}
}

func spawnListGrandchild(t *testing.T, mode string, detached bool, sleep time.Duration) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestListFixture$")
	env := fixtureEnv("GAPFIX_MODE", mode)
	env = fixtureEnvFrom(env, "GAPFIX_SLEEP", sleep.String())
	if pf := os.Getenv("GAPFIX_PIDFILE"); pf != "" {
		env = fixtureEnvFrom(env, "GAPFIX_PIDFILE", pf)
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	if detached {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn grandchild: %v", err)
	}
}

func fixtureEnv(key, value string) []string {
	return fixtureEnvFrom(os.Environ(), key, value)
}

func fixtureEnvFrom(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func writeFixturePID(t *testing.T, path string, pid int) {
	t.Helper()
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatalf("write pidfile %s: %v", path, err)
	}
}

func fixturePID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fixture never recorded PID at %s", path)
	return 0
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func cleanupFixturePID(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

func assertProcessExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("synthetic process %d survived ListShortcuts return", pid)
}

func TestListShortcutsDeadlineBoundsDescendants(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "grandchild.pid")
	// ListShortcuts spawns the wrapper with the test process's env; both
	// vars must be present here for the fixture chain to activate.
	t.Setenv("GAPFIX_MODE", "list-child")
	t.Setenv("GAPFIX_PIDFILE", pf)
	cleanupFixturePID(t, pf)
	r := &ShortcutRunner{ShortcutsPath: listFixturePath(t)}

	start := time.Now()
	names, err := r.ListShortcuts(context.Background())
	elapsed := time.Since(start)

	t.Logf("ListShortcuts returned after %v (deadline %v; grandchild holds stdout for %v)", elapsed, listDeadline, listGrandchildSleep)
	t.Logf("result: names=%v err=%v", names, err)

	// TIMING: the call must return within the deadline bound even when a
	// descendant holds every pipe.
	if elapsed > listReturnBound {
		t.Errorf("TIMING violated: ListShortcuts returned after %v (bound %v); the return was bounded by the descendant, not the deadline", elapsed, listReturnBound)
	}

	// NO FABRICATED SUCCESS: after the deadline expired there must be no
	// successful listing — the direct-child-exits-0-with-descendant-
	// holding-pipe case must yield the timeout kind, not err == nil.
	if err == nil {
		t.Fatalf("NO FABRICATED SUCCESS violated: the deadline expired (a descendant held stdout for %v) yet ListShortcuts returned success with names=%v", listGrandchildSleep, names)
	}
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("NO FABRICATED SUCCESS violated: deadline error is not a runner *Error: %T", err)
	}
	if re.Kind != KindTimeout {
		t.Errorf("NO FABRICATED SUCCESS violated: deadline classified as %v, want KindTimeout", re.Kind)
	} else if !strings.Contains(re.Error(), "did not complete within") || !strings.Contains(re.Error(), "no bridge is proven missing") {
		t.Errorf("deadline message must be the approved fixed sentence, got: %s", re.Error())
	}
	if !re.ListTimeout {
		t.Error("ListTimeout marker is false; CLI may append the model-timeout hint")
	}

	// NO ABSENCE CLAIM: a deadline must never be representable as a
	// completed (possibly empty) listing — absence may only be claimed by
	// a listing that actually completed.
	if names != nil {
		t.Errorf("NO ABSENCE CLAIM violated: deadline returned names=%v; a timed-out listing must not look like a completed one", names)
	}

	// Assert before cleanup that the early-exit leader did not leave its
	// pipe-holding grandchild alive. The cleanup is only a safety net after
	// this assertion, not a substitute for it.
	pid := fixturePID(t, pf)
	time.Sleep(200 * time.Millisecond)
	if processExists(pid) {
		t.Errorf("process-group member %d survived the list deadline and return", pid)
	}
}

func TestListShortcutsBoundsLateLeaderExit(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Setenv("GAPFIX_MODE", "list-late-leader")
	t.Setenv("GAPFIX_PIDFILE", pf)
	cleanupFixturePID(t, pf)
	start := time.Now()
	names, err := (&ShortcutRunner{ShortcutsPath: listFixturePath(t)}).ListShortcuts(context.Background())
	elapsed := time.Since(start)
	t.Logf("late leader returned after %v (deadline %v, leader exits at 8s), names=%v err=%v", elapsed, listDeadline, names, err)
	if elapsed > listReturnBound {
		t.Errorf("late leader exceeded deadline bound: elapsed=%v bound=%v", elapsed, listReturnBound)
	}
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout || !re.ListTimeout {
		t.Errorf("late leader outcome=%v, want marked KindTimeout", err)
	}
	pid := fixturePID(t, pf)
	assertProcessExited(t, pid)
}

func TestListShortcutsKillsGroupWhenLeaderAlsoHangs(t *testing.T) {
	grandchildPath := filepath.Join(t.TempDir(), "grandchild.pid")
	leaderPath := filepath.Join(t.TempDir(), "leader.pid")
	t.Setenv("GAPFIX_MODE", "list-hang-group")
	t.Setenv("GAPFIX_PIDFILE", grandchildPath)
	t.Setenv("GAPFIX_LEADER_PIDFILE", leaderPath)
	cleanupFixturePID(t, grandchildPath)
	cleanupFixturePID(t, leaderPath)
	start := time.Now()
	names, err := (&ShortcutRunner{ShortcutsPath: listFixturePath(t)}).ListShortcuts(context.Background())
	elapsed := time.Since(start)
	t.Logf("hanging group returned after %v (deadline %v), names=%v err=%v", elapsed, listDeadline, names, err)
	if elapsed > listReturnBound {
		t.Errorf("hanging process group exceeded deadline bound: elapsed=%v bound=%v", elapsed, listReturnBound)
	}
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout || !re.ListTimeout || names != nil {
		t.Errorf("hanging group outcome names=%v err=%v, want no names and marked KindTimeout", names, err)
	}
	grandchildPID := fixturePID(t, grandchildPath)
	leaderPID := fixturePID(t, leaderPath)
	assertProcessExited(t, grandchildPID)
	assertProcessExited(t, leaderPID)
}

func TestListShortcutsDiscardsOutputWrittenAfterDeadline(t *testing.T) {
	t.Setenv("GAPFIX_MODE", "list-late-output")
	start := time.Now()
	names, err := (&ShortcutRunner{ShortcutsPath: listFixturePath(t)}).ListShortcuts(context.Background())
	elapsed := time.Since(start)
	t.Logf("late output returned after %v (deadline %v), names=%v err=%v", elapsed, listDeadline, names, err)
	if elapsed > listReturnBound {
		t.Errorf("late output exceeded deadline bound: elapsed=%v bound=%v", elapsed, listReturnBound)
	}
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout || !re.ListTimeout {
		t.Errorf("late output outcome=%v, want marked KindTimeout", err)
	}
	if names != nil {
		t.Errorf("late descendant output fabricated a listing: %v", names)
	}
}

func TestListShortcutsCallerDeadlineIsReflectedInMessage(t *testing.T) {
	t.Setenv("GAPFIX_MODE", "list-hang-group")
	r := &ShortcutRunner{ShortcutsPath: listFixturePath(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	names, err := r.ListShortcuts(ctx)
	elapsed := time.Since(start)
	t.Logf("short caller deadline returned after %v, names=%v err=%v", elapsed, names, err)
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout || !re.ListTimeout {
		t.Fatalf("caller deadline outcome=%v, want marked KindTimeout", err)
	}
	// Time spent entering ListShortcuts can reduce the remaining budget.
	// Require a subsecond message, rather than an exact scheduling-dependent
	// duration, and verify the fixed 300ms rendering directly below.
	if !strings.Contains(re.Error(), "ms and was stopped") || strings.Contains(re.Error(), "10 seconds") {
		t.Errorf("message does not reflect the shorter caller deadline: %q", re.Error())
	}
	if names != nil {
		t.Errorf("short-deadline listing returned names: %v", names)
	}
}

func TestListDeadlineErrorSetsListTimeoutMarker(t *testing.T) {
	re := listDeadlineError(listDeadline)
	if re.Kind != KindTimeout || !re.ListTimeout {
		t.Fatalf("deadline error = %+v, want KindTimeout with ListTimeout marker", re)
	}
	const want = "Listing Shortcuts did not complete within 10 seconds and was stopped; no bridge is proven missing. The underlying cause is unconfirmed."
	if got := re.Error(); got != want {
		t.Fatalf("deadline message = %q, want exactly %q", got, want)
	}
	if got := listDeadlineError(300 * time.Millisecond).Error(); !strings.Contains(got, "within 300ms") {
		t.Fatalf("short deadline message = %q, want 300ms budget", got)
	}
}

func TestListShortcutsWaitDelayBoundsDetachedPipeHolder(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Setenv("GAPFIX_MODE", "list-detached-hold")
	t.Setenv("GAPFIX_PIDFILE", pf)
	cleanupFixturePID(t, pf)
	start := time.Now()
	names, err := (&ShortcutRunner{ShortcutsPath: listFixturePath(t)}).ListShortcuts(context.Background())
	elapsed := time.Since(start)
	t.Logf("detached pipe-holder returned after %v (deadline %v; holder exits at %v), names=%v err=%v", elapsed, listDeadline, listGrandchildSleep, names, err)
	if elapsed > listReturnBound {
		t.Errorf("WaitDelay failed to bound detached pipe-holder: elapsed=%v bound=%v", elapsed, listReturnBound)
	}
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout {
		t.Errorf("detached pipe-holder outcome=%v, want KindTimeout", err)
	}
}
