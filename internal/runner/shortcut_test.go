// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runnerWithFake installs a fake `shortcuts` shell script that records argv,
// drains stdin to disk, and behaves according to mode:
//
//	echo      cat the recorded stdin back (round-trip)
//	empty     exit 0 with no stdout (the ambiguous signature)
//	whitespace exit 0 with only non-content whitespace
//	missing   exit 1 with Apple-like stderr
//	usage     exit 64
//	sigabrt   raise SIGABRT (exit 134 via signal)
//	hang      sleep forever (exercises the deadline kill)
//	fail-once exit 1 on the first invocation, echo afterwards (fallback tests)
//
// Every invocation appends to <dir>/count.txt so tests can assert how
// many spawns happened (e.g. that auto did or did not fall back).
func runnerWithFake(t *testing.T, mode string) (*ShortcutRunner, string) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/shortcuts"
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" > " + dir + "/argv.txt\n" +
		"cat > " + dir + "/stdin.txt\n" +
		"count=$(cat " + dir + "/count.txt 2>/dev/null || echo 0)\n" +
		"count=$((count + 1))\n" +
		"echo \"$count\" > " + dir + "/count.txt\n" +
		"mode=${HOLLIS_FAKE_MODE:-" + mode + "}\n" +
		"case \"$mode\" in\n" +
		"  echo) cat " + dir + "/stdin.txt ;;\n" +
		"  empty) exit 0 ;;\n" +
		"  whitespace) printf ' \\n\\t' ;;\n" +
		"  missing) echo 'The shortcut named \"AFM Bridge\" could not be found' >&2; exit 1 ;;\n" +
		"  usage) echo 'Error: invalid value ... for --output-type' >&2; exit 64 ;;\n" +
		"  rate) echo 'Too many incoming requests' >&2; exit 1 ;;\n" +
		"  generic) echo 'temporary transport problem' >&2; exit 1 ;;\n" +
		"  sigabrt) kill -ABRT $$ ;;\n" +
		"  sigterm) kill -TERM $$ ;;\n" +
		// The sleep runs in the background so its PID can be recorded, and
		// `wait` keeps the script alive as the direct child. Tests assert
		// against that exact PID rather than pgrep-ing for a command line.
		"  hang) sleep 300 & echo $! > " + dir + "/child.pid; wait ;;\n" +
		"  fail-once) if [ \"$count\" -le 1 ]; then echo 'cloud unavailable' >&2; exit 1; fi; cat " + dir + "/stdin.txt ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := New()
	r.ShortcutsPath = path
	return r, dir
}

func TestRoundTripMultiLineNoTrailingNewline(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	prompt := "line1\nline2\nline3"
	got, _, err := r.Run(context.Background(), ModelCloud, prompt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != prompt {
		t.Fatalf("response not round-tripped: %q", got)
	}
}

func TestRoundTripUnicodeAndTabs(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	prompt := "line1\nline2\n\ttabbed ✓ emoji 🚀 \"quoted\""
	got, _, err := r.Run(context.Background(), ModelCloud, prompt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != prompt {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", got, prompt)
	}
}

func TestRunInvokesUUIDWithPlainTextFlag(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	if _, _, err := r.Run(context.Background(), ModelCloudPro, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	argv, err := os.ReadFile(dir + "/argv.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := "run " + BridgeUUIDCloudPro + " --output-type public.plain-text\n"
	if string(argv) != want {
		t.Fatalf("argv = %q, want %q", string(argv), want)
	}
}

func TestEmptyPromptRefusedWithoutSpawn(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	for _, p := range []string{"", "   \n\t"} {
		_, _, err := r.Run(context.Background(), ModelCloud, p)
		var re *Error
		if !errors.As(err, &re) || re.Kind != KindEmptyPrompt {
			t.Fatalf("prompt %q: want empty_prompt, got %v", p, err)
		}
	}
	if _, err := os.Stat(dir + "/argv.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child spawned for empty prompt (argv file exists): %v", err)
	}
}

func TestExitZeroEmptyStdoutIsNoOutput(t *testing.T) {
	for _, mode := range []string{"empty", "whitespace"} {
		r, _ := runnerWithFake(t, mode)
		_, _, err := r.Run(context.Background(), ModelCloud, "hello")
		var re *Error
		if !errors.As(err, &re) || re.Kind != KindNoOutput {
			t.Fatalf("mode %s: want KindNoOutput, got %v", mode, err)
		}
	}
}

func TestMissingShortcutMapsExit1(t *testing.T) {
	r, _ := runnerWithFake(t, "missing")
	_, _, err := r.Run(context.Background(), ModelCloud, "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindShortcutMissing {
		t.Fatalf("want KindShortcutMissing, got %v", err)
	}
	if re.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", re.ExitCode)
	}
}

func TestUsageErrorMapsExit64(t *testing.T) {
	r, _ := runnerWithFake(t, "usage")
	_, _, err := r.Run(context.Background(), ModelCloud, "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindUsage {
		t.Fatalf("want KindUsage, got %v", err)
	}
}

func TestRateLimitAndGenericExitOneAreNotMissing(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want Kind
	}{{"rate", KindRateLimited}, {"generic", KindTransport}} {
		r, _ := runnerWithFake(t, tc.mode)
		_, _, err := r.Run(context.Background(), ModelCloud, "hello")
		var runErr *Error
		if !errors.As(err, &runErr) || runErr.Kind != tc.want {
			t.Fatalf("mode %s: got %v, want %s", tc.mode, err, tc.want)
		}
	}
}

func TestRealSignalsAreClassifiedBeforeText(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want Kind
		sig  syscall.Signal
	}{{"sigabrt", KindSIGABRT, syscall.SIGABRT}, {"sigterm", KindSignal, syscall.SIGTERM}} {
		r, _ := runnerWithFake(t, tc.mode)
		_, _, err := r.Run(context.Background(), ModelCloud, "hello")
		var runErr *Error
		if !errors.As(err, &runErr) || runErr.Kind != tc.want || runErr.Signal != tc.sig {
			t.Fatalf("mode %s: got %+v, want kind=%s signal=%s", tc.mode, runErr, tc.want, tc.sig)
		}
	}
}

