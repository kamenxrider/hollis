// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
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

func fakeNative(t *testing.T, script string) *NativeRunner {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hollis-native")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return &NativeRunner{Timeout: 2 * time.Second, helperPath: path, platformCheck: func(context.Context) error { return nil }}
}
func nativeLine(event string, fields map[string]any) string {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["protocol"], fields["version"], fields["model"], fields["event"] = NativeProtocol, NativeVersion, ModelLocal, event
	b, _ := json.Marshal(fields)
	return string(b)
}
func nativePrint(line string) string {
	return "printf '%s\\n' '" + strings.ReplaceAll(line, "'", "'\"'\"'") + "'\n"
}
func nativeReady() string {
	return nativePrint(nativeLine("status", map[string]any{"available": true}))
}
func nativeDone(text string, usage bool) string {
	fields := map[string]any{"text": text}
	if usage {
		fields["usage"] = Usage{InputTokens: 7, OutputTokens: 3, ReasoningTokens: 0}
	}
	return nativePrint(nativeLine("complete", fields))
}
func requireNativeKind(t *testing.T, err error, kind Kind) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != kind {
		t.Fatalf("error = %v; want %s", err, kind)
	}
}
func TestNativeCompletePreservesRequestAndMeasuredUsage(t *testing.T) {
	requestFile := filepath.Join(t.TempDir(), "request.json")
	r := fakeNative(t, "cat > '"+requestFile+"'\n"+nativeReady()+nativeDone("\tREADY\r\n雪\n", true))
	prompt := "def answer():\r\n\treturn \"雪\"\n\n"
	result, err := r.RunComplete(t.Context(), ModelLocal, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "\tREADY\r\n雪\n" || result.Model != ModelLocal || result.Usage.InputTokens != 7 {
		t.Fatalf("result %#v", result)
	}
	raw, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	var request nativeRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Prompt != prompt || request.Operation != "complete" || request.Protocol != 1 {
		t.Fatalf("request %#v", request)
	}
}
func TestNativeStreamSnapshotsAreIncremental(t *testing.T) {
	script := "cat >/dev/null\n" + nativeReady()
	for _, text := range []string{"", "雪", "雪", "雪 ☁", "雪 ☁\n"} {
		script += nativePrint(nativeLine("snapshot", map[string]any{"text": text}))
	}
	script += nativeDone("雪 ☁\n", false)
	r := fakeNative(t, script)
	var deltas []string
	result, err := r.Stream(t.Context(), ModelLocal, "hello", func(s string) error { deltas = append(deltas, s); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != result.Text || len(deltas) != 3 || result.Usage != nil {
		t.Fatalf("result %#v deltas %#v", result, deltas)
	}
}
func TestNativeProtocolRejectsIncompleteOrCorruptResponses(t *testing.T) {
	snapshot := nativePrint(nativeLine("snapshot", map[string]any{"text": "hello"}))
	cases := []struct {
		name, script string
		stream       bool
		kind         Kind
	}{
		{"no completion", nativeReady(), false, KindNativeProtocol},
		{"empty", nativeReady() + nativeDone("", true), false, KindNativeProtocol},
		{"no usage", nativeReady() + nativeDone("ok", false), false, KindNativeProtocol},
		{"invalid json", "echo broken\n", false, KindNativeProtocol},
		{"trailing json", nativeReady() + nativeDone("ok", true) + "echo broken\n", false, KindNativeProtocol},
		{"no status", nativeDone("ok", true), false, KindNativeProtocol},
		{"failed exit", nativeReady() + nativeDone("ok", true) + "exit 7\n", false, KindNativeFailed},
		{"revision", nativeReady() + snapshot + nativePrint(nativeLine("snapshot", map[string]any{"text": "help"})), true, KindNativeProtocol},
		{"mismatched final", nativeReady() + snapshot + nativeDone("hello!", false), true, KindNativeProtocol},
		{"snapshot in complete", nativeReady() + snapshot, false, KindNativeProtocol},
		{"stream usage", nativeReady() + snapshot + nativeDone("hello", true), true, KindNativeProtocol},
		{"wrong version", strings.Replace(nativeReady(), NativeVersion, "0.3.3", 1), false, KindNativeProtocol},
		{"unavailable", nativePrint(nativeLine("status", map[string]any{"available": false, "reason": "not ready"})), false, KindLocalUnavailable},
		{"capacity", nativeReady() + nativePrint(nativeLine("error", map[string]any{"kind": "context_capacity"})), false, KindContextCapacity},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := fakeNative(t, "cat >/dev/null\n"+tt.script)
			var result Completion
			var err error
			if tt.stream {
				result, err = r.Stream(t.Context(), ModelLocal, "hello", func(string) error { return nil })
			} else {
				result, err = r.RunComplete(t.Context(), ModelLocal, "hello")
			}
			requireNativeKind(t, err, tt.kind)
			if result.Text != "" || result.Usage != nil {
				t.Fatal("failed response was returned as completion")
			}
		})
	}
}
func TestNativeInputRejectionNeverDispatches(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "called")
	r := fakeNative(t, "touch '"+marker+"'\n")
	for _, tt := range []struct {
		prompt string
		kind   Kind
	}{{" \n", KindEmptyPrompt}, {strings.Repeat("a", NativeMaxPromptBytes+1), KindContextCapacity}, {string([]byte{0xff}), KindUsage}} {
		_, err := r.RunComplete(t.Context(), ModelLocal, tt.prompt)
		requireNativeKind(t, err, tt.kind)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("helper dispatched for rejected input")
	}
}
func TestNativePromptBoundaryIsNotShortened(t *testing.T) {
	requestFile := filepath.Join(t.TempDir(), "request")
	r := fakeNative(t, "cat > '"+requestFile+"'\n"+nativeReady()+nativeDone("ok", true))
	prompt := strings.Repeat("x", NativeMaxPromptBytes)
	if _, err := r.RunComplete(t.Context(), ModelLocal, prompt); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(requestFile)
	var request nativeRequest
	if err := json.Unmarshal(raw, &request); err != nil || request.Prompt != prompt {
		t.Fatal("boundary prompt changed")
	}
}
func TestNativeCancellationKillsProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "orphan-finished")
	r := fakeNative(t, "cat >/dev/null\n(sleep 1; touch '"+marker+"') &\nwait\n")
	r.Timeout = 60 * time.Millisecond
	started := time.Now()
	_, err := r.RunComplete(t.Context(), ModelLocal, "hello")
	requireNativeKind(t, err, KindTimeout)
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("cancellation did not close output promptly")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("descendant survived cancellation")
	}
}
func TestNativeCallbackFailureStopsWithoutSuccessfulCompletion(t *testing.T) {
	r := fakeNative(t, "cat >/dev/null\n"+nativeReady()+nativePrint(nativeLine("snapshot", map[string]any{"text": "hello"}))+"sleep 5\n")
	sentinel := errors.New("client disconnected")
	start := time.Now()
	result, err := r.Stream(t.Context(), ModelLocal, "hello", func(string) error { return sentinel })
	if !errors.Is(err, sentinel) || result.Text != "" || time.Since(start) > time.Second {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
func TestNativeStatusIsNonGenerativeProtocol(t *testing.T) {
	requestFile := filepath.Join(t.TempDir(), "request")
	r := fakeNative(t, "cat > '"+requestFile+"'\n"+nativeReady())
	status, err := r.NativeStatus(t.Context())
	if err != nil || !status.Available || status.Version != NativeVersion {
		t.Fatalf("status %#v err=%v", status, err)
	}
	b, _ := os.ReadFile(requestFile)
	var request nativeRequest
	if err := json.Unmarshal(b, &request); err != nil || request.Operation != "status" || request.Prompt != "" {
		t.Fatal("status sent generation request")
	}
}
func TestLocalIsSelectableWithoutShortcutBridge(t *testing.T) {
	if !ModelLocal.Valid() {
		t.Fatal("local not selectable")
	}
	for _, model := range Models {
		if model == ModelLocal {
			t.Fatal("local included in Shortcut discovery")
		}
	}
	if CompiledUUID(ModelLocal) != "" {
		t.Fatal("local mapped to Shortcut")
	}
	r := NewRouted()
	if r.ShortcutTransport() != r.ShortcutRunner {
		t.Fatal("lost Shortcut configuration access")
	}
	if _, _, err := r.RunWithImages(t.Context(), ModelLocal, "hello", []string{"image.png"}); err == nil {
		t.Fatal("accepted native image")
	}
}

func TestNativeCallerDeadlineOverridesDefaultWithoutTruncation(t *testing.T) {
	r := fakeNative(t, "cat >/dev/null\nsleep 0.1\n"+nativeReady()+nativeDone("ok", true))
	r.Timeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := r.RunComplete(ctx, ModelLocal, "hello"); err != nil {
		t.Fatalf("caller deadline silently reduced: %v", err)
	}
}
func TestNativeCanceledBeforeLaunch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "dispatch")
	r := fakeNative(t, "touch '"+marker+"'\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := r.RunComplete(ctx, ModelLocal, "hello")
	requireNativeKind(t, err, KindContextCanceled)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("dispatched canceled request")
	}
}
func TestNativeRejectsSymlinkHelper(t *testing.T) {
	r := fakeNative(t, "exit 0\n")
	link := filepath.Join(t.TempDir(), "hollis-native")
	if err := os.Symlink(r.helperPath, link); err != nil {
		t.Fatal(err)
	}
	r.helperPath = link
	_, err := r.RunComplete(t.Context(), ModelLocal, "hello")
	requireNativeKind(t, err, KindLocalUnavailable)
}
func TestNativeIncompleteUsageRejected(t *testing.T) {
	r := fakeNative(t, "cat >/dev/null\n"+nativeReady()+nativePrint(nativeLine("complete", map[string]any{"text": "ok", "usage": map[string]int{"input_tokens": 7}})))
	_, err := r.RunComplete(t.Context(), ModelLocal, "hello")
	requireNativeKind(t, err, KindNativeProtocol)
}
