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

// nativeProtocolHelper answers the non-generative --protocol-version probe with
// version and then runs body for a real request, mirroring the shipped helper's
// metadata commands.
func nativeProtocolHelper(t *testing.T, version, body string) *NativeRunner {
	t.Helper()
	probe := "if [ \"${1:-}\" = \"--protocol-version\" ]; then printf '%s\\n' '" + version + "'; exit 0; fi\n"
	return fakeNative(t, probe+body)
}

// nativeLineFor stamps an event for an explicit protocol and version, which is
// how a stale helper is modelled.
func nativeLineFor(protocol int, version, event string, fields map[string]any) string {
	stamped := map[string]any{}
	for key, value := range fields {
		stamped[key] = value
	}
	stamped["protocol"], stamped["version"], stamped["model"], stamped["event"] = protocol, version, ModelLocal, event
	b, _ := json.Marshal(stamped)
	return string(b)
}

const guidedSchema = `{"type":"object","additionalProperties":false,"required":["name","age"],` +
	`"properties":{"name":{"type":"string","enum":["a","b"]},"age":{"type":"integer","minimum":0}}}`

func mustParseSchema(t *testing.T, text string) *Schema {
	t.Helper()
	schema, err := ParseSchema([]byte(text))
	if err != nil {
		t.Fatalf("ParseSchema: %v", err)
	}
	return schema
}

func nativeUsageEvent() map[string]any {
	return map[string]any{"input_tokens": 11, "output_tokens": 5, "reasoning_tokens": 1}
}

func TestNativeSchemaRequestCarriesSchemaAndValidatedObject(t *testing.T) {
	requestFile := filepath.Join(t.TempDir(), "request.json")
	structured := map[string]any{"name": "a", "age": 3}
	encoded, _ := json.Marshal(structured)
	body := "cat > '" + requestFile + "'\n" +
		nativePrint(nativeLine("status", map[string]any{"available": true, "context_size": 4096})) +
		nativePrint(nativeLine("complete", map[string]any{
			"text": string(encoded), "structured": structured, "usage": nativeUsageEvent(),
		}))
	r := nativeProtocolHelper(t, "2", body)
	schema := mustParseSchema(t, guidedSchema)
	result, err := r.RunSchema(t.Context(), ModelLocal, "produce a record", schema)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != ModelLocal || result.Text != string(encoded) {
		t.Fatalf("result %#v", result)
	}
	if string(result.Structured) != string(encoded) {
		t.Fatalf("structured = %s; want %s", result.Structured, encoded)
	}
	if result.Usage == nil || *result.Usage != (Usage{InputTokens: 11, OutputTokens: 5, ReasoningTokens: 1}) {
		t.Fatalf("usage %#v", result.Usage)
	}
	raw, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	var request nativeRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Protocol != NativeProtocol || request.Operation != "schema" || request.Prompt != "produce a record" {
		t.Fatalf("request %#v", request)
	}
	if string(request.Schema) != string(schema.Bytes()) {
		t.Fatalf("request schema = %s; want the normalized encoding %s", request.Schema, schema.Bytes())
	}
}

func TestNativeStatusReportsContextSizeOnlyWhenMeasured(t *testing.T) {
	withSize := fakeNative(t, "cat >/dev/null\n"+nativePrint(nativeLine("status", map[string]any{"available": true, "context_size": 4096})))
	status, err := withSize.NativeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.ContextSize != 4096 || status.Protocol != NativeProtocol || status.Version != NativeVersion {
		t.Fatalf("status %#v", status)
	}
	withoutSize := fakeNative(t, "cat >/dev/null\n"+nativePrint(nativeLine("status", map[string]any{"available": true})))
	status, err = withoutSize.NativeStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.ContextSize != 0 {
		t.Fatalf("context size = %d; want absent (0)", status.ContextSize)
	}
	// A measured context window is a positive count; zero or a negative value
	// is a protocol error rather than an absence.
	invalid := fakeNative(t, "cat >/dev/null\n"+nativePrint(nativeLine("status", map[string]any{"available": true, "context_size": 0})))
	_, err = invalid.NativeStatus(t.Context())
	requireNativeKind(t, err, KindNativeProtocol)
}

