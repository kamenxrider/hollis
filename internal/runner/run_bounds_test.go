// Regression tests for the run path's capture bound.
//
// Defect, measured on unmodified main: runTierWithImages captures stdout and
// stderr in unbounded bytes.Buffers (shortcut.go:445-447), so a flooding
// child grows parent memory without bound until the deadline kill. Measured:
// 514.5 MiB of parent RSS growth in 4 s under a ~40 MiB/s flood across both
// streams (base 6.5 → peak 521.0 MiB), with deadline classification otherwise
// correct.
//
// Property:
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
	"bytes"
	"context"
	"crypto/rand"
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
	case "capture":
		for _, stream := range []struct {
			name string
			file *os.File
		}{
			{"SUBPROCFIX_STDOUT_PATH", os.Stdout},
			{"SUBPROCFIX_STDERR_PATH", os.Stderr},
		} {
			data, err := os.ReadFile(os.Getenv(stream.name))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.file.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		code, err := strconv.Atoi(os.Getenv("SUBPROCFIX_EXIT"))
		if err != nil {
			t.Fatal(err)
		}
		os.Exit(code)
	}
}

func captureRunFixture(t *testing.T, stdout, stderr []byte, exitCode int) *ShortcutRunner {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string][]byte{"stdout": stdout, "stderr": stderr} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SUBPROCFIX_"+strings.ToUpper(name)+"_PATH", path)
	}
	t.Setenv("SUBPROCFIX_EXIT", strconv.Itoa(exitCode))
	path := filepath.Join(dir, "capture-shortcuts")
	script := "#!/bin/sh\nexport SUBPROCFIX_MODE=capture\nexec \"" + os.Args[0] + "\" -test.run='^TestSubprocFixture$'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &ShortcutRunner{
		ShortcutsPath: path,
		BridgeRefs:    map[Model]string{ModelCloud: "capture-fixture"},
		Timeout:       DefaultTimeout,
	}
}

func TestRunCaptureRejectsCapPlusOne(t *testing.T) {
	r := captureRunFixture(t, bytes.Repeat([]byte("x"), runCaptureCap+1), nil, 0)
	response, _, err := r.Run(context.Background(), ModelCloud, "capture fixture")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTransport {
		t.Fatalf("cap+1 outcome = %v, want KindTransport", err)
	}
	if response != "" {
		t.Errorf("truncated capture returned %d response bytes", len(response))
	}
	if re.ExitCode != 0 {
		t.Errorf("child exit code = %d, want its successful exit status 0", re.ExitCode)
	}
}

func TestRunCapturePreservesExactCapRandomBytes(t *testing.T) {
	want := make([]byte, runCaptureCap)
	if _, err := rand.Read(want); err != nil {
		t.Fatal(err)
	}
	r := captureRunFixture(t, want, nil, 0)
	response, _, err := r.Run(context.Background(), ModelCloud, "capture fixture")
	if err != nil || !bytes.Equal([]byte(response), want) {
		t.Fatalf("exact-cap output changed: got %d bytes, want %d bytes, err=%v", len(response), len(want), err)
	}
}

func TestRunCaptureClassifiesStderrTailAfterCap(t *testing.T) {
	for _, exitCode := range []int{0, 1} {
		t.Run(strconv.Itoa(exitCode), func(t *testing.T) {
			stderr := append([]byte("diagnostic beginning\n"), bytes.Repeat([]byte("x"), 3*runCaptureCap)...)
			stderr = append(stderr, []byte("\nrate limit\n")...)
			r := captureRunFixture(t, nil, stderr, exitCode)
			response, _, err := r.Run(context.Background(), ModelCloud, "capture fixture")
			var re *Error
			if !errors.As(err, &re) {
				t.Fatalf("stderr-tail error type = %T, want *Error", err)
			}
			if re.Kind != KindRateLimited {
				t.Fatalf("stderr-tail kind = %v, want KindRateLimited", re.Kind)
			}
			if response != "" || re.ExitCode != exitCode {
				t.Errorf("response bytes=%d exit=%d, want empty response and exit %d", len(response), re.ExitCode, exitCode)
			}
			for _, excerpt := range []string{"diagnostic beginning", "rate limit", "[stderr truncated]"} {
				if !strings.Contains(re.Stderr, excerpt) {
					t.Errorf("stderr excerpt does not contain %q", excerpt)
				}
			}
			if !strings.Contains(re.Error(), "[stderr truncated]") {
				t.Error("error does not surface stderr truncation")
			}
			if len(re.Stderr) > runCaptureCap+128 {
				t.Errorf("stderr excerpt grew to %d bytes beyond capture bound", len(re.Stderr))
			}
		})
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
	// Skipped under -race: the detector's allocator inflates baseline and
	// transients, so the RSS proxy is not meaningful there. CI's test job
	// runs both `go test ./...` (enforces this bound) and
	// `go test -race ./...` (still runs the classification assertion).
	if !raceEnabled && growthMiB > captureBoundMiB {
		t.Errorf("BOUNDED CAPTURE violated: parent RSS grew %.1f MiB during a %.1fs flood; the run path's capture is unbounded", growthMiB, elapsed.Seconds())
	}

	// The flood still ends as a classified deadline error, never success.
	var re *Error
	if !errors.As(runErr, &re) || re.Kind != KindTimeout {
		t.Errorf("flood outcome classified %T (%v), want a runner *Error with KindTimeout", runErr, runErr)
	}
}