func TestCanceledRunDoesNotFallback(t *testing.T) {
	r, dir := runnerWithFake(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	_, _, err := r.Run(ctx, ModelAuto, "hello")
	var runErr *Error
	if !errors.As(err, &runErr) || runErr.Kind != KindContextCanceled {
		t.Fatalf("got %v, want context_canceled", err)
	}
	count, _ := os.ReadFile(dir + "/count.txt")
	if strings.TrimSpace(string(count)) != "1" {
		t.Fatalf("spawn count=%q, want one", count)
	}
}

func TestAutoFallbackPolicy(t *testing.T) {
	for _, mode := range []string{"rate", "generic", "empty"} {
		r, dir := runnerWithFake(t, mode)
		// Make only the first invocation fail; the fake's fail-once mode is
		// already covered separately. These cases assert policy classification.
		if mode != "empty" {
			// After the first spawn the environment-driven mode is still the same,
			// so both calls fail; two spawns are the assertion.
		}
		_, _, _ = r.Run(context.Background(), ModelAuto, "hello")
		count, _ := os.ReadFile(dir + "/count.txt")
		if strings.TrimSpace(string(count)) != "2" {
			t.Fatalf("mode %s spawn count=%q, want two", mode, count)
		}
	}
	for _, mode := range []string{"usage", "sigabrt", "sigterm"} {
		r, dir := runnerWithFake(t, mode)
		_, _, _ = r.Run(context.Background(), ModelAuto, "hello")
		count, _ := os.ReadFile(dir + "/count.txt")
		if strings.TrimSpace(string(count)) != "1" {
			t.Fatalf("mode %s spawn count=%q, want one", mode, count)
		}
	}
}

func TestBridgeReferenceBeginningWithDashRejectedBeforeSpawn(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	r.BridgeRefs[ModelCloud] = "--help"
	_, _, err := r.Run(context.Background(), ModelCloud, "hello")
	var runErr *Error
	if !errors.As(err, &runErr) || runErr.Kind != KindUsage {
		t.Fatalf("got %v, want usage", err)
	}
	if _, statErr := os.Stat(dir + "/count.txt"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("transport spawned: %v", statErr)
	}
}

func TestDefaultBridgeRefsCoverAllModels(t *testing.T) {
	r := New()
	if len(r.BridgeRefs) != len(Models) {
		t.Fatalf("BridgeRefs has %d entries, want %d", len(r.BridgeRefs), len(Models))
	}
	for _, m := range Models {
		if r.BridgeRefs[m] == "" {
			t.Fatalf("model %q missing bridge ref", m)
		}
	}
}

func TestMissingResolvedRefDoesNotInvokeCompiledCandidate(t *testing.T) {
	r := New()
	r.BridgeRefs = map[Model]string{}
	_, err := r.runTier(context.Background(), ModelCloud, "quiet test")
	var runErr *Error
	if !errors.As(err, &runErr) || runErr.Kind != KindShortcutMissing {
		t.Fatalf("err=%v kind=%v, want shortcut_missing", err, runErr)
	}
}

func TestAutoUsesPrimaryWithoutFallback(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	got, _, err := r.Run(context.Background(), ModelAuto, "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "hello" {
		t.Fatalf("auto round-trip mismatch: %q", got)
	}
	count, _ := os.ReadFile(dir + "/count.txt")
	if strings.TrimSpace(string(count)) != "1" {
		t.Fatalf("spawn count = %s, want 1 (no fallback on success)", count)
	}
}

func TestAutoFallsBackToOnDevice(t *testing.T) {
	r, dir := runnerWithFake(t, "fail-once")
	got, _, err := r.Run(context.Background(), ModelAuto, "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "hello" {
		t.Fatalf("auto fallback round-trip mismatch: %q", got)
	}
	count, _ := os.ReadFile(dir + "/count.txt")
	if strings.TrimSpace(string(count)) != "2" {
		t.Fatalf("spawn count = %s, want 2 (cloud then on-device)", count)
	}
	// The second (fallback) invocation must reference the on-device UUID.
	argv, _ := os.ReadFile(dir + "/argv.txt")
	if !strings.Contains(string(argv), BridgeUUIDOnDevice) {
		t.Fatalf("fallback argv missing on-device UUID: %q", string(argv))
	}
}

func TestAutoSkipsUndiscoveredCloudWithoutSpawningIt(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	r.BridgeRefs = map[Model]string{ModelOnDevice: "verified-on-device"}
	got, used, fallback, err := r.RunWithFallback(context.Background(), ModelAuto, "hello")
	if err != nil || got != "hello" || used != ModelOnDevice {
		t.Fatalf("got=%q used=%s fallback=%+v err=%v", got, used, fallback, err)
	}
	if !fallback.Used || fallback.Reason != KindShortcutMissing {
		t.Fatalf("fallback=%+v, want unavailable-cloud fallback", fallback)
	}
	count, _ := os.ReadFile(dir + "/count.txt")
	if strings.TrimSpace(string(count)) != "1" {
		t.Fatalf("spawn count=%q, want only on-device", count)
	}
	argv, _ := os.ReadFile(dir + "/argv.txt")
	if !strings.Contains(string(argv), "verified-on-device") || strings.Contains(string(argv), BridgeUUIDCloud) {
		t.Fatalf("unexpected transport argv: %q", argv)
	}
}

func TestAutoReportsTheTierThatServed(t *testing.T) {
	// auto falls back silently; the caller must still be able to tell a
	// cloud answer from an on-device one, because they are not
	// interchangeable (on-device has refused tasks cloud completed).
	r, _ := runnerWithFake(t, "echo")
	if _, used, err := r.Run(context.Background(), ModelAuto, "hi"); err != nil || used != ModelCloud {
		t.Fatalf("auto success: used = %q, err = %v; want cloud", used, err)
	}

	r, _ = runnerWithFake(t, "fail-once")
	if _, used, err := r.Run(context.Background(), ModelAuto, "hi"); err != nil || used != ModelOnDevice {
		t.Fatalf("auto fallback: used = %q, err = %v; want on-device", used, err)
	}

	// Explicit tiers always report themselves.
	r, _ = runnerWithFake(t, "echo")
	for _, m := range Models {
		if _, used, err := r.Run(context.Background(), m, "hi"); err != nil || used != m {
			t.Fatalf("explicit %s: used = %q, err = %v", m, used, err)
		}
	}
}

func TestAutoDoesNotRetryEmptyPrompt(t *testing.T) {
	r, dir := runnerWithFake(t, "fail-once")
	_, _, err := r.Run(context.Background(), ModelAuto, "")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindEmptyPrompt {
		t.Fatalf("want empty_prompt, got %v", err)
	}
	if _, err := os.Stat(dir + "/count.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child spawned for empty prompt (count file exists): %v", err)
	}
}

