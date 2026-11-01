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
	runtimeRecordPipeModeEnv = "HOLLIS_RECORD_PIPE_MODE"
	runtimeRecordPipePIDEnv  = "HOLLIS_RECORD_PIPE_PID"
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
