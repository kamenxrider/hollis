// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runnerWithFake installs a fake `shortcuts` shell script that records argv,
// drains stdin to disk, and behaves according to mode:
//
//	echo      cat the recorded stdin back (round-trip)
//	empty     exit 0 with no stdout (the ambiguous signature)
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
		"  missing) echo 'The shortcut named \"AFM Bridge\" could not be found' >&2; exit 1 ;;\n" +
		"  usage) echo 'Error: invalid value ... for --output-type' >&2; exit 64 ;;\n" +
		"  sigabrt) kill -ABRT $$ ;;\n" +
		"  hang) sleep 300 ;;\n" +
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
	got, err := r.Run(context.Background(), ModelCloud, prompt)
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
	got, err := r.Run(context.Background(), ModelCloud, prompt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != prompt {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", got, prompt)
	}
}

func TestRunInvokesUUIDWithPlainTextFlag(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	if _, err := r.Run(context.Background(), ModelCloudPro, "hi"); err != nil {
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
		_, err := r.Run(context.Background(), ModelCloud, p)
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
	r, _ := runnerWithFake(t, "empty")
	_, err := r.Run(context.Background(), ModelCloud, "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindNoOutput {
		t.Fatalf("want KindNoOutput, got %v", err)
	}
}

func TestMissingShortcutMapsExit1(t *testing.T) {
	r, _ := runnerWithFake(t, "missing")
	_, err := r.Run(context.Background(), ModelCloud, "hello")
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
	_, err := r.Run(context.Background(), ModelCloud, "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindUsage {
		t.Fatalf("want KindUsage, got %v", err)
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

func TestAutoUsesPrimaryWithoutFallback(t *testing.T) {
	r, dir := runnerWithFake(t, "echo")
	got, err := r.Run(context.Background(), ModelAuto, "hello")
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
	got, err := r.Run(context.Background(), ModelAuto, "hello")
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

func TestAutoDoesNotRetryEmptyPrompt(t *testing.T) {
	r, dir := runnerWithFake(t, "fail-once")
	_, err := r.Run(context.Background(), ModelAuto, "")
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
		got, err := r.Run(context.Background(), m, "ping")
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
	_, err := r.Run(context.Background(), Model("nope"), "hello")
	var re *Error
	if !errors.As(err, &re) || re.Kind != KindUsage {
		t.Fatalf("want usage error for unknown model, got %v", err)
	}
}

func TestTimeoutMessageUsesEffectiveDeadline(t *testing.T) {
	r, _ := runnerWithFake(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := r.Run(ctx, ModelCloud, "hello")
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
	_, err := r.Run(ctx, ModelCloud, "hello")
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

func TestTimeoutKillsChild(t *testing.T) {
	r, _ := runnerWithFake(t, "hang")
	r.Timeout = 150 * time.Millisecond
	start := time.Now()
	_, err := r.Run(context.Background(), ModelCloud, "hello")
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
	// Rule 3: no orphaned child should survive the group kill.
	out, _ := exec.Command("pgrep", "-f", "sleep 300").CombinedOutput()
	if len(out) > 0 {
		t.Fatalf("orphaned child processes remain: %s", out)
	}
}

func TestConcurrencyFourParallel(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	var errs [4]error
	var resps [4]string
	done := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			text, err := r.Run(context.Background(), ModelCloud, "parallel prompt")
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
	if _, err := r.Run(context.Background(), ModelCloud, "hello"); err != nil {
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
	got, err := r.Run(context.Background(), ModelCloud, "no newline at end")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("unexpected trailing newline: %q", got)
	}
}
