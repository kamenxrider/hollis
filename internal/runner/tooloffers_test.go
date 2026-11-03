// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/store"
)

const rawOfferSchema = `{"type":"object","additionalProperties":true,"properties":{"n":{"type":"number","default":7}}}`

func mustMCPOffer(t *testing.T, name string, raw bool) ToolOffer {
	t.Helper()
	schema := json.RawMessage(emptyToolParameters)
	if raw {
		schema = json.RawMessage(rawOfferSchema)
	}
	offer, err := NewMCPToolOffer(name, "Server tool", schema, raw)
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func TestHybridEmptyMCPDescriptionsRemainOffered(t *testing.T) {
	for _, raw := range []bool{false, true} {
		offer, err := NewMCPToolOffer("server__empty", "", json.RawMessage(emptyToolParameters), raw)
		if err != nil {
			t.Fatal(err)
		}
		_, request, err := prepareToolOffers(ToolConversation{Offers: []ToolOffer{offer}})
		if err != nil {
			t.Fatal(err)
		}
		options := nativeOptions{ToolCalling: request.Mode, ToolInstructions: request.Instructions,
			ToolDefinitions: request.Definitions, RawToolDefinitions: request.RawDefinitions, HybridTools: request.Hybrid}
		if err := options.validate(); err != nil {
			t.Fatal(err)
		}
	}
	tool := mustTools(t)[0]
	tool.Description = ""
	if _, _, err := prepareToolOffers(ToolConversation{Tools: []Tool{tool}}); err == nil {
		t.Fatal("empty caller description was accepted")
	}
}

func TestHybridOutcomeIncludesInputWithoutFinalText(t *testing.T) {
	input := []store.Message{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "earlier answer"}, {Role: "user", Content: "now"}}
	for _, choice := range []ToolChoice{ToolChoiceAuto, ToolChoiceNone} {
		r := &scriptedRounds{replies: []string{"final answer"}}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, ToolConversation{
			Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true)}, Messages: input, Choice: choice,
		}, nil)
		if err != nil || out.Text != "final answer" || !reflect.DeepEqual(out.Messages, input) {
			t.Fatalf("plain outcome changed input or duplicated answer: %+v %v", out, err)
		}
		out.Messages[0].Content = "mutated"
		if input[0].Content != "earlier" {
			t.Fatal("outcome aliases caller's input slice")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := RunToolLoop(ctx, &scriptedRounds{}, ModelLocal, ToolConversation{
		Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true)}, Messages: input, Choice: ToolChoiceAuto,
	}, nil)
	if err == nil || out.Rounds != 0 || !reflect.DeepEqual(out.Messages, input) {
		t.Fatalf("zero-dispatch outcome lost input: %+v %v", out, err)
	}
}

