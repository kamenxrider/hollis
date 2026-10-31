// Regression tests for the `shortcuts list` bounds (wave 2, "Gap A").
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
// Three named properties (plans/subprocess-lifecycle-design.md §6):
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

// listGrandchildSleep exceeds the list deadline (10s) plus the fix's
// WaitDelay (2s), so on the unfixed code the call returns only at the
// descendant's exit and the timing assertion can tell deadline-bound returns
// from descendant-bound ones. The fixed finite value is what bounds the
// observation: the test cannot hang forever even against the defect.
const listGrandchildSleep = 16 * time.Second

// listReturnBound is the property bound: deadline (10s) + WaitDelay (2s) + 2s
// scheduler slack. It mirrors the fix's constants; see the comment above.
const listReturnBound = 14 * time.Second

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
		cmd := exec.Command(os.Args[0], "-test.run=^TestListFixture$")
		env := append(os.Environ(), "GAPFIX_MODE=list-grandchild")
		if pf := os.Getenv("GAPFIX_PIDFILE"); pf != "" {
			env = append(env, "GAPFIX_PIDFILE="+pf)
		}
		cmd.Env = env
		cmd.Stdout = os.Stdout // the grandchild inherits the pipe
		if err := cmd.Start(); err != nil {
			t.Fatalf("spawn grandchild: %v", err)
		}
		os.Exit(0)
	case "list-grandchild":
		if pf := os.Getenv("GAPFIX_PIDFILE"); pf != "" {
			if err := os.WriteFile(pf, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
				t.Fatalf("write pidfile: %v", err)
			}
		}
		time.Sleep(listGrandchildSleep)
	}
}

func TestListShortcutsDeadlineBoundsDescendants(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "grandchild.pid")
	// ListShortcuts spawns the wrapper with the test process's env; both
	// vars must be present here for the fixture chain to activate.
	t.Setenv("GAPFIX_MODE", "list-child")
	t.Setenv("GAPFIX_PIDFILE", pf)
	r := &ShortcutRunner{ShortcutsPath: listFixturePath(t)}

	start := time.Now()
	names, err := r.ListShortcuts(context.Background())
	elapsed := time.Since(start)

	t.Cleanup(func() {
		if data, readErr := os.ReadFile(pf); readErr == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	t.Logf("ListShortcuts returned after %v (deadline 10s; grandchild holds stdout for %v)", elapsed, listGrandchildSleep)
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

	// NO ABSENCE CLAIM: a deadline must never be representable as a
	// completed (possibly empty) listing — absence may only be claimed by
	// a listing that actually completed.
	if names != nil {
		t.Errorf("NO ABSENCE CLAIM violated: deadline returned names=%v; a timed-out listing must not look like a completed one", names)
	}
}
