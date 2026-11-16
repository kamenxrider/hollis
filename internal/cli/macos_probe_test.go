// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"os"
	"path/filepath"
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
	if mode == "early-exit" {
		path = writeEarlyExitStandIn(t, pidPath)
	} else {
		script := "#!/bin/sh\n" +
			macosProbeModeEnv + "='" + mode + "'\n" +
			macosProbePIDEnv + "='" + pidPath + "'\n" +
			"export " + macosProbeModeEnv + " " + macosProbePIDEnv + "\n" +
			"exec \"" + os.Args[0] + "\" -test.run='^TestMacOSProbeHelper$'\n"
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
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
				budget := 300 * time.Millisecond
				if mode == "early-exit" {
					// The leader must record its holder before the deadline kills
					// the group; under CPU load even a shell can need more than
					// 300ms. The holder still outlives this budget many times over.
					budget = 2 * time.Second
				}
				useMacOSProbeStandIn(t, mode, pidPath, budget)
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