func TestHybridOfferAdmission(t *testing.T) {
	for _, tc := range []struct {
		callers, mcp int
		fail         bool
	}{
		{32, 0, false}, {33, 0, true}, {0, 128, false}, {0, 129, true},
		{32, 96, false}, {32, 97, true}, {1, 127, false}, {1, 128, true},
	} {
		t.Run(fmt.Sprintf("%d-%d", tc.callers, tc.mcp), func(t *testing.T) {
			c := ToolConversation{}
			for i := 0; i < tc.callers; i++ {
				tool := mustTools(t)[0]
				tool.Name = fmt.Sprintf("caller_%d", i)
				c.Tools = append(c.Tools, tool)
			}
			for i := 0; i < tc.mcp; i++ {
				c.Offers = append(c.Offers, mustMCPOffer(t, fmt.Sprintf("server__t%d", i), false))
			}
			tools, request, err := prepareToolOffers(c)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
			if tc.fail {
				return
			}
			if len(tools) != tc.callers+tc.mcp || request.Hybrid != (tc.mcp > 0) {
				t.Fatal("incorrect source admission")
			}
			opts := nativeOptions{ToolCalling: request.Mode, ToolInstructions: request.Instructions, ToolDefinitions: request.Definitions, HybridTools: request.Hybrid}
			if err := opts.validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	c := ToolConversation{Tools: mustTools(t)}
	c.Offers = []ToolOffer{mustMCPOffer(t, c.Tools[0].Name, true)}
	if _, _, err := prepareToolOffers(c); err == nil {
		t.Fatal("cross-tier name collision admitted")
	}
	c = ToolConversation{Offers: []ToolOffer{{}}}
	if _, _, err := prepareToolOffers(c); err == nil {
		t.Fatal("untagged offer admitted")
	}
	c = ToolConversation{Tools: []Tool{{Name: "nil_schema", Description: "Not Tier2"}}}
	if _, _, err := prepareToolOffers(c); err == nil {
		t.Fatal("nil Schema treated as Tier2")
	}
	if _, err := NewMCPToolOffer("server__t", "raw", json.RawMessage(rawOfferSchema), false); err == nil {
		t.Fatal("unsupported native schema silently fell back")
	}
	for _, schema := range []string{`null`, `[]`, `{"x":1,"x":2}`, `{"x":`, "{\"x\":\"" + strings.Repeat("a", 2048) + "\"}"} {
		if _, err := NewMCPToolOffer("server__t", "raw", json.RawMessage(schema), true); err == nil {
			t.Fatalf("invalid schema admitted: %d bytes", len(schema))
		}
	}
}

func TestHybridRawSchemaBudget(t *testing.T) {
	schema := `{"description":"` + strings.Repeat("x", 2048-len(`{"description":""}`)) + `"}`
	c := ToolConversation{}
	for i := 0; i < 8; i++ {
		offer, err := NewMCPToolOffer(fmt.Sprintf("server__t%d", i), "bounded", json.RawMessage(schema), true)
		if err != nil {
			t.Fatal(err)
		}
		c.Offers = append(c.Offers, offer)
	}
	_, request, err := prepareToolOffers(c)
	if err != nil || len(request.Definitions) != 0 || len(request.RawDefinitions) != 8 {
		t.Fatalf("request=%+v error=%v", request, err)
	}
	c.Offers = append(c.Offers, mustMCPOffer(t, "server__overflow", true))
	if _, _, err := prepareToolOffers(c); err == nil {
		t.Fatal("aggregate raw schema cap not enforced")
	}
}

func TestHybridAllRawCountAndBatchValidation(t *testing.T) {
	c := ToolConversation{Choice: ToolChoiceAuto, Messages: userTurn("bounded catalog")}
	for i := 0; i < 128; i++ {
		c.Offers = append(c.Offers, mustMCPOffer(t, fmt.Sprintf("server__t%d", i), true))
	}
	_, request, err := prepareToolOffers(c)
	if err != nil || len(request.RawDefinitions) != 128 || len(request.Definitions) != 0 {
		t.Fatalf("raw count admission: %v", err)
	}
	if err := (nativeOptions{ToolCalling: request.Mode, ToolInstructions: request.Instructions, RawToolDefinitions: request.RawDefinitions, HybridTools: true}).validate(); err != nil {
		t.Fatal(err)
	}
	c.Offers = append(c.Offers, mustMCPOffer(t, "server__overflow", true))
	if _, _, err := prepareToolOffers(c); err == nil {
		t.Fatal("129 raw offers admitted")
	}

	c.Offers = c.Offers[:1]
	c.Tools = mustTools(t)
	invalidBatch := `{"tool_calls":[{"function":{"name":"server__t0","arguments":{}}},{"function":{"name":"get_weather","arguments":{}}}]}`
	r := &scriptedRounds{replies: []string{invalidBatch, invalidBatch}}
	executor := &recordingExecutor{output: "must not execute"}
	_, err = RunToolLoop(t.Context(), r, ModelLocal, c, executor)
	requireNativeKind(t, err, KindToolCallInvalid)
	if executor.calls != 0 {
		t.Fatal("valid prefix executed before full-batch validation")
	}
}

func TestHybridPhysicalCapStillPreventsDispatch(t *testing.T) {
	valid := `{"tool_calls":[{"function":{"name":"server__raw","arguments":{}}}]}`
	malformed := `{"tool_calls":[{"function":{"name":"server__raw","arguments":"{`
	r := &scriptedRounds{replies: []string{malformed, valid, valid, "must not generate"}}
	executor := &recordingExecutor{output: "framed"}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, ToolConversation{
		Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true)}, Choice: ToolChoiceAuto, Messages: userTurn("cap")}, executor)
	requireNativeKind(t, err, KindToolRoundCap)
	if out.Rounds != 3 || len(r.requests) != 3 || executor.calls != 1 || len(out.Messages) != 3 {
		t.Fatalf("out=%+v dispatched=%d", out, executor.calls)
	}
}

