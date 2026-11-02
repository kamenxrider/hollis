// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestProtocolCompatibilityMatrix is the named contract from the protocol-3
// design: each row pins what one CLI era does against one helper era. The rows
// that need a real helper's byte output run in the helper golden script; the
// rows here drive a scripted fake so they stay offline and deterministic.
//
// Every row that expects a refusal also asserts that the helper received no
// request, because a refusal that spawns the request anyway is a silent
// downgrade with extra latency.
func TestProtocolCompatibilityMatrix(t *testing.T) {
	t.Run("row 3: current CLI against an older helper refuses every operation", func(t *testing.T) {
		for _, older := range []int{1, 2} {
			for _, operation := range []string{"status", "complete", "stream", "schema"} {
				t.Run(strconv.Itoa(older)+"/"+operation, func(t *testing.T) {
					marker := filepath.Join(t.TempDir(), "request-received")
					r := fakeNativeAt(t, older, ": > '"+marker+"'\ncat >/dev/null\n")
					err := runNativeOperation(t, r, operation)
					requireNativeKind(t, err, KindNativeProtocol)
					if !strings.Contains(err.Error(), "install the matching bundle") {
						t.Fatalf("error = %v; want the matching-bundle message", err)
					}
					if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
						t.Fatalf("%s reached a protocol-%d helper despite the refusal (stat err %v)", operation, older, statErr)
					}
				})
			}
		}
	})

	t.Run("row 3: a helper newer than this CLI is refused too", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "request-received")
		r := fakeNativeAt(t, NativeProtocol+1, ": > '"+marker+"'\ncat >/dev/null\n")
		_, err := r.NativeStatus(t.Context())
		requireNativeKind(t, err, KindNativeProtocol)
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("helper received a request despite the refusal (stat err %v)", statErr)
		}
	})

	t.Run("row 6: probing happens once per helper path", func(t *testing.T) {
		count := filepath.Join(t.TempDir(), "probe-count")
		script := "if [ \"$#\" -eq 1 ] && [ \"$1\" = \"--protocol-version\" ]; then echo x >> '" + count + "'; printf '%d\\n' " + strconv.Itoa(NativeProtocol) + "; exit 0; fi\ncat >/dev/null\n" + nativeReady() + nativeDone("ok", true)
		r := rawNative(t, script)
		for i := 0; i < 3; i++ {
			if _, err := r.RunComplete(t.Context(), ModelLocal, "prompt"); err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
		}
		probes, err := os.ReadFile(count)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(probes), "x"); got != 1 {
			t.Fatalf("probe ran %d times; want once per helper path", got)
		}
	})

	t.Run("row 7: generation works without capabilities, tool requests are refused by name", func(t *testing.T) {
		// A helper that advertises no capabilities object at all: plain work is
		// unaffected, and a request that needs an advertised capability is a
		// named refusal rather than a generic failure.
		r := fakeNative(t, "cat >/dev/null\n"+nativeReady()+nativeDone("plain", true))
		if text, err := r.RunComplete(t.Context(), ModelLocal, "prompt"); err != nil || text.Text != "plain" {
			t.Fatalf("plain completion %q err=%v; want it served", text.Text, err)
		}
		_, err := r.RunCompleteWithOptions(t.Context(), ModelLocal, "prompt", nativeOptions{ToolCalling: "allowed"})
		requireNativeKind(t, err, KindUnsupportedCapability)
		if !strings.Contains(err.Error(), "tool calling") {
			t.Fatalf("error = %v; want the capability named", err)
		}
	})

	t.Run("row 8: an advertised-but-unsupported capability carries its fixed reason", func(t *testing.T) {
		status := nativeLine("status", map[string]any{
			"available":    true,
			"capabilities": map[string]any{"tools": map[string]any{"supported": false, "reason": "not_implemented"}},
		})
		r := fakeNative(t, "cat >/dev/null\n"+nativePrint(status))
		_, err := r.RunCompleteWithOptions(t.Context(), ModelLocal, "prompt", nativeOptions{ToolCalling: "allowed"})
		requireNativeKind(t, err, KindUnsupportedCapability)
		// An unknown reason string is dropped rather than echoed: only the
		// fixed protocol tokens may reach user-facing text.
		if strings.Contains(err.Error(), "not_implemented") {
			t.Fatalf("error = %v; want the unknown reason dropped", err)
		}
	})

	t.Run("row 8: the advertised reason is used when it is a fixed token", func(t *testing.T) {
		status := nativeLine("status", map[string]any{
			"available":    true,
			"capabilities": map[string]any{"tools": map[string]any{"supported": false, "reason": "macos_26"}},
		})
		r := fakeNative(t, "cat >/dev/null\n"+nativePrint(status))
		_, err := r.RunCompleteWithOptions(t.Context(), ModelLocal, "prompt", nativeOptions{ToolCalling: "allowed"})
		requireNativeKind(t, err, KindUnsupportedCapability)
		if !strings.Contains(err.Error(), "macos_26") {
			t.Fatalf("error = %v; want the advertised reason named", err)
		}
	})

	t.Run("row 8: an advertised tool mode is accepted and sent as a request member", func(t *testing.T) {
		requestFile := filepath.Join(t.TempDir(), "request")
		status := nativeLine("status", map[string]any{
			"available":    true,
			"capabilities": map[string]any{"tools": map[string]any{"supported": true, "tool_calling_mode": true}},
		})
		r := fakeNative(t, "cat > '"+requestFile+"'\n"+nativePrint(status)+nativeDone("ok", true))
		if _, err := r.RunCompleteWithOptions(t.Context(), ModelLocal, "prompt", nativeOptions{ToolCalling: "disallowed"}); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(requestFile)
		if err != nil {
			t.Fatal(err)
		}
		var request nativeRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.ToolCalling != "disallowed" {
			t.Fatalf("request tool_calling = %q; want disallowed", request.ToolCalling)
		}
	})

	t.Run("row 12: capabilities outside status and data after completion are protocol errors", func(t *testing.T) {
		capabilitiesOnComplete := nativePrint(nativeLine("complete", map[string]any{
			"text": "ok", "usage": nativeUsageEvent(), "capabilities": map[string]any{},
		}))
		r := fakeNative(t, "cat >/dev/null\n"+nativeReady()+capabilitiesOnComplete)
		_, err := r.RunComplete(t.Context(), ModelLocal, "prompt")
		requireNativeKind(t, err, KindNativeProtocol)
		if !strings.Contains(err.Error(), "unexpected native capabilities") {
			t.Fatalf("error = %v; want the placement rule named", err)
		}
	})

	t.Run("row 12: unknown members inside capabilities are additive, outside are refused", func(t *testing.T) {
		status := nativeLine("status", map[string]any{
			"available": true,
			"capabilities": map[string]any{
				"future_block": map[string]any{"supported": true},
				"vision":       true,
			},
		})
		r := fakeNative(t, "cat >/dev/null\n"+nativePrint(status))
		reported, err := r.NativeStatus(t.Context())
		if err != nil {
			t.Fatalf("status with an unknown capability block: %v", err)
		}
		if reported.Capabilities == nil || reported.Capabilities.Vision == nil || !*reported.Capabilities.Vision {
			t.Fatalf("status %#v; want known members read", reported)
		}

		unknownTopLevel := nativePrint(nativeLine("status", map[string]any{"available": true, "future_member": true}))
		strict := fakeNative(t, "cat >/dev/null\n"+unknownTopLevel)
		if _, err := strict.NativeStatus(t.Context()); err == nil {
			t.Fatal("an unknown top-level event member was accepted")
		}
	})
}

