// Regression test for the run path's capture bound (wave 2, "Gap B").
//
// Defect, measured on unmodified main: runTierWithImages captures stdout and
// stderr in unbounded bytes.Buffers (shortcut.go:445-447), so a flooding
// child grows parent memory without bound until the deadline kill. Measured:
// 514.5 MiB of parent RSS growth in 4 s under a ~40 MiB/s flood across both
// streams (base 6.5 → peak 521.0 MiB), with deadline classification otherwise
// correct.
//
// Property pinned here (plans/subprocess-lifecycle-design.md §4, property 2):
//
//	BOUNDED CAPTURE — parent memory does not grow with child volume.
//
// Fixture: offline self-re-exec of the test binary via a wrapper script
// (the run path spawns ShortcutsPath with `run <ref> …` arguments);
// /usr/bin/shortcuts is never invoked. RSS is a proxy measurement — it is
// asserted in non-race runs only, because the race detector's allocator
// inflates both baseline and transients.
package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// floodRateDelay paces the flood at ~40 MiB/s across both streams.
const floodRateDelay = 50 * time.Millisecond

// captureBoundMiB is the RSS proxy bound: two capped 1 MiB streams plus
// allocator and sampling slack.
const captureBoundMiB = 8.0

// TestSubprocFixture is the shared fixture entrypoint selected by
// SUBPROCFIX_MODE. Without the env var it skips, so normal suite runs are
// unaffected.
func TestSubprocFixture(t *testing.T) {
	switch os.Getenv("SUBPROCFIX_MODE") {
	case "":
		t.Skip("fixture only")
	case "flood":
		chunk := make([]byte, 1<<20)
		for i := range chunk {
			chunk[i] = 'x'
		}
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(0)
			}
			if _, err := os.Stderr.Write(chunk); err != nil {
				os.Exit(0)
			}
			time.Sleep(floodRateDelay)
		}
	}
}

// runFloodFixturePath is the run-path fixture: the run path spawns
// ShortcutsPath with `run <ref> …` arguments, so the wrapper re-execs the
// fixture entrypoint and ignores the run arguments.
func runFloodFixturePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flood-shortcuts")
	script := "#!/bin/sh\nexport SUBPROCFIX_MODE=flood\nexec \"" + os.Args[0] + "\" -test.run='^TestSubprocFixture$' run flood-ref\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write flood wrapper: %v", err)
	}
	return path
}

func sampleRSS(t *testing.T) uint64 {
	t.Helper()
	out, err := exec.Command("/bin/ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return kb
}

func TestRunCaptureIsBoundedUnderFlood(t *testing.T) {
	if raceEnabled {
		t.Skip("RSS is a proxy measurement; the race detector's allocator inflates baseline and transients, so the bound is asserted in non-race runs")
	}
	r := &ShortcutRunner{
		ShortcutsPath: runFloodFixturePath(t),
		BridgeRefs:    map[Model]string{ModelCloud: "gap-b-probe-ref"},
		Timeout:       DefaultTimeout,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	base := sampleRSS(t)
	samples := make(chan uint64, 64)
	stop := make(chan struct{})
	go func() {
		defer close(samples)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if kb := sampleRSS(t); kb > 0 {
				select {
				case samples <- kb:
				default:
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()

	start := time.Now()
	_, _, _, runErr := r.RunWithFallback(ctx, ModelCloud, "gap B flood probe prompt")
	elapsed := time.Since(start)
	close(stop)

	peak := base
	for kb := range samples {
		if kb > peak {
			peak = kb
		}
	}
	growthMiB := float64(peak-base) / 1024.0

	t.Logf("run path returned after %v (caller deadline 4s), err=%v", elapsed, runErr)
	t.Logf("parent RSS: base %.1f MiB, peak %.1f MiB, growth %.1f MiB under a ~40 MiB/s flood", float64(base)/1024, float64(peak)/1024, growthMiB)

	// BOUNDED CAPTURE: parent memory must not grow with child volume.
	if growthMiB > captureBoundMiB {
		t.Errorf("BOUNDED CAPTURE violated: parent RSS grew %.1f MiB during a %.1fs flood; the run path's capture is unbounded", growthMiB, elapsed.Seconds())
	}

	// The flood still ends as a classified deadline error, never success.
	var re *Error
	if !errors.As(runErr, &re) || re.Kind != KindTimeout {
		t.Errorf("flood outcome classified %T (%v), want a runner *Error with KindTimeout", runErr, runErr)
	}
}
