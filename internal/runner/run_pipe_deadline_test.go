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

const runPipeDeadline = 4 * time.Second

func TestRunPipeDeadlineFixture(t *testing.T) {
	switch os.Getenv("RUN_PIPE_DEADLINE_MODE") {
	case "":
		t.Skip("offline fixture only")
	case "leader":
		holder := exec.Command(os.Args[0], "-test.run=^TestRunPipeDeadlineFixture$")
		holder.Env = append(os.Environ(), "RUN_PIPE_DEADLINE_MODE=holder")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		delay, err := time.ParseDuration(os.Getenv("RUN_PIPE_DEADLINE_LEADER_DELAY"))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(delay)
		_, _ = os.Stdout.WriteString("BEFORE")
		os.Exit(0)
	case "holder":
		if err := os.WriteFile(os.Getenv("RUN_PIPE_DEADLINE_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		// Finite even against broken deadline handling. The holder is outside
		// the leader's group, so group SIGKILL cannot close its pipe ends.
		time.Sleep(8 * time.Second)
		os.Exit(0)
	}
}

func TestRunDeadlineOwnsDetachedPipes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		delay time.Duration
	}{
		{"late-leader", runPipeDeadline - 500*time.Millisecond},
		{"early-leader", 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pidfile := filepath.Join(dir, "holder.pid")
			t.Setenv("RUN_PIPE_DEADLINE_MODE", "leader")
			t.Setenv("RUN_PIPE_DEADLINE_PIDFILE", pidfile)
			t.Setenv("RUN_PIPE_DEADLINE_LEADER_DELAY", tc.delay.String())
			t.Cleanup(func() {
				data, err := os.ReadFile(pidfile)
				if err != nil {
					return
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
				if err == nil && pid > 0 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			wrapper := filepath.Join(dir, "run-shortcuts")
			script := "#!/bin/sh\nexec \"" + os.Args[0] + "\" -test.run='^TestRunPipeDeadlineFixture$'\n"
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			r := &ShortcutRunner{
				ShortcutsPath: wrapper,
				BridgeRefs:    map[Model]string{ModelCloud: "synthetic-pipe-deadline"},
				Timeout:       runPipeDeadline,
			}
			start := time.Now()
			response, err := r.runTierWithImages(context.Background(), ModelCloud, "synthetic pipe deadline fixture", nil)
			elapsed := time.Since(start)
			data, readErr := os.ReadFile(pidfile)
			if readErr != nil {
				t.Fatalf("holder did not record its PID: %v", readErr)
			}
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil || pid <= 0 {
				t.Fatalf("invalid holder PID: %q", data)
			}
			if err := syscall.Kill(pid, 0); err != nil {
				t.Errorf("detached holder did not survive until observation: %v", err)
			}
			t.Logf("leader delay=%v budget=%v elapsed=%v response bytes=%d err=%v", tc.delay, runPipeDeadline, elapsed, len(response), err)

			// A successful leader is not a completed response while a holder
			// retains its pipe. Neither a fresh cleanup interval after exit nor
			// an early WaitDelay failure is the deadline-owned pipe contract.
			if elapsed > runPipeDeadline+500*time.Millisecond {
				t.Errorf("pipe holder extended the budget: elapsed=%v bound=%v", elapsed, runPipeDeadline+500*time.Millisecond)
			}
			if elapsed < runPipeDeadline-100*time.Millisecond {
				t.Errorf("pipe handling ended before the deadline: elapsed=%v budget=%v", elapsed, runPipeDeadline)
			}
			var re *Error
			if !errors.As(err, &re) || re.Kind != KindTimeout {
				t.Errorf("outcome=%v, want KindTimeout", err)
			}
			if response != "" {
				t.Errorf("incomplete response escaped the deadline: %q", response)
			}
		})
	}
}
