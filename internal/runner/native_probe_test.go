// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

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
	nativeProbeModeEnv = "HOLLIS_NATIVE_PROBE_MODE"
	nativeProbePIDEnv  = "HOLLIS_NATIVE_PROBE_PID"
)

// TestNativePlatformProbeHelper is an offline stand-in for sw_vers. The holder
// inherits the probe's stdout and outlives the leader: the platform check used
// to run without a process group, so that shape could block status and
// completion calls with nothing bounding it.
func TestNativePlatformProbeHelper(t *testing.T) {
	mode := os.Getenv(nativeProbeModeEnv)
	if mode == "" {
		t.Skip("offline fixture only")
	}
	switch mode {
	case "hang":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "early-exit":
		holder := exec.Command(os.Args[0], "-test.run=^TestNativePlatformProbeHelper$")
		holder.Env = append(os.Environ(), nativeProbeModeEnv+"=holder")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		if err := holder.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "holder":
		if err := os.WriteFile(os.Getenv(nativeProbePIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "current":
		_, _ = os.Stdout.WriteString("27.0\n")
		os.Exit(0)
	case "old":
		_, _ = os.Stdout.WriteString("26.5\n")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

// useNativeProbeStandIn substitutes the sw_vers stand-in. A zero timeout keeps
// the production budget: a race-enabled helper binary takes long enough to start
// that a test-scale budget would measure startup, not the probe's lifecycle.
func useNativeProbeStandIn(t *testing.T, mode, pidPath string, timeout time.Duration) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sw-vers-stand-in")
	script := "#!/bin/sh\n" +
		nativeProbeModeEnv + "='" + mode + "'\n" +
		nativeProbePIDEnv + "='" + pidPath + "'\n" +
		"export " + nativeProbeModeEnv + " " + nativeProbePIDEnv + "\n" +
		"exec \"" + os.Args[0] + "\" -test.run='^TestNativePlatformProbeHelper$'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previousPath, previousTimeout := swVersPath, platformProbeTimeout
	swVersPath = path
	if timeout > 0 {
		platformProbeTimeout = timeout
	}
	t.Cleanup(func() { swVersPath, platformProbeTimeout = previousPath, previousTimeout })
}

func TestNativePlatformProbeBoundsStandInPipes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native local platform gate is Apple Silicon only")
	}
	for _, mode := range []string{"hang", "early-exit"} {
		t.Run(mode, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "holder.pid")
			useNativeProbeStandIn(t, mode, pidPath, 300*time.Millisecond)
			start := time.Now()
			err := nativePlatform(context.Background())
			elapsed := time.Since(start)
			if err == nil || !strings.Contains(err.Error(), "could not determine macOS version") {
				t.Fatalf("err = %v, want a failed platform probe", err)
			}
			if elapsed > platformProbeTimeout+2*time.Second {
				t.Errorf("probe descendant extended the deadline: elapsed=%v budget=%v", elapsed, platformProbeTimeout)
			}
			if mode == "early-exit" {
				pid := readNativeProbePID(t, pidPath)
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if killErr := syscall.Kill(pid, 0); killErr != nil {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("probe descendant %d survived the group kill", pid)
			}
		})
	}
}

func TestNativePlatformProbeHonorsShortCallerDeadline(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native local platform gate is Apple Silicon only")
	}
	useNativeProbeStandIn(t, "hang", "", 0)
	platformProbeTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := nativePlatform(ctx); err == nil {
		t.Fatal("platform probe succeeded from a hanging stand-in")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("caller deadline did not bound the probe: elapsed=%v", elapsed)
	}
}

func TestNativePlatformProbeStillGatesOnMajorVersion(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native local platform gate is Apple Silicon only")
	}
	t.Run("current", func(t *testing.T) {
		useNativeProbeStandIn(t, "current", "", 0)
		if err := nativePlatform(context.Background()); err != nil {
			t.Fatalf("platform probe rejected a current version: %v", err)
		}
	})
	t.Run("old", func(t *testing.T) {
		useNativeProbeStandIn(t, "old", "", 0)
		err := nativePlatform(context.Background())
		if err == nil || !strings.Contains(err.Error(), "requires macOS 27 or later") {
			t.Fatalf("err = %v, want the version gate", err)
		}
	})
}

func readNativeProbePID(t *testing.T, path string) int {
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