type toolExecutorFunc func(context.Context, []ToolCall) (ToolExecution, error)

func TestHybridIncrementalBudgetIncludesHistoryCallsAndMetadata(t *testing.T) {
	calls := []ToolCall{
		{ID: "one", Name: "server__raw", Arguments: "{}"},
		{ID: "two", Name: "server__raw", Arguments: "{}"},
		{ID: "three", Name: "server__raw", Arguments: "{}"},
	}
	raw := mustMCPOffer(t, "server__raw", true)
	raw.description = strings.Repeat("metadata", 100)
	conversation := ToolConversation{
		Tools: mustTools(t), Offers: []ToolOffer{raw}, Choice: ToolChoiceAuto,
		Messages: []store.Message{{Role: "user", Content: strings.Repeat("history", 1000)}, {Role: "assistant", Content: "Prior answer"}, {Role: "user", Content: "Now use tools"}},
	}
	_, request, err := prepareToolOffers(conversation)
	if err != nil {
		t.Fatal(err)
	}
	messages := append([]store.Message(nil), conversation.Messages...)
	messages = append(messages,
		store.Message{Role: "assistant", Content: chat.EncodeToolCalls(toolCallRecords(calls))},
		store.Message{Role: chat.RoleTool, Content: chat.EncodeToolResult("one", "")})
	request.Prompt = chat.RenderToolTranscript("", messages, "")
	room := NativeMaxPromptBytes - request.inputBytes()
	for _, tc := range []struct {
		name             string
		size, dispatched int
		fail             bool
	}{
		{"one-byte-over", room + 1, 1, true},
		{"exact-first-limit", room, 2, true},
		{"valid", 10, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &scriptedRounds{replies: []string{
				`{"tool_calls":[{"id":"one","function":"server__raw","arguments":{}},{"id":"two","function":"server__raw","arguments":{}},{"id":"three","function":"server__raw","arguments":{}}]}`, "Done."}}
			executor := &recordingExecutor{output: strings.Repeat("x", tc.size)}
			out, err := RunToolLoop(t.Context(), r, ModelLocal, conversation, executor)
			if tc.fail {
				requireNativeKind(t, err, KindContextCapacity)
			} else if err != nil {
				t.Fatal(err)
			}
			wantRounds := 2
			if tc.fail {
				wantRounds = 1
			}
			if executor.calls != tc.dispatched || len(r.requests) != wantRounds ||
				len(out.Messages) != len(conversation.Messages)+1+tc.dispatched {
				t.Fatalf("wrong partial result accounting: dispatched=%d rounds=%d messages=%d", executor.calls, len(r.requests), len(out.Messages))
			}
			if !reflect.DeepEqual(out.Messages[:len(conversation.Messages)], conversation.Messages) {
				t.Fatal("input history changed")
			}
			id, output, ok := chat.DecodeToolResult(out.Messages[len(out.Messages)-1].Content)
			if !ok || id != calls[tc.dispatched-1].ID || output != executor.output {
				t.Fatal("last completed result was lost or truncated")
			}
		})
	}
}

func (f toolExecutorFunc) ExecuteTools(ctx context.Context, calls []ToolCall, admit func(ToolResult) error) (ToolExecution, error) {
	execution, err := f(ctx, calls)
	if err == nil {
		for _, result := range execution.Results {
			if err = admit(result); err != nil {
				break
			}
		}
	}
	return execution, err
}

