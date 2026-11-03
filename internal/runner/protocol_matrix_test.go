// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestProtocolCompatibilityMatrix is the named contract from the protocol-3
// design: each row pins what one CLI era does against one helper era. The rows
// that need a real helper's byte output run in the helper golden script; the
// rows here drive a scripted fake so they stay offline and deterministic.
//
// Probe mismatch rows assert that no request was sent. Capability refusal rows
// exercise the error or status event before any generation.
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

	t.Run("row 6: probing happens once per unchanged helper", func(t *testing.T) {
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
			t.Fatalf("probe ran %d times; want once per unchanged helper", got)
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
		if !strings.Contains(err.Error(), "tools") {
			t.Fatalf("error = %v; want the capability named", err)
		}
	})

	t.Run("row 8: an advertised-but-unsupported capability carries its fixed reason", func(t *testing.T) {
		status := nativeLine("status", map[string]any{
			"available":    true,
			"capabilities": map[string]any{"tools": map[string]any{"supported": false, "reason": "untrusted_diagnostic"}},
		})
		r := fakeNative(t, "cat >/dev/null\n"+nativePrint(status))
		_, err := r.RunCompleteWithOptions(t.Context(), ModelLocal, "prompt", nativeOptions{ToolCalling: "allowed"})
		requireNativeKind(t, err, KindUnsupportedCapability)
		// An unknown reason string is dropped rather than echoed: only the
		// fixed protocol tokens may reach user-facing text.
		if strings.Contains(err.Error(), "untrusted_diagnostic") {
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

func TestNativeProbeInvalidatedByFileChange(t *testing.T) {
	for _, change := range []string{"inode", "size", "mtime"} {
		t.Run(change, func(t *testing.T) {
			count := filepath.Join(t.TempDir(), "probes")
			marker := filepath.Join(t.TempDir(), "request")
			script := "if [ \"$1\" = --protocol-version ]; then echo x >> '" + count +
				"'; echo 3; exit 0; fi\n: > '" + marker + "'\n"
			r := rawNative(t, script)
			if err := r.negotiate(t.Context(), r.helperPath); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(r.helperPath)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(r.helperPath)
			if err != nil {
				t.Fatal(err)
			}
			body = []byte(strings.Replace(string(body), "echo 3;", "echo 2;", 1))
			target := r.helperPath
			if change == "inode" {
				target += ".replacement"
			}
			if change == "size" {
				body = append(body, []byte("# changed size\n")...)
			}
			if err := os.WriteFile(target, body, 0o700); err != nil {
				t.Fatal(err)
			}
			mtime := info.ModTime()
			if change == "mtime" {
				mtime = mtime.Add(time.Second)
			}
			if err := os.Chtimes(target, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			if change == "inode" {
				if err := os.Rename(target, r.helperPath); err != nil {
					t.Fatal(err)
				}
			}
			_, err = r.RunComplete(t.Context(), ModelLocal, "fixture")
			requireNativeKind(t, err, KindNativeProtocol)
			probes, err := os.ReadFile(count)
			if err != nil || strings.Count(string(probes), "x") != 2 {
				t.Fatalf("probes=%q err=%v; want re-probe", probes, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("replaced helper received a generation request: %v", err)
			}
		})
	}
}

func TestNativeCapabilityErrorEvent(t *testing.T) {
	for _, capability := range []string{"tools", "private diagnostic", ""} {
		for _, reason := range []string{"not_implemented", "private diagnostic", ""} {
			t.Run(capability+"/"+reason, func(t *testing.T) {
				event := nativeLine("error", map[string]any{
					"kind": "unsupported_capability", "capability": capability, "reason": reason,
				})
				r := fakeNative(t, "cat >/dev/null\n"+nativePrint(event))
				_, err := r.RunComplete(t.Context(), ModelLocal, "fixture")
				if capability != "tools" || reason != "not_implemented" {
					requireNativeKind(t, err, KindNativeProtocol)
					if strings.Contains(err.Error(), "private diagnostic") {
						t.Fatalf("untrusted identifiers leaked: %v", err)
					}
					return
				}
				requireNativeKind(t, err, KindUnsupportedCapability)
				const want = "native helper does not support tools (not_implemented); install the matching bundle"
				if err.Error() != want {
					t.Fatalf("error=%v; want %s", err, want)
				}
			})
		}
	}
}

// The native golden suite supplies its freshly built helper. No status or
// generation is requested: tool mode is refused before any model access.
func TestBuiltHelperNamedCapabilityRefusal(t *testing.T) {
	path := os.Getenv("HOLLIS_TEST_NATIVE_HELPER")
	if path == "" {
		t.Skip("run by the built-helper golden suite")
	}
	r := &NativeRunner{helperPath: path, platformCheck: func(context.Context) error { return nil }}
	_, err := r.RunCompleteWithOptions(t.Context(), ModelLocal, "fixture", nativeOptions{ToolCalling: "allowed"})
	requireNativeKind(t, err, KindUnsupportedCapability)
	const want = "native helper does not support tools (not_implemented); install the matching bundle"
	if err.Error() != want {
		t.Fatalf("error=%v; want %s", err, want)
	}
}

// Frozen event fields and identity checks from v0.4.0 and 0197c2b. Keep these
// independent of nativeEvent so additive current fields cannot weaken the test.
type legacyNativeEvent struct {
	Protocol  int          `json:"protocol"`
	Version   string       `json:"version"`
	Model     Model        `json:"model"`
	Event     string       `json:"event"`
	Available *bool        `json:"available,omitempty"`
	Reason    string       `json:"reason,omitempty"`
	Text      *string      `json:"text,omitempty"`
	Usage     *nativeUsage `json:"usage,omitempty"`
	Kind      Kind         `json:"kind,omitempty"`
}

func decodeLegacyIdentity(data []byte, protocol int, version string) error {
	var base legacyNativeEvent
	var event any = &base
	withSchema := struct {
		legacyNativeEvent
		ContextSize *int            `json:"context_size,omitempty"`
		Structured  json.RawMessage `json:"structured,omitempty"`
	}{}
	if protocol == 2 {
		event = &withSchema
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(event); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nativeError(KindNativeProtocol, "extra native event data")
	}
	if protocol == 2 {
		base = withSchema.legacyNativeEvent
	}
	if base.Protocol != protocol || base.Version != version || base.Model != ModelLocal {
		return helperMismatch()
	}
	return nil
}

func TestOldEraDecodersRejectCurrentVersion(t *testing.T) {
	for protocol, version := range map[int]string{1: "0.4.0", 2: "0.5.0"} {
		for _, stamped := range []string{version, NativeVersion} {
			data, err := json.Marshal(map[string]any{
				"protocol": protocol, "version": stamped, "model": "local",
				"event": "status", "available": true,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = decodeLegacyIdentity(data, protocol, version)
			if stamped == version {
				if err != nil {
					t.Fatalf("matched era %d: %v", protocol, err)
				}
			} else {
				requireNativeKind(t, err, KindNativeProtocol)
			}
		}
	}
}

func TestBuiltHelperOldEraDecoders(t *testing.T) {
	path := os.Getenv("HOLLIS_TEST_NATIVE_HELPER")
	if path == "" {
		t.Skip("run by the built-helper golden suite")
	}
	for protocol, version := range map[int]string{1: "0.4.0", 2: "0.5.0"} {
		// Protocol 1 has no schema operation; protocol 2 rejects this unsupported
		// schema before model access. Both produce the historical error shape.
		request, err := json.Marshal(map[string]any{
			"protocol": protocol, "operation": "schema", "prompt": "fixture",
			"schema": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), path)
		cmd.Stdin = strings.NewReader(string(request))
		data, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		requireNativeKind(t, decodeLegacyIdentity(data, protocol, version), KindNativeProtocol)
		// Changing only the expected version accepts the exact same bytes:
		// format compatibility is separate from bundled version identity.
		if err := decodeLegacyIdentity(data, protocol, NativeVersion); err != nil {
			t.Fatalf("protocol %d request shape: %v", protocol, err)
		}
	}
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