// runNativeOperation drives one operation through the runner so a matrix row can
// assert the same refusal for every entry point.
func runNativeOperation(t *testing.T, r *NativeRunner, operation string) error {
	t.Helper()
	switch operation {
	case "status":
		_, err := r.NativeStatus(t.Context())
		return err
	case "complete":
		_, err := r.RunComplete(t.Context(), ModelLocal, "prompt")
		return err
	case "stream":
		_, err := r.Stream(t.Context(), ModelLocal, "prompt", func(string) error { return nil })
		return err
	case "schema":
		_, err := r.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
		return err
	}
	t.Fatalf("unknown operation %q", operation)
	return nil
}

// TestNativeOptionsValidate pins the closed set of tool modes: the helper has
// two, and anything else is a caller error before a process starts.
func TestNativeOptionsValidate(t *testing.T) {
	for _, mode := range []string{"", "allowed", "disallowed"} {
		if err := (nativeOptions{ToolCalling: mode}).validate(); err != nil {
			t.Fatalf("mode %q rejected: %v", mode, err)
		}
	}
	for _, mode := range []string{"required", "auto", "ALLOWED"} {
		err := (nativeOptions{ToolCalling: mode}).validate()
		requireNativeKind(t, err, KindUsage)
	}
}

// TestNegotiateRespectsAnExpiredCallerDeadline keeps a probe from claiming a
// timeout that belongs to the request: the caller's deadline expired before the
// probe could run, which is the model-facing deadline message, not the probe's.
func TestNegotiateRespectsAnExpiredCallerDeadline(t *testing.T) {
	r := fakeNative(t, "cat >/dev/null\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.negotiate(ctx, r.helperPath)
	requireNativeKind(t, err, KindContextCanceled)
}
