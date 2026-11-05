package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func metadataStatus(executable bool) string {
	return nativeLine("status", map[string]any{
		"available": true,
		"capabilities": map[string]any{"tools": map[string]any{
			"supported": true, "tool_calling_mode": true, "native_definitions": true,
			"executable": executable, "turn_handoff": executable,
		}},
	})
}

func metadataRound(t *testing.T) ToolRoundRequest {
	t.Helper()
	c := nativeConversation(t, ToolChoiceAuto)
	tools, offers, err := prepareToolOffers(c)
	if err != nil {
		t.Fatal(err)
	}
	instructions, history, err := nativeHistory(c.Messages, tools)
	if err != nil {
		t.Fatal(err)
	}
	return ToolRoundRequest{Native: &NativeToolStep{
		Protocol: NativeProtocol, Operation: "tool_step", RequestID: "test-request",
		Step: 1, RemainingSteps: NativeToolRoundCap, Choice: nativeChoice(c.Choice),
		Instructions: instructions, Tools: nativeDefinitions(offers), History: history,
	}}
}

func nativeRequestStatus(executable bool) string {
	var fields map[string]any
	if err := json.Unmarshal([]byte(metadataStatus(executable)), &fields); err != nil {
		panic(err)
	}
	fields["request_id"] = "test-request"
	data, _ := json.Marshal(fields)
	return string(data)
}

func nativeRequestDone(text string) string {
	return nativePrint(nativeLine("complete", map[string]any{
		"request_id": "test-request", "text": text,
		"usage": Usage{InputTokens: 7, OutputTokens: 3},
	}))
}

func TestNativeMetadataPreflightAndWire(t *testing.T) {
	dir := t.TempDir()
	probe, generation := filepath.Join(dir, "status"), filepath.Join(dir, "generation")
	r := fakeNative(t, "if [ \"$#\" -eq 0 ]; then\ncat > '"+probe+"'\n"+
		nativePrint(metadataStatus(true))+"exit 0\nfi\n"+
		"test \"$1\" = '--native-tools'\ncat > '"+generation+"'\n"+
		nativePrint(nativeRequestStatus(true))+nativeRequestDone("Done."))
	request := metadataRound(t)
	out, err := r.RunToolRound(t.Context(), ModelLocal, request)
	if err != nil || out.Text != "Done." {
		t.Fatalf("out=%+v error=%v", out, err)
	}
	status, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	var statusFields map[string]json.RawMessage
	if err := json.Unmarshal(status, &statusFields); err != nil {
		t.Fatal(err)
	}
	if len(statusFields) != 2 || string(statusFields["operation"]) != `"status"` {
		t.Fatalf("status probe carried generation data: %s", status)
	}
	data, err := os.ReadFile(generation)
	if err != nil {
		t.Fatal(err)
	}
	var decoded NativeToolStep
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, *request.Native) {
		t.Fatalf("native wire changed: %+v", decoded)
	}
	for _, field := range []string{`"tool_definitions"`, `"tool_instructions"`, `"prompt"`} {
		if strings.Contains(string(data), field) {
			t.Fatalf("legacy field %s sent", field)
		}
	}
}

func TestNativeMetadataRefusesBeforeGenerationDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		kind         Kind
	}{
		{"old version", strings.Replace(metadataStatus(true), NativeVersion, "0.6.0", 1), KindNativeProtocol},
		{"metadata only", metadataStatus(false), KindUnsupportedCapability},
		{"absent capability", nativeLine("status", map[string]any{"available": true}), KindUnsupportedCapability},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "generation")
			r := fakeNative(t, "if [ \"$#\" -ne 0 ]; then touch '"+marker+"'; fi\ncat >/dev/null\n"+nativePrint(tc.status))
			_, err := r.RunToolRound(t.Context(), ModelLocal, metadataRound(t))
			requireNativeKind(t, err, tc.kind)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("generation dispatched after failed preflight")
			}
		})
	}
}

