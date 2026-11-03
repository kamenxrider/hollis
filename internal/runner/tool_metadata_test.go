// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func metadataStatus(definitions bool) string {
	return nativeLine("status", map[string]any{
		"available": true,
		"capabilities": map[string]any{"tools": map[string]any{
			"supported": true, "tool_calling_mode": true, "native_definitions": definitions,
		}},
	})
}

func metadataRound(t *testing.T) ToolRoundRequest {
	t.Helper()
	return ToolRoundRequest{Prompt: "Weather in Tokyo?", Mode: "allowed",
		Instructions: toolInstructions, Definitions: nativeToolDefinitions(mustTools(t))}
}

func TestNativeMetadataPreflightAndWire(t *testing.T) {
	dir := t.TempDir()
	probe, generation := filepath.Join(dir, "status"), filepath.Join(dir, "generation")
	r := fakeNative(t, "if [ \"$#\" -eq 0 ]; then\ncat > '"+probe+"'\n"+
		nativePrint(metadataStatus(true))+"exit 0\nfi\n"+
		"test \"$1\" = '--native-definitions'\ncat > '"+generation+"'\n"+
		nativePrint(metadataStatus(true))+nativeDone("Done.", true))
	request := metadataRound(t)
	out, err := r.RunToolRound(t.Context(), ModelLocal, request)
	if err != nil || out.Text != "Done." {
		t.Fatalf("out=%+v error=%v", out, err)
	}
	read := func(path string) map[string]json.RawMessage {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		return fields
	}
	status := read(probe)
	if len(status) != 2 || string(status["operation"]) != `"status"` {
		t.Fatalf("status probe carried generation data: %v", status)
	}
	wire := read(generation)
	var decoded nativeRequest
	data, _ := json.Marshal(wire)
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Prompt != request.Prompt || decoded.ToolInstructions != toolInstructions ||
		!reflect.DeepEqual(decoded.ToolDefinitions, request.Definitions) {
		t.Fatalf("metadata wire changed: %+v", decoded)
	}
	if strings.Contains(decoded.Prompt, "parameters") || strings.Contains(decoded.Prompt, toolFramingOpen) {
		t.Fatal("metadata duplicated into prompt")
	}
}

func TestNativeMetadataRefusesBeforeGenerationDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		kind         Kind
	}{
		{"old version", strings.Replace(metadataStatus(true), NativeVersion, "0.6.0", 1), KindNativeProtocol},
		{"mode only", metadataStatus(false), KindUnsupportedCapability},
		{"absent capability", nativeLine("status", map[string]any{"available": true}), KindUnsupportedCapability},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "generation")
			r := fakeNative(t, "if [ \"$#\" -ne 0 ]; then touch '"+marker+"'; fi\ncat >/dev/null\n"+nativePrint(tc.status))
			_, err := r.RunToolRound(t.Context(), ModelLocal, metadataRound(t))
			requireNativeKind(t, err, tc.kind)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("generation process dispatched after failed preflight")
			}
		})
	}
}

func TestNativeMetadataReplacedHelperRejectsMode(t *testing.T) {
	dir := t.TempDir()
	replacement, marker := filepath.Join(dir, "old-helper"), filepath.Join(dir, "generation")
	// Model the 0.6.0 argument contract exactly: unexpected argv returns
	// before stdin, model status, tokenization or generation.
	oldError := strings.Replace(nativeLine("error", map[string]any{"kind": "native_failed"}), NativeVersion, "0.6.0", 1)
	old := "#!/bin/sh\nif [ \"$#\" -eq 1 ] && [ \"$1\" = '--protocol-version' ]; then echo 3; exit 0; fi\n" +
		"if [ \"$#\" -ne 0 ]; then\n" + nativePrint(oldError) + "exit 0\nfi\n" +
		"touch '" + marker + "'\n"
	if err := os.WriteFile(replacement, []byte(old), 0700); err != nil {
		t.Fatal(err)
	}
	r := fakeNative(t, "if [ \"$#\" -eq 0 ]; then\ncat >/dev/null\n"+
		"cp '"+replacement+"' \"$0.next\"\nmv \"$0.next\" \"$0\"\n"+nativePrint(metadataStatus(true))+"exit 0\nfi\nexit 1\n")
	_, err := r.RunToolRound(t.Context(), ModelLocal, metadataRound(t))
	requireNativeKind(t, err, KindNativeProtocol)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("replaced old helper reached generation")
	}
}

func TestNativeMetadataNoneAndPlainOmitEverything(t *testing.T) {
	for _, none := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "none"}[none], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request")
			r := fakeNative(t, "test \"$#\" -eq 0\ncat > '"+path+"'\n"+
				nativePrint(metadataStatus(false))+nativeDone("Done.", true))
			var err error
			if none {
				_, err = r.RunToolRound(t.Context(), ModelLocal, ToolRoundRequest{Prompt: "hello", Mode: "disallowed"})
			} else {
				_, err = r.RunComplete(t.Context(), ModelLocal, "hello")
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "tool_definitions") || strings.Contains(string(body), "tool_instructions") ||
				(!none && strings.Contains(string(body), "tool_calling")) {
				t.Fatalf("unexpected metadata: %s", body)
			}
		})
	}
}

func TestNativeMetadataBudgetAndValidationBeforeHelper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ToolRoundRequest)
		kind   Kind
	}{
		{"metadata alone", func(r *ToolRoundRequest) { r.Definitions[0].Description = strings.Repeat("x", NativeMaxPromptBytes) }, KindContextCapacity},
		{"aggregate", func(r *ToolRoundRequest) {
			r.Prompt = strings.Repeat("x", NativeMaxPromptBytes-r.inputBytes()+len(r.Prompt)+1)
		}, KindContextCapacity},
		{"none with metadata", func(r *ToolRoundRequest) { r.Mode = "disallowed" }, KindUsage},
		{"no instructions", func(r *ToolRoundRequest) { r.Instructions = "" }, KindUsage},
		{"unfixed instructions", func(r *ToolRoundRequest) { r.Instructions = "invented" }, KindUsage},
		{"invalid schema", func(r *ToolRoundRequest) {
			r.Definitions[0].Parameters = json.RawMessage(`{"type":"object","additionalProperties":true}`)
		}, KindUsage},
		{"repeated name", func(r *ToolRoundRequest) { r.Definitions[1].Name = r.Definitions[0].Name }, KindUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := metadataRound(t)
			tc.mutate(&request)
			r := &NativeRunner{} // No helper/platform: admission must fail first.
			_, err := r.RunToolRound(t.Context(), ModelLocal, request)
			requireNativeKind(t, err, tc.kind)
		})
	}
}

func TestNativeMetadataPreservedOnRepairAndContinuation(t *testing.T) {
	tools := mustTools(t)
	r := &scriptedRounds{replies: []string{malformedWeatherCall,
		`{"tool_calls":[{"function":"get_weather","arguments":{"city":"Tokyo"}}]}`, "Done."}}
	executor := &recordingExecutor{output: `{"temp_c":18}`}
	out, err := RunToolLoop(t.Context(), r, ModelLocal,
		ToolConversation{Tools: tools, Choice: ToolChoiceAuto, Messages: userTurn("Weather?")}, executor)
	if err != nil || out.Rounds != 3 || len(r.requests) != 3 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	for _, request := range r.requests {
		if request.Instructions != toolInstructions || !reflect.DeepEqual(request.Definitions, nativeToolDefinitions(tools)) {
			t.Fatal("repair or continuation changed declared metadata")
		}
	}
}
