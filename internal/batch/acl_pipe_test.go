// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

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
	aclPipeModeEnv = "HOLLIS_ACL_PIPE_MODE"
	aclPipePIDEnv  = "HOLLIS_ACL_PIPE_PID"
)

// TestACLPipeHelper is an offline stand-in for /bin/ls -ldbe. The holder
// inherits the inspector's stdout and outlives the leader, which is what used
// to keep the fail-closed inspection blocked after its deadline.
func TestACLPipeHelper(t *testing.T) {
	mode := os.Getenv(aclPipeModeEnv)
	if mode == "" {
		t.Skip("offline fixture only")
	}
	switch mode {
	case "hang":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "early-exit":
		holder := exec.Command(os.Args[0], "-test.run=^TestACLPipeHelper$")
		holder.Env = append(os.Environ(), aclPipeModeEnv+"=holder")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		if err := holder.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "holder":
		if err := os.WriteFile(os.Getenv(aclPipePIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "clean":
		_, _ = os.Stdout.WriteString("drwx------  2 synthetic  staff  64 Jan  1 00:00 /tmp/synthetic\n")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

// writeACLStandIn writes the /bin/ls stand-in. The production ACL command runs
// with a deliberately minimal environment, so the fixture mode travels inside
// the script instead of the parent environment.
func writeACLStandIn(t *testing.T, mode, pidPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acl-stand-in")
	script := "#!/bin/sh\n" +
		aclPipeModeEnv + "='" + mode + "'\n" +
		aclPipePIDEnv + "='" + pidPath + "'\n" +
		"export " + aclPipeModeEnv + " " + aclPipePIDEnv + "\n" +
		"exec \"" + os.Args[0] + "\" -test.run='^TestACLPipeHelper$'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := aclInspectionBinary
	aclInspectionBinary = path
	t.Cleanup(func() { aclInspectionBinary = previous })
	return path
}

func TestACLInspectionBoundsPipeHolder(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ACL inspection is macOS-only")
	}
	for _, mode := range []string{"hang", "early-exit"} {
		t.Run(mode, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "holder.pid")
			writeACLStandIn(t, mode, pidPath)
			start := time.Now()
			err := requireSafeLockACL(context.Background(), filepath.Join(t.TempDir(), "job.lock"), "lock file")
			elapsed := time.Since(start)
			if err == nil || !strings.Contains(err.Error(), "inspect lock file ACL") {
				t.Fatalf("err = %v, want a fail-closed ACL inspection error", err)
			}
			if elapsed > aclInspectionTimeout+2*time.Second {
				t.Errorf("inspector descendant extended the deadline: elapsed=%v budget=%v", elapsed, aclInspectionTimeout)
			}
			if mode == "early-exit" {
				pid := readACLPID(t, pidPath)
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if killErr := syscall.Kill(pid, 0); killErr != nil {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("inspector descendant %d survived the group kill", pid)
			}
		})
	}
}

func TestACLInspectionAcceptsBoundedStandIn(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ACL inspection is macOS-only")
	}
	writeACLStandIn(t, "clean", "")
	if err := requireSafeLockACL(context.Background(), filepath.Join(t.TempDir(), "job.lock"), "lock file"); err != nil {
		t.Fatalf("bounded stand-in listing was rejected: %v", err)
	}
}

func readACLPID(t *testing.T, path string) int {
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