func TestNativeSchemaRejectsNonConformingReturnedPayload(t *testing.T) {
	cases := []struct {
		name       string
		structured map[string]any
	}{
		{"missing required", map[string]any{"name": "a"}},
		{"extra property", map[string]any{"name": "a", "age": 3, "extra": true}},
		{"wrong enum", map[string]any{"name": "z", "age": 3}},
		{"wrong type", map[string]any{"name": "a", "age": "3"}},
		{"fractional integer", map[string]any{"name": "a", "age": 1.5}},
		{"below minimum", map[string]any{"name": "a", "age": -1}},
		{"empty payload", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, _ := json.Marshal(tc.structured)
			body := "cat >/dev/null\n" + nativePrint(nativeLine("status", map[string]any{"available": true})) +
				nativePrint(nativeLine("complete", map[string]any{
					"text": string(encoded), "structured": tc.structured, "usage": nativeUsageEvent(),
				}))
			r := nativeProtocolHelper(t, "2", body)
			result, err := r.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
			requireNativeKind(t, err, KindNativeProtocol)
			if !strings.Contains(err.Error(), "returned payload does not match the requested schema") {
				t.Fatalf("error = %v; want the fixed payload sentence", err)
			}
			if result.Structured != nil || result.Text != "" {
				t.Fatalf("result %#v; want no partial success", result)
			}
		})
	}
}

func TestNativeSchemaRejectsStructuredOutsideTerminalCompletion(t *testing.T) {
	structured := map[string]any{"name": "a", "age": 3}
	snapshotWithStructured := nativePrint(nativeLine("snapshot", map[string]any{"text": "x", "structured": structured}))
	statusWithStructured := nativePrint(nativeLine("status", map[string]any{"available": true, "structured": structured}))
	contextSizeOnComplete := nativePrint(nativeLine("complete", map[string]any{
		"text": `{"name":"a","age":3}`, "structured": structured, "usage": nativeUsageEvent(), "context_size": 4096,
	}))
	cases := []struct {
		name    string
		body    string
		op      string
		message string
	}{
		{"status op carries structured", "cat >/dev/null\n" + statusWithStructured, "status", "unexpected structured output"},
		{"status carries structured", "cat >/dev/null\n" + statusWithStructured, "schema", "unexpected structured output"},
		{"snapshot carries structured", "cat >/dev/null\n" + nativePrint(nativeLine("status", map[string]any{"available": true})) + snapshotWithStructured, "schema", "unexpected structured output"},
		{"complete carries context_size", "cat >/dev/null\n" + nativePrint(nativeLine("status", map[string]any{"available": true})) + contextSizeOnComplete, "schema", "unexpected native context size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := nativeProtocolHelper(t, "2", tc.body)
			if tc.op == "status" {
				_, err := r.NativeStatus(t.Context())
				requireNativeKind(t, err, KindNativeProtocol)
				if !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("error = %v; want %q", err, tc.message)
				}
				return
			}
			_, err := r.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
			requireNativeKind(t, err, KindNativeProtocol)
			if !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v; want %q", err, tc.message)
			}
		})
	}
	// A complete event without structured output is not a guided success.
	missing := nativeProtocolHelper(t, "2", "cat >/dev/null\n"+
		nativePrint(nativeLine("status", map[string]any{"available": true}))+
		nativePrint(nativeLine("complete", map[string]any{"text": "plain", "usage": nativeUsageEvent()})))
	_, err := missing.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindNativeProtocol)
	// Structured output on a plain complete request is equally unexpected.
	unrequested := nativeProtocolHelper(t, "2", "cat >/dev/null\n"+
		nativePrint(nativeLine("status", map[string]any{"available": true}))+
		nativePrint(nativeLine("complete", map[string]any{"text": "plain", "structured": structured, "usage": nativeUsageEvent()})))
	_, err = unrequested.RunComplete(t.Context(), ModelLocal, "prompt")
	requireNativeKind(t, err, KindNativeProtocol)
}

func TestNativeSchemaRefusesOldHelperBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "request-received")
	// A protocol-1 helper answers the probe with 1 and would write the marker
	// as soon as it received a request. The refusal must happen before that.
	body := ": > '" + marker + "'\ncat >/dev/null\n" +
		nativePrint(nativeLine("status", map[string]any{"available": true}))
	r := nativeProtocolHelper(t, "1", body)
	_, err := r.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindUsage)
	if !strings.Contains(err.Error(), "does not support guided output") {
		t.Fatalf("error = %v; want the guided-output refusal", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("helper received a request despite the pre-spawn refusal (stat err %v)", statErr)
	}
	// Plain completion is unchanged by the probe: the same old helper is asked
	// and its protocol-1 events fail closed on identity, not on a new path.
	_, err = r.RunComplete(t.Context(), ModelLocal, "prompt")
	requireNativeKind(t, err, KindNativeProtocol)
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("plain completion never reached the helper: %v", statErr)
	}
}