func TestRoundTripOnDeviceAndChatGPT(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	for _, m := range []Model{ModelOnDevice, ModelChatGPT} {
		got, _, err := r.Run(context.Background(), m, "ping")
		if err != nil {
			t.Fatalf("model %s: Run: %v", m, err)
		}
		if got != "ping" {
			t.Fatalf("model %s: echo round-trip mismatch: %q", m, got)
		}
	}
}

func TestUnknownModelRejected(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	_, _, err := r.Run(context.Background(), Model("nope"), "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindUsage {
		t.Fatalf("want usage error for unknown model, got %v", err)
	}
}

func TestTimeoutMessageUsesEffectiveDeadline(t *testing.T) {
	r, _ := runnerWithFake(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, _, err := r.Run(ctx, ModelCloud, "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout {
		t.Fatalf("want KindTimeout, got %v", err)
	}
	if strings.Contains(err.Error(), "exceeded 30s") {
		t.Fatalf("timeout message must use the effective deadline, not the default: %v", err)
	}
}

func TestCallerDeadlineClampedToCeiling(t *testing.T) {
	old := MaxTimeout
	MaxTimeout = 60 * time.Millisecond
	t.Cleanup(func() { MaxTimeout = old })
	r, _ := runnerWithFake(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	start := time.Now()
	_, _, err := r.Run(ctx, ModelCloud, "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout {
		t.Fatalf("want KindTimeout, got %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("ceiling not enforced, ran %s", d)
	}
	if !strings.Contains(err.Error(), "exceeded 60ms") {
		t.Fatalf("timeout message should name the clamped ceiling: %v", err)
	}
}

func TestCallerDeadlineCanExceedTheDefault(t *testing.T) {
	// The regression that made `--timeout` a no-op above 30s: runTier took
	// the caller's deadline only when it was SHORTER than r.Timeout, so a
	// longer one was silently discarded and every run died at the default.
	r, _ := runnerWithFake(t, "hang")
	r.Timeout = 120 * time.Millisecond // stands in for the 30s default
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := r.Run(ctx, ModelCloud, "hello")
	elapsed := time.Since(start)

	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout {
		t.Fatalf("want KindTimeout, got %v", err)
	}
	if elapsed < 500*time.Millisecond {
		t.Fatalf("caller deadline ignored: died after %s, want ~900ms", elapsed)
	}
	if strings.Contains(err.Error(), "120ms") {
		t.Fatalf("timeout message quotes the default, not the caller's deadline: %v", err)
	}
}

func TestExpiredDeadlineIsTimeoutWithoutSpawning(t *testing.T) {
	// A deadline that has already passed must not reach exec: Start would
	// fail with ctx.Err() and be misfiled as a transport error, and the
	// message would quote a negative duration.
	r, dir := runnerWithFake(t, "echo")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, _, err := r.Run(ctx, ModelCloud, "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout {
		t.Fatalf("want KindTimeout, got %v (%T)", err, err)
	}
	if strings.Contains(err.Error(), "-") {
		t.Fatalf("timeout message quotes a negative duration: %v", err)
	}
	if _, statErr := os.Stat(dir + "/count.txt"); statErr == nil {
		t.Fatal("the transport was spawned despite an already-expired deadline")
	}
}

func TestTimeoutKillsChild(t *testing.T) {
	r, dir := runnerWithFake(t, "hang")
	// Long enough that the fake reliably reaches its `sleep` and records the
	// PID before the deadline fires. At 150ms a loaded or sandboxed machine
	// gets killed mid-spawn, and the test then fails on a missing pidfile
	// rather than on the orphan it is actually looking for.
	r.Timeout = 500 * time.Millisecond
	start := time.Now()
	_, _, err := r.Run(context.Background(), ModelCloud, "hello")
	if err == nil {
		t.Fatal("want timeout error")
	}
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindTimeout {
		t.Fatalf("want KindTimeout, got %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("kill took too long: %s", d)
	}

	// Rule 3: the grandchild must not survive the process-group kill.
	//
	// This asserts against the sleep's own recorded PID. The previous
	// `pgrep -f "sleep 300"` was wrong twice over: it failed on any
	// unrelated `sleep 300` anywhere on the machine, and it treated
	// pgrep's own error output as proof of an orphan, so a restricted
	// environment where pgrep cannot enumerate processes reported a
	// failure that had not happened.
	raw, err := os.ReadFile(dir + "/child.pid")
	if err != nil {
		t.Fatalf("fake never recorded its child PID: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("bad recorded PID %q: %v", raw, err)
	}
	// Reaping is asynchronous; give the kill a moment to land.
	deadline := time.Now().Add(2 * time.Second)
	for {
		// Signal 0 probes for existence without delivering anything.
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return // gone, as required
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphaned child %d survived the group kill", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConcurrencyFourParallel(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	var errs [4]error
	var resps [4]string
	done := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			text, _, err := r.Run(context.Background(), ModelCloud, "parallel prompt")
			resps[i], errs[i] = text, err
			done <- i
		}(i)
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	for i := 0; i < 4; i++ {
		if errs[i] != nil {
			t.Fatalf("parallel run %d: %v", i, errs[i])
		}
		if resps[i] != "parallel prompt" {
			t.Fatalf("parallel run %d wrong response: %q", i, resps[i])
		}
	}
}

func TestUUIDAndPlainTextFlagInArgv(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	if _, _, err := r.Run(context.Background(), ModelCloud, "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	argv, _ := os.ReadFile(dir + "/argv.txt")
	if !strings.Contains(string(argv), BridgeUUIDCloud) {
		t.Fatalf("expected UUID reference in argv, got %q", string(argv))
	}
	if !strings.Contains(string(argv), "--output-type public.plain-text") {
		t.Fatalf("expected --output-type public.plain-text in argv: %q", string(argv))
	}
}

func TestNoTrailingNewlinePreserved(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	got, _, err := r.Run(context.Background(), ModelCloud, "no newline at end")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("unexpected trailing newline: %q", got)
	}
}
