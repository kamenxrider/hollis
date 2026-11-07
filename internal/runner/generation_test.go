// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/kamenxrider/hollis/internal/store"
)

func TestNativeGenerationOptionsReachEveryOperation(t *testing.T) {
	temperature, topP, maxTokens, seed := 0.7, 0.8, 12, uint64(18446744073709551615)
	options := &GenerationOptions{Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens, Seed: &seed}
	for _, operation := range []string{"complete", "stream", "schema"} {
		t.Run(operation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request")
			fields := map[string]any{"text": "Sampled."}
			var schema *Schema
			if operation == "schema" {
				schema = mustParseSchema(t, `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
				fields["text"] = `{"city":"Tokyo"}`
				fields["structured"] = map[string]any{"city": "Tokyo"}
			}
			body := "cat > '" + path + "'\n" +
				nativePrint(nativeLine("status", map[string]any{"available": true, "context_size": 8192}))
			var emit func(string) error
			var text string
			if operation == "stream" {
				body += nativePrint(nativeLine("snapshot", map[string]any{"text": "Sampled."}))
				emit = func(delta string) error { text += delta; return nil }
			} else {
				fields["usage"] = nativeUsageEvent()
			}
			body += nativePrint(nativeLine("complete", fields))
			r := nativeProtocolHelper(t, strconv.Itoa(NativeProtocol), body)
			result, err := r.RunGeneration(t.Context(), ModelLocal, GenerationRequest{Prompt: "Hello", Schema: schema, Options: options}, emit)
			if err != nil || result.Text != fields["text"] || (operation == "stream" && text != result.Text) {
				t.Fatalf("operation=%s result=%+v err=%v streamed=%q", operation, result, err, text)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var wire nativeRequest
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Operation != operation || wire.Protocol != 4 || wire.Prompt != "Hello" ||
				!reflect.DeepEqual(wire.GenerationOptions, options) {
				t.Fatalf("wire changed: %s", raw)
			}
		})
	}
}

func TestNativeGenerationSessionFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	r := fakeNative(t, "cat > '"+path+"'\n"+nativeReady()+nativeDone("ok", true))
	_, err := r.RunGeneration(t.Context(), ModelLocal, GenerationRequest{
		Prompt: "next", Instructions: "be brief", Permissive: true,
		History: []store.Message{{Role: "user", Content: "before"}, {Role: "assistant", Content: "answer"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request["instructions"] != "be brief" || request["permissive"] != true {
		t.Fatal(string(data))
	}
	history := request["history"].([]any)
	if len(history) != 2 || history[0].(map[string]any)["role"] != "user" || len(history[0].(map[string]any)) != 2 {
		t.Fatal(string(data))
	}
}

func TestNativeTokenCountDoesNotGenerate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	r := fakeNative(t, "cat > '"+path+"'\n"+
		nativePrint(nativeLine("status", map[string]any{"available": true, "context_size": 4096}))+
		nativePrint(nativeLine("checked", map[string]any{"input_tokens": 3500})))
	count, err := r.CountTokens(t.Context(), GenerationRequest{Prompt: "count me", Instructions: "brief"}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if count.Tokens != 3500 || count.ContextSize != 4096 || count.Fits {
		t.Fatalf("%+v", count)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var request nativeRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Operation != "count_tokens" || request.Instructions != "brief" {
		t.Fatal(string(data))
	}
}