func TestNativeSchemaRefusesUnusableProtocolProbe(t *testing.T) {
	silent := fakeNative(t, "if [ \"${1:-}\" = \"--protocol-version\" ]; then exit 1; fi\ncat >/dev/null\n")
	_, err := silent.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindNativeFailed)

	garbage := nativeProtocolHelper(t, "two", "cat >/dev/null\n")
	_, err = garbage.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindNativeFailed)

	outOfRange := nativeProtocolHelper(t, "100", "cat >/dev/null\n")
	_, err = outOfRange.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindNativeFailed)

	newer := nativeProtocolHelper(t, "3", "cat >/dev/null\n")
	_, err = newer.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindNativeProtocol)
	if !strings.Contains(err.Error(), "install the matching bundle") {
		t.Fatalf("error = %v; want the matching-bundle message", err)
	}

	// A helper that never answers the metadata command is a probe timeout, not
	// a protocol mismatch and not a silent success.
	previous := helperProbeTimeout
	helperProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { helperProbeTimeout = previous })
	hanging := fakeNative(t, "if [ \"${1:-}\" = \"--protocol-version\" ]; then sleep 30; fi\ncat >/dev/null\n")
	_, err = hanging.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindTimeout)
}

func TestNativeProtocolOneHelperFailsClosedOnIdentity(t *testing.T) {
	// A 0.4.0 helper stamps events with protocol 1 and version 0.4.0. The
	// request never reaches generation: the per-event identity check rejects
	// it, so a half-switched install cannot degrade silently into plain text.
	stale := func(event string, fields map[string]any) string {
		return nativePrint(nativeLineFor(1, "0.4.0", event, fields))
	}
	r := fakeNative(t, "cat >/dev/null\n"+
		stale("status", map[string]any{"available": true})+
		stale("complete", map[string]any{"text": "plain", "usage": nativeUsageEvent()}))
	_, err := r.RunComplete(t.Context(), ModelLocal, "prompt")
	requireNativeKind(t, err, KindNativeProtocol)
	if !strings.Contains(err.Error(), "install the matching bundle") {
		t.Fatalf("error = %v; want the matching-bundle message", err)
	}
	// The same stale helper reports protocol 1 on the metadata probe.
	probeOnly := fakeNative(t, "if [ \"${1:-}\" = \"--protocol-version\" ]; then printf '1\\n'; exit 0; fi\ncat >/dev/null\n")
	_, err = probeOnly.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindUsage)
}

func TestNativeSchemaRejectsUnavailableModelWithoutStructuredOutput(t *testing.T) {
	body := "cat >/dev/null\n" + nativePrint(nativeLine("status", map[string]any{"available": false, "reason": "device_not_eligible"}))
	r := nativeProtocolHelper(t, "2", body)
	_, err := r.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindLocalUnavailable)
}

func TestNativeSchemaDeclinedEmitsNoStructuredOutput(t *testing.T) {
	body := "cat >/dev/null\n" + nativePrint(nativeLine("status", map[string]any{"available": true})) +
		nativePrint(nativeLine("error", map[string]any{"kind": "request_declined"}))
	r := nativeProtocolHelper(t, "2", body)
	result, err := r.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindRequestDeclined)
	if result.Structured != nil {
		t.Fatalf("result %#v; want no structured output on refusal", result)
	}
}

func TestNativeSchemaDeadlineOmitsStructuredOutput(t *testing.T) {
	body := "cat >/dev/null\n" + nativePrint(nativeLine("status", map[string]any{"available": true})) + "sleep 5\n"
	r := nativeProtocolHelper(t, "2", body)
	r.Timeout = 300 * time.Millisecond
	result, err := r.RunSchema(t.Context(), ModelLocal, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindTimeout)
	if result.Structured != nil {
		t.Fatalf("result %#v; want no structured output on timeout", result)
	}
}

func TestRoutedRunnerRejectsGuidedOutputForShortcutRoutes(t *testing.T) {
	r := NewRouted()
	_, err := r.RunSchema(context.Background(), ModelCloud, "prompt", mustParseSchema(t, guidedSchema))
	requireNativeKind(t, err, KindUsage)
	if !strings.Contains(err.Error(), "only for model local") {
		t.Fatalf("error = %v", err)
	}
}