func TestNativeMetadataReplacedHelperRejectsMode(t *testing.T) {
	dir := t.TempDir()
	replacement, marker := filepath.Join(dir, "old-helper"), filepath.Join(dir, "generation")
	oldError := nativePrint(nativeLineFor(3, NativeVersion, "error", map[string]any{"kind": "native_failed"}))
	old := "#!/bin/sh\nif [ \"$#\" -eq 1 ] && [ \"$1\" = '--protocol-version' ]; then echo 3; exit 0; fi\n" +
		"if [ \"$#\" -ne 0 ]; then\n" + oldError + "exit 0\nfi\ntouch '" + marker + "'\n"
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
			script := "cat > '" + path + "'\n" + nativePrint(metadataStatus(true)) + nativeRequestDone("Done.")
			if none {
				script = "if [ \"$#\" -eq 0 ]; then\ncat >/dev/null\n" + nativePrint(metadataStatus(true)) + "exit 0\nfi\n" +
					"test \"$1\" = '--native-tools'\ncat > '" + path + "'\n" + nativePrint(nativeRequestStatus(true)) + nativeRequestDone("Done.")
			} else {
				script = "test \"$#\" -eq 0\ncat > '" + path + "'\n" + nativePrint(metadataStatus(true)) + nativeDone("Done.", true)
			}
			r := fakeNative(t, script)
			var err error
			if none {
				request := metadataRound(t)
				request.Native.Choice = NativeToolChoice{Type: "none"}
				_, err = r.RunToolRound(t.Context(), ModelLocal, request)
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
			for _, field := range []string{`"tool_definitions"`, `"tool_instructions"`, `"tool_calling"`} {
				if strings.Contains(string(body), field) {
					t.Fatalf("legacy metadata: %s", body)
				}
			}
			if none {
				var wire NativeToolStep
				if err := json.Unmarshal(body, &wire); err != nil || wire.Choice.Type != "none" {
					t.Fatalf("choice lost: %s", body)
				}
			} else if strings.Contains(string(body), `"tools"`) {
				t.Fatal("plain completion offered tools")
			}
		})
	}
}

func TestNativeMetadataBudgetAndValidationBeforeHelper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*NativeToolStep)
		kind   Kind
	}{
		{"metadata alone", func(r *NativeToolStep) { r.Tools[0].Description = strings.Repeat("x", NativeMaxPromptBytes) }, KindContextCapacity},
		{"aggregate", func(r *NativeToolStep) { text := strings.Repeat("x", NativeMaxPromptBytes); r.History[0].Text = &text }, KindContextCapacity},
		{"invalid choice", func(r *NativeToolStep) { r.Choice.Type = "invented" }, KindUsage},
		{"missing named tool", func(r *NativeToolStep) { r.Choice = NativeToolChoice{Type: "function", Name: "unknown"} }, KindUsage},
		{"invalid schema", func(r *NativeToolStep) {
			r.Tools[0].Parameters = json.RawMessage(`{"type":"object","additionalProperties":true}`)
		}, KindUsage},
		{"repeated name", func(r *NativeToolStep) { r.Tools[1].Name = r.Tools[0].Name }, KindUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := metadataRound(t)
			tc.mutate(request.Native)
			_, err := (&NativeRunner{}).RunToolRound(t.Context(), ModelLocal, request)
			requireNativeKind(t, err, tc.kind)
		})
	}
}

func TestNativeMetadataPreservedOnRepairAndContinuation(t *testing.T) {
	r := &nativeScript{replies: []Completion{nativeReply("call-1", "get_weather", `{"city":"Tokyo"}`), {Text: "Done."}}}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), &recordingExecutor{output: `{"temp_c":18}`})
	if err != nil || out.Rounds != 2 || len(r.requests) != 2 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if !reflect.DeepEqual(r.requests[0].Tools, r.requests[1].Tools) {
		t.Fatal("continuation changed definitions")
	}
	if len(r.requests[1].History) != 3 || r.requests[1].History[0].Type != "user" || r.requests[1].History[1].Type != "calls" || r.requests[1].History[2].Type != "output" {
		t.Fatal("continuation repeated or flattened user history")
	}
}
