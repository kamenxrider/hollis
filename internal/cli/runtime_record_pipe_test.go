// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	runtimeRecordPipeModeEnv  = "HOLLIS_RECORD_PIPE_MODE"
	runtimeRecordPipePIDEnv   = "HOLLIS_RECORD_PIPE_PID"
	runtimeRecordPipeGroupEnv = "HOLLIS_RECORD_PIPE_GROUP_PID"
)

// TestRuntimeRecordPipeHelper is an offline fixture: the producer stand-in. The
// holder inherits the producer's stdout and keeps it open after the leader
// exits, which is the shape that used to outlive the verification budget.
func TestRuntimeRecordPipeHelper(t *testing.T) {
	mode := os.Getenv(runtimeRecordPipeModeEnv)
	if mode == "" {
		t.Skip("offline fixture only")
	}
	switch mode {
	case "hang":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "early-exit":
		holder := exec.Command(os.Args[0], "-test.run=^TestRuntimeRecordPipeHelper$")
		holder.Env = append(os.Environ(), runtimeRecordPipeModeEnv+"=holder")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		if err := holder.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "late-exit":
		// Two descendants, two claims. The detached holder keeps stdout open
		// after the leader exits and is out of the producer's group, so only
		// closing the read ends can bound the wait on it. The in-group child
		// must be killed by the deadline's group kill.
		holder := exec.Command(os.Args[0], "-test.run=^TestRuntimeRecordPipeHelper$")
		holder.Env = append(os.Environ(), runtimeRecordPipeModeEnv+"=holder")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := holder.Start(); err != nil {
			os.Exit(3)
		}
		group := exec.Command(os.Args[0], "-test.run=^TestRuntimeRecordPipeHelper$")
		group.Env = append(os.Environ(), runtimeRecordPipeModeEnv+"=group-member",
			runtimeRecordPipeGroupEnv+"="+os.Getenv(runtimeRecordPipeGroupEnv))
		group.Stdout, group.Stderr = os.Stdout, os.Stderr
		if err := group.Start(); err != nil {
			os.Exit(3)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_, holderErr := os.Stat(os.Getenv(runtimeRecordPipePIDEnv))
			_, groupErr := os.Stat(os.Getenv(runtimeRecordPipeGroupEnv))
			if holderErr == nil && groupErr == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond)
		os.Exit(0)
	case "group-member":
		if err := os.WriteFile(os.Getenv(runtimeRecordPipeGroupEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "holder":
		pidPath := os.Getenv(runtimeRecordPipePIDEnv)
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "valid":
		_, _ = os.Stdout.WriteString(`{"contract_version":1,"state":"action_required","reason_code":"helper_missing"}`)
		os.Exit(10)
	default:
		os.Exit(2)
	}
}

// writeProbeStandIn writes a producer stand-in that re-executes the test binary
// in the fixture mode named by modeEnv. Extra arguments the caller supplies are
// deliberately dropped: the stand-in ignores its own flag surface.
func writeProbeStandIn(t *testing.T, helper string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stand-in")
	script := "#!/bin/sh\nexec \"" + os.Args[0] + "\" -test.run='^" + helper + "$'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func readProbePID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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

func waitForProbeExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived the bounded probe", pid)
}

func TestReadRuntimeRecordBoundsPipeHolder(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("producer stand-in mirrors the macOS-only runtime record")
	}
	previous := runtimeRecordTimeout
	runtimeRecordTimeout = 300 * time.Millisecond
	t.Cleanup(func() { runtimeRecordTimeout = previous })

	for _, mode := range []string{"hang", "early-exit"} {
		t.Run(mode, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "holder.pid")
			t.Setenv(runtimeRecordPipeModeEnv, mode)
			t.Setenv(runtimeRecordPipePIDEnv, pidPath)
			producer := writeProbeStandIn(t, "TestRuntimeRecordPipeHelper")
			start := time.Now()
			record, err := readRuntimeRecord(context.Background(), producer)
			elapsed := time.Since(start)
			if err == nil || record.State == "verified" {
				t.Fatalf("record=%+v err=%v, want a fail-closed verification", record, err)
			}
			if elapsed > runtimeRecordTimeout+2*time.Second {
				t.Errorf("producer pipe holder extended verification: elapsed=%v budget=%v", elapsed, runtimeRecordTimeout)
			}
			if mode == "early-exit" {
				waitForProbeExit(t, readProbePID(t, pidPath))
			}
		})
	}
}

// A producer that exits just before the budget expires while a descendant keeps
// stdout open is the late-leader shape. The run must end at the deadline the
// caller set, not at the deadline plus a cleanup interval, and the fail-closed
// classification must not depend on how long the descendant lived.
func TestReadRuntimeRecordLateLeaderReturnsAtDeadline(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("producer stand-in mirrors the macOS-only runtime record")
	}
	previous := runtimeRecordTimeout
	runtimeRecordTimeout = 300 * time.Millisecond
	t.Cleanup(func() { runtimeRecordTimeout = previous })

	pidPath := filepath.Join(t.TempDir(), "holder.pid")
	groupPath := filepath.Join(t.TempDir(), "group.pid")
	t.Setenv(runtimeRecordPipeModeEnv, "late-exit")
	t.Setenv(runtimeRecordPipePIDEnv, pidPath)
	t.Setenv(runtimeRecordPipeGroupEnv, groupPath)
	producer := writeProbeStandIn(t, "TestRuntimeRecordPipeHelper")
	start := time.Now()
	record, err := readRuntimeRecord(context.Background(), producer)
	elapsed := time.Since(start)
	if err == nil || record.State == "verified" {
		t.Fatalf("record=%+v err=%v, want a fail-closed verification", record, err)
	}
	// The leader exits at 150ms while the detached holder would keep the pipe
	// open for 30s. Anything past the budget plus a small scheduling allowance
	// means the run waited on a cleanup interval rather than on the deadline.
	if elapsed < runtimeRecordTimeout-100*time.Millisecond || elapsed > runtimeRecordTimeout+500*time.Millisecond {
		t.Errorf("late leader did not return at the deadline: elapsed=%v budget=%v", elapsed, runtimeRecordTimeout)
	}
	// The deadline's group kill covers every descendant that stayed in the
	// producer's group.
	waitForProbeExit(t, readProbePID(t, groupPath))
	// A holder that left the group survives by design: the deadline disconnects
	// the pipe instead of signalling it. Record that honestly rather than
	// claiming a kill this layer does not perform.
	holderPID := readProbePID(t, pidPath)
	if killErr := syscall.Kill(holderPID, 0); killErr != nil {
		t.Logf("detached holder %d exited on its own: %v", holderPID, killErr)
	} else {
		t.Logf("detached holder %d survives the call, as documented", holderPID)
	}
	t.Cleanup(func() { _ = syscall.Kill(holderPID, syscall.SIGKILL) })
}

func TestReadRuntimeRecordStillAcceptsBoundedProducer(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("producer stand-in mirrors the macOS-only runtime record")
	}
	previous := runtimeRecordTimeout
	runtimeRecordTimeout = 5 * time.Second
	t.Cleanup(func() { runtimeRecordTimeout = previous })
	t.Setenv(runtimeRecordPipeModeEnv, "valid")
	record, err := readRuntimeRecord(context.Background(), writeProbeStandIn(t, "TestRuntimeRecordPipeHelper"))
	if err != nil {
		t.Fatalf("readRuntimeRecord: %v", err)
	}
	if record.State != "action_required" || record.ContractVersion != 1 {
		t.Fatalf("record = %+v", record)
	}
}
