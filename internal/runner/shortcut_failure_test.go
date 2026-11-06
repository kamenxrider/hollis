// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func failureFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "shortcut-"+name+".ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestShortcutFailureLogShapes(t *testing.T) {
	for name, code := range map[string]int{"rate": 1508, "refused": 900, "access": 1511} {
		info := parseShortcutFailureLog(failureFixture(t, name))
		if info.Domain != "WFAskLLMActionErrorDomain" || info.Code != code || len(info.Keys) == 0 {
			t.Fatalf("%s: %+v", name, info)
		}
		data, _ := json.Marshal(info)
		if strings.Contains(string(data), "private") || strings.Contains(string(data), "process") {
			t.Fatal("retained raw log content")
		}
	}
	data := failureFixture(t, "refused")
	overlap := append(append([]byte(nil), data...), []byte(`{"processID":2,"processImagePath":"/System/BackgroundShortcutRunner","eventMessage":"Use Model action started"}`)...)
	for _, input := range [][]byte{overlap, append(append([]byte(nil), data...), failureFixture(t, "rate")...), []byte("invalid"), []byte(`{"processID":3,"processImagePath":"/System/another-app","eventMessage":"WFAskLLMActionErrorDomain Code=1508"}`)} {
		if info := parseShortcutFailureLog(input); info.Code != 0 || info.Domain != "" {
			t.Fatalf("guessed: %+v", info)
		}
	}
}

func TestShortcutLogWindowDistinguishesNeighborsFromOverlap(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)
	end := start.Add(time.Second)
	current := `{"processID":2,"processImagePath":"/System/BackgroundShortcutRunner","timestamp":"2026-01-01 00:00:02.500000+0000","eventMessage":"WFAskLLMActionErrorDomain Code=900"}`
	prior := `{"processID":1,"processImagePath":"/System/BackgroundShortcutRunner","timestamp":"2026-01-01 00:00:01.900000+0000","eventMessage":"WFAskLLMActionErrorDomain Code=1508"}`
	info := parseShortcutFailureLogWindow([]byte(prior+"\n"+current), start, end)
	if info.Code != 900 {
		t.Fatalf("serialized neighbor contaminated classification: %+v", info)
	}
	overlap := strings.Replace(prior, "01.900000", "02.400000", 1)
	if info := parseShortcutFailureLogWindow([]byte(overlap+"\n"+current), start, end); info.Code != 0 {
		t.Fatalf("overlap guessed: %+v", info)
	}
	if info := parseShortcutFailureLogWindow(failureFixture(t, "refused"), start, end); info.Code != 0 {
		t.Fatal("missing timestamps guessed")
	}
}

func TestShortcutFailureClassificationAndNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name, stream, diagnostic string
		code                     int
		want                     Kind
	}{
		{"exit-zero-usage", "stdout", "Error: You have reached the usage limit for this model. Please try again later.", 0, KindRateLimited},
		{"busy-wording", "stderr", "Error: Your request can’t be completed right now.", 0, KindRateLimited},
		{"ascii-wording", "stderr", "Error: Your request can't be completed right now.", 0, KindRateLimited},
		{"log-rate", "stderr", "Error: The Shortcut execution failed for an unrecognized reason.", 1508, KindRateLimited},
		{"refused-not-limit", "stderr", "Error: The model cannot provide a response for this request. Please revise the request and try again.", 900, KindContentRefused},
		{"code-wins", "stderr", "Error: rate limit", 900, KindContentRefused},
		{"access", "stderr", "Error: The Shortcut execution failed for an unrecognized reason.", 1511, KindAccessRequired},
		{"unknown", "stderr", "Error: The Shortcut execution failed for an unrecognized reason.", 0, KindShortcutFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, dir := runnerWithFake(t, "empty")
			quoted := strings.ReplaceAll(tc.diagnostic, "'", "'\\''")
			script := "#!/bin/sh\nprintf 'call\\n' >> '" + dir + "/calls'\nprintf '%s' '" + quoted + "'"
			if tc.stream == "stderr" {
				script += " >&2\nexit 1\n"
			}
			if err := os.WriteFile(r.ShortcutsPath, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			logCalls := 0
			r.failureLog = func(context.Context, time.Time, time.Time) ShortcutFailureInfo {
				logCalls++
				if tc.code == 0 {
					return ShortcutFailureInfo{}
				}
				return ShortcutFailureInfo{Domain: "WFAskLLMActionErrorDomain", Code: tc.code}
			}
			text, _, fallback, err := r.RunWithFallback(t.Context(), ModelAuto, "request-canary")
			var failure *Error
			if text != "" || fallback.Used || !errors.As(err, &failure) || failure.Kind != tc.want || logCalls != 1 {
				t.Fatalf("text=%q fallback=%+v err=%v logs=%d", text, fallback, err, logCalls)
			}
			if !strings.Contains(failure.PublicShortcutMessage(), tc.diagnostic) || len(failure.ShortcutDetails()) == 0 {
				t.Fatal("lost Apple text")
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			if strings.Count(string(calls), "call") != 1 {
				t.Fatal("retried")
			}
		})
	}
}

func TestShortcutLogTimeoutFallsBackAndSuccessNeverChecksLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if info := readShortcutFailureLog(t.Context(), start, start, path, 20*time.Millisecond); info.Code != 0 {
		t.Fatal(info)
	}
	if time.Since(start) > time.Second {
		t.Fatal("log timeout unbounded")
	}
	r, _ := runnerWithFake(t, "rate")
	r.failureLog = func(ctx context.Context, start, end time.Time) ShortcutFailureInfo {
		return readShortcutFailureLog(ctx, start, end, path, 20*time.Millisecond)
	}
	_, _, err := r.Run(t.Context(), ModelCloud, "fixture")
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != KindRateLimited {
		t.Fatal(err)
	}
	r, _ = runnerWithFake(t, "echo")
	r.failureLog = func(context.Context, time.Time, time.Time) ShortcutFailureInfo {
		t.Fatal("log queried on success")
		return ShortcutFailureInfo{}
	}
	if _, _, err := r.Run(t.Context(), ModelCloud, "fixture"); err != nil {
		t.Fatal(err)
	}
}

func TestAppleDiagnosticSanitization(t *testing.T) {
	prompt := "request-canary\nsecond-request-line"
	raw := "Error: " + prompt + " /Users/example/private.txt token=credential-canary \x1b[31m" + strings.Repeat("z", 4000)
	e := &Error{Kind: KindShortcutFailed}
	r := New()
	r.ShortcutsPath = "/synthetic/shortcuts"
	r.classifyFailure(t.Context(), e, time.Now(), time.Now(), prompt, raw, raw)
	all, _ := json.Marshal([]any{e.Error(), e.PublicShortcutMessage(), e.ShortcutDetails()})
	for _, private := range []string{"request-canary", "second-request-line", "/Users/", "credential-canary", `\u001b`} {
		if strings.Contains(string(all), private) {
			t.Fatalf("leaked %s", private)
		}
	}
	if len(e.Stdout) > appleTextLimit+20 || len(e.Stderr) > appleTextLimit+20 {
		t.Fatal("unbounded diagnostic")
	}
}