func TestHybridPendingStopsAndOrdersTranscript(t *testing.T) {
	reply := `{"tool_calls":[{"id":"p1","function":{"name":"get_weather","arguments":{"city":"Tokyo"}}},{"id":"r1","function":{"name":"server__raw","arguments":{"n":1e400,"s":"12"}}},{"id":"r2","function":{"name":"server__native","arguments":{}}},{"id":"p2","function":{"name":"get_weather","arguments":{"city":"Osaka"}}}]}`
	r := &scriptedRounds{replies: []string{reply, "must not continue"}}
	c := ToolConversation{Tools: mustTools(t), Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true), mustMCPOffer(t, "server__native", false)}, Choice: ToolChoiceAuto, Messages: userTurn("run mixed")}
	executor := toolExecutorFunc(func(_ context.Context, calls []ToolCall) (ToolExecution, error) {
		if calls[1].Arguments != `{"n":1e400,"s":"12"}` {
			t.Fatalf("raw argument changed: %s", calls[1].Arguments)
		}
		return ToolExecution{Results: []ToolResult{{CallID: "r2", Output: "second"}, {CallID: "r1", Output: "first"}},
			Pending: []ToolCall{calls[3], calls[0]}}, nil
	})
	out, err := RunToolLoop(t.Context(), r, ModelLocal, c, executor)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.requests) != 1 || out.Rounds != 1 || len(out.Calls) != 2 || out.Calls[0].ID != "p1" || out.Calls[1].ID != "p2" {
		t.Fatalf("out=%+v", out)
	}
	if len(out.Messages) != 4 {
		t.Fatalf("messages=%+v", out.Messages)
	}
	calls, ok := chat.DecodeToolCalls(out.Messages[1].Content)
	if !ok || len(calls) != 4 {
		t.Fatal("original full assistant call batch missing")
	}
	for i, want := range []string{"r1", "r2"} {
		id, _, ok := chat.DecodeToolResult(out.Messages[i+2].Content)
		if !ok || id != want {
			t.Fatal("results not in original call order")
		}
	}
	if !r.requests[0].Hybrid || len(r.requests[0].Definitions) != len(c.Tools)+1 || len(r.requests[0].RawDefinitions) != 1 {
		t.Fatal("hybrid metadata missing")
	}
}

func TestHybridExecutorDispositions(t *testing.T) {
	calls := []ToolCall{{ID: "one", Name: "a", Arguments: "{}"}, {ID: "two", Name: "b", Arguments: "{}"}}
	for _, tc := range []struct {
		name          string
		execution     ToolExecution
		partial, fail bool
	}{
		{"all pending", ToolExecution{Pending: calls}, false, false},
		{"split", ToolExecution{Results: []ToolResult{{CallID: "one"}}, Pending: calls[1:]}, false, false},
		{"missing", ToolExecution{Pending: calls[:1]}, false, true},
		{"duplicate result", ToolExecution{Results: []ToolResult{{CallID: "one"}, {CallID: "one"}}}, false, true},
		{"duplicate pending", ToolExecution{Pending: []ToolCall{calls[0], calls[0]}}, false, true},
		{"both", ToolExecution{Results: []ToolResult{{CallID: "one"}}, Pending: calls}, false, true},
		{"unknown result", ToolExecution{Results: []ToolResult{{CallID: "other"}}}, true, true},
		{"mutated pending", ToolExecution{Pending: []ToolCall{{ID: "one", Name: "different", Arguments: "{}"}}}, true, true},
		{"partial error", ToolExecution{Results: []ToolResult{{CallID: "one"}}}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := matchExecution(calls, tc.execution, tc.partial)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestHybridPartialFailurePreservesCompletedMessages(t *testing.T) {
	r := &scriptedRounds{replies: []string{`{"tool_calls":[{"id":"one","function":{"name":"server__raw","arguments":{}}},{"id":"two","function":{"name":"server__raw","arguments":{}}}]}`}}
	stop := errors.New("adapter failure")
	out, err := RunToolLoop(t.Context(), r, ModelLocal, ToolConversation{Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true)}, Choice: ToolChoiceAuto, Messages: userTurn("two calls")},
		toolExecutorFunc(func(_ context.Context, calls []ToolCall) (ToolExecution, error) {
			return ToolExecution{Results: []ToolResult{{CallID: calls[0].ID, Output: "completed"}}}, stop
		}))
	if !errors.Is(err, stop) || out.Rounds != 1 || len(out.Calls) != 0 || len(out.Messages) != 3 || out.Messages[2].Role != "tool" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestHybridRawValidationRepairAndContinuation(t *testing.T) {
	c := ToolConversation{Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true)}, Choice: ToolChoiceAuto, Messages: userTurn("raw only")}
	for _, args := range []string{`[]`, `null`, `"not JSON"`, `"{\"x\":1,\"x\":2}"`, encodeCompact(map[string]string{"large": strings.Repeat("x", MaxToolArgumentsBytes)})} {
		t.Run(fmt.Sprintf("args-%d", len(args)), func(t *testing.T) {
			reply := `{"tool_calls":[{"function":{"name":"server__raw","arguments":` + args + `}}]}`
			r := &scriptedRounds{replies: []string{reply, reply}}
			executed := false
			_, err := RunToolLoop(t.Context(), r, ModelLocal, c, toolExecutorFunc(func(context.Context, []ToolCall) (ToolExecution, error) { executed = true; return ToolExecution{}, nil }))
			requireNativeKind(t, err, KindToolCallInvalid)
			if len(r.requests) != 2 || executed {
				t.Fatal("invalid batch executed or repair count changed")
			}
		})
	}
	r := &scriptedRounds{replies: []string{`{"tool_calls":[{"function":{"name":"server__raw","arguments":{"s":"12","huge":1e400}}}]}`, "done"}}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, c, &recordingExecutor{output: "framed result"})
	if err != nil || out.Text != "done" || len(out.Messages) != 3 || out.Messages[2].Role != "tool" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(r.requests[0].Definitions) != 0 || !reflect.DeepEqual(r.requests[0].RawDefinitions, r.requests[1].RawDefinitions) {
		t.Fatal("raw-only fabricated native definitions or changed metadata")
	}
	r = &scriptedRounds{replies: []string{`{"tool_calls":[{"function":{"name":"unknown","arguments":{}}}]}`}}
	out, err = RunToolLoop(t.Context(), r, ModelLocal, c, nil)
	if err != nil || out.Text == "" || out.Rounds != 1 {
		t.Fatal("unknown tool no longer plain text")
	}
}

