// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const (
	macosProbeModeEnv = "HOLLIS_MACOS_PROBE_MODE"
	macosProbePIDEnv  = "HOLLIS_MACOS_PROBE_PID"
)

// TestMacOSProbeHelper is an offline stand-in for sw_vers. doctor and setup
// used to call it with a raw exec.Command and no deadline at all, so a hang or a
// pipe-holding exit could block them indefinitely.
func TestMacOSProbeHelper(t *testing.T) {
	mode := os.Getenv(macosProbeModeEnv)
	if mode == "" {
		t.Skip("offline fixture only")
	}
	switch mode {
	case "hang":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "early-exit":
		holder := exec.Command(os.Args[0], "-test.run=^TestMacOSProbeHelper$")
		holder.Env = append(os.Environ(), macosProbeModeEnv+"=holder")
		holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
		if err := holder.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "holder":
		if err := os.WriteFile(os.Getenv(macosProbePIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "current":
		_, _ = os.Stdout.WriteString("27.1\n")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

// useMacOSProbeStandIn substitutes the sw_vers stand-in. A zero timeout keeps
// the production budget: a race-enabled helper binary takes long enough to start
// that a test-scale budget would measure startup, not the probe's lifecycle.
func useMacOSProbeStandIn(t *testing.T, mode, pidPath string, timeout time.Duration) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sw-vers-stand-in")
	script := "#!/bin/sh\n" +
		macosProbeModeEnv + "='" + mode + "'\n" +
		macosProbePIDEnv + "='" + pidPath + "'\n" +
		"export " + macosProbeModeEnv + " " + macosProbePIDEnv + "\n" +
		"exec \"" + os.Args[0] + "\" -test.run='^TestMacOSProbeHelper$'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previousPath, previousTimeout := swVersPath, macosProbeTimeout
	swVersPath = path
	if timeout > 0 {
		macosProbeTimeout = timeout
	}
	t.Cleanup(func() { swVersPath, macosProbeTimeout = previousPath, previousTimeout })
}

func TestMacOSProbesBoundStandInPipes(t *testing.T) {
	probes := map[string]func() string{
		"productVersion": macosVersion,
		"buildVersion":   macosBuild,
	}
	for name, probe := range probes {
		for _, mode := range []string{"hang", "early-exit"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				pidPath := filepath.Join(t.TempDir(), "holder.pid")
				useMacOSProbeStandIn(t, mode, pidPath, 300*time.Millisecond)
				start := time.Now()
				value := probe()
				elapsed := time.Since(start)
				if value != "" {
					t.Fatalf("probe = %q, want the bounded unknown result", value)
				}
				if elapsed > macosProbeTimeout+2*time.Second {
					t.Errorf("probe descendant extended the deadline: elapsed=%v budget=%v", elapsed, macosProbeTimeout)
				}
				if mode == "early-exit" {
					waitForProbeExit(t, readProbePID(t, pidPath))
				}
			})
		}
	}
}

func TestMacOSProbeReadsBoundedStandInOutput(t *testing.T) {
	useMacOSProbeStandIn(t, "current", "", 0)
	if value := macosVersion(); value != "27.1" {
		t.Errorf("macosVersion() = %q, want the trimmed stand-in value", value)
	}
}
