package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
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