func hybridStatus(native, hybrid bool) string {
	return nativeLine("status", map[string]any{"available": true, "capabilities": map[string]any{"tools": map[string]any{
		"supported": true, "tool_calling_mode": true, "native_definitions": native, "hybrid_tools": hybrid,
	}}})
}

func TestHybridNativeWireAndCapabilityPreflight(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw-%v", raw), func(t *testing.T) {
			_, request, err := prepareToolOffers(ToolConversation{Offers: []ToolOffer{mustMCPOffer(t, "server__t", raw)}})
			if err != nil {
				t.Fatal(err)
			}
			request.Prompt = "hello"
			marker := filepath.Join(t.TempDir(), "generation")
			r := fakeNative(t, "if [ \"$#\" -ne 0 ]; then touch '"+marker+"'; fi\ncat >/dev/null\n"+nativePrint(metadataStatus(true)))
			_, err = r.RunToolRound(t.Context(), ModelLocal, request)
			requireNativeKind(t, err, KindUnsupportedCapability)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("old helper received MCP generation")
			}
			wirePath := filepath.Join(t.TempDir(), "request")
			// Raw-only requests intentionally need no native definitions capability.
			status := hybridStatus(!raw, true)
			r = fakeNative(t, "if [ \"$#\" -eq 0 ]; then\ncat >/dev/null\n"+nativePrint(status)+"exit 0\nfi\n"+
				"test \"$1\" = '--hybrid-tools'\ncat > '"+wirePath+"'\n"+nativePrint(status)+nativeDone("done", true))
			if _, err = r.RunToolRound(t.Context(), ModelLocal, request); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(wirePath)
			if err != nil {
				t.Fatal(err)
			}
			var wire nativeRequest
			if err = json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if !wire.HybridTools || !reflect.DeepEqual(wire.RawToolDefinitions, request.RawDefinitions) || !reflect.DeepEqual(wire.ToolDefinitions, request.Definitions) {
				t.Fatal("metadata wire changed")
			}
			if raw && strings.Contains(string(data), `"tool_definitions"`) {
				t.Fatal("fabricated all-Tier2 native definitions")
			}
			request.Prompt = strings.Repeat("x", NativeMaxPromptBytes-request.inputBytes()+len(request.Prompt)+1)
			_, err = (&NativeRunner{}).RunToolRound(t.Context(), ModelLocal, request)
			requireNativeKind(t, err, KindContextCapacity)
		})
	}
}
