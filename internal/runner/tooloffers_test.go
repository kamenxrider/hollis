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
	if err != nil || len(nativeDefinitions(request)) != 128 {
		t.Fatalf("raw count admission: %v", err)
	}
	c.Offers = append(c.Offers, mustMCPOffer(t, "server__overflow", true))
	if _, _, err := prepareToolOffers(c); err == nil {
		t.Fatal("129 offers admitted")
	}
	c.Offers = c.Offers[:1]
	c.Tools = mustTools(t)
	reply := rawNativeReply("a", "server__t0", `{}`)
	reply.NativeCalls = append(reply.NativeCalls, NativeToolCall{"b", "get_weather", `{}`})
	r := &nativeScript{replies: []Completion{reply}}
	executor := &recordingExecutor{output: "never"}
	_, err = RunToolLoop(t.Context(), r, ModelLocal, c, executor)
	requireNativeKind(t, err, KindToolCallInvalid)
	if executor.calls != 0 || len(r.requests) != 1 {
		t.Fatal("valid prefix dispatched or invalid batch repaired")
	}
}

func TestHybridPhysicalCapStillPreventsDispatch(t *testing.T) {
	r := &nativeScript{replies: []Completion{
		rawNativeReply("a", "server__raw", `{}`), rawNativeReply("b", "server__raw", `{}`), rawNativeReply("c", "server__raw", `{}`),
	}}
	c := ToolConversation{Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true)}, Choice: ToolChoiceAuto, Messages: userTurn("cap"), RoundLimit: 3}
	executor := &recordingExecutor{output: "framed"}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, c, executor)
	requireNativeKind(t, err, KindToolRoundCap)
	if out.Rounds != 3 || len(r.requests) != 3 || executor.calls != 2 || len(out.Messages) != 5 {
		t.Fatalf("out=%+v dispatched=%d", out, executor.calls)
	}
}

type toolExecutorFunc func(context.Context, []ToolCall) (ToolExecution, error)

func TestHybridIncrementalBudgetIncludesHistoryCallsAndMetadata(t *testing.T) {
	calls := []ToolCall{{"one", "server__raw", "{}"}, {"two", "server__raw", "{}"}, {"three", "server__raw", "{}"}}
	raw := mustMCPOffer(t, "server__raw", true)
	raw.description = strings.Repeat("metadata", 100)
	c := ToolConversation{Tools: mustTools(t), Offers: []ToolOffer{raw}, Choice: ToolChoiceAuto,
		Messages: []store.Message{{Role: "user", Content: strings.Repeat("history", 1000)}, {Role: "assistant", Content: "Prior answer"}, {Role: "user", Content: "Now use tools"}}}
	room := nativeAdmissionRoom(t, c, calls)
	for _, tc := range []struct {
		name             string
		size, dispatched int
		fail             bool
	}{
		{"one-byte-over", room + 1, 1, true}, {"exact-first-limit", room, 2, true}, {"valid", 10, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := Completion{}
			for _, call := range calls {
				reply.NativeCalls = append(reply.NativeCalls, rawNativeReply(call.ID, call.Name, call.Arguments).NativeCalls...)
			}
			r := &nativeScript{replies: []Completion{reply, {Text: "Done."}}}
			executor := &recordingExecutor{output: strings.Repeat("x", tc.size)}
			out, err := RunToolLoop(t.Context(), r, ModelLocal, c, executor)
			if tc.fail {
				requireNativeKind(t, err, KindContextCapacity)
			} else if err != nil {
				t.Fatal(err)
			}
			rounds := 2
			if tc.fail {
				rounds = 1
			}
			if executor.calls != tc.dispatched || len(r.requests) != rounds || len(out.Messages) != len(c.Messages)+1+tc.dispatched {
				t.Fatalf("dispatched=%d rounds=%d messages=%d", executor.calls, len(r.requests), len(out.Messages))
			}
			if !reflect.DeepEqual(out.Messages[:len(c.Messages)], c.Messages) {
				t.Fatal("input history changed")
			}
			id, output, ok := chat.DecodeToolResult(out.Messages[len(out.Messages)-1].Content)
			if !ok || id != calls[tc.dispatched-1].ID || output != executor.output {
				t.Fatal("completed result lost or truncated")
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
	reply := nativeReply("p1", "get_weather", `{"city":"Tokyo"}`)
	reply.NativeCalls = append(reply.NativeCalls, rawNativeReply("r1", "server__raw", `{"n":1e400,"s":"12"}`).NativeCalls...)
	reply.NativeCalls = append(reply.NativeCalls, NativeToolCall{"r2", "server__native", `{}`}, NativeToolCall{"p2", "get_weather", `{"city":"Osaka"}`})
	r := &nativeScript{replies: []Completion{reply, {Text: "must not continue"}}}
	c := ToolConversation{Tools: mustTools(t), Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true), mustMCPOffer(t, "server__native", false)}, Choice: ToolChoiceAuto, Messages: userTurn("run mixed")}
	executor := toolExecutorFunc(func(_ context.Context, calls []ToolCall) (ToolExecution, error) {
		if calls[1].Arguments != `{"n":1e400,"s":"12"}` {
			t.Fatalf("raw argument changed: %s", calls[1].Arguments)
		}
		return ToolExecution{Results: []ToolResult{{CallID: "r2", Output: "second"}, {CallID: "r1", Output: "first"}}, Pending: []ToolCall{calls[3], calls[0]}}, nil
	})
	out, err := RunToolLoop(t.Context(), r, ModelLocal, c, executor)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.requests) != 1 || out.Rounds != 1 || len(out.Calls) != 2 || out.Calls[0].ID != "p1" || out.Calls[1].ID != "p2" || len(out.Messages) != 4 {
		t.Fatalf("out=%+v", out)
	}
	calls, ok := chat.DecodeToolCalls(out.Messages[1].Content)
	if !ok || len(calls) != 4 {
		t.Fatal("full assistant batch missing")
	}
	for i, want := range []string{"r1", "r2"} {
		id, _, ok := chat.DecodeToolResult(out.Messages[i+2].Content)
		if !ok || id != want {
			t.Fatal("results not in call order")
		}
	}
	if len(r.requests[0].Tools) != len(c.Tools)+2 {
		t.Fatal("native offers missing")
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
	reply := rawNativeReply("one", "server__raw", `{}`)
	reply.NativeCalls = append(reply.NativeCalls, rawNativeReply("two", "server__raw", `{}`).NativeCalls...)
	r := &nativeScript{replies: []Completion{reply}}
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
	for _, args := range []string{`[]`, `null`, `"not JSON"`, `{"x":1,"x":2}`, encodeCompact(map[string]string{"large": strings.Repeat("x", MaxToolArgumentsBytes)})} {
		t.Run(fmt.Sprintf("args-%d", len(args)), func(t *testing.T) {
			r := &nativeScript{replies: []Completion{rawNativeReply("a", "server__raw", args)}}
			executor := &recordingExecutor{output: "never"}
			_, err := RunToolLoop(t.Context(), r, ModelLocal, c, executor)
			requireNativeKind(t, err, KindToolCallInvalid)
			if len(r.requests) != 1 || executor.calls != 0 {
				t.Fatal("invalid native envelope executed or repaired")
			}
		})
	}
	r := &nativeScript{replies: []Completion{rawNativeReply("a", "server__raw", `{"s":"12","huge":1e400}`), {Text: "done"}}}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, c, &recordingExecutor{output: "framed result"})
	if err != nil || out.Text != "done" || len(out.Messages) != 3 || !reflect.DeepEqual(r.requests[0].Tools, r.requests[1].Tools) {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if r.requests[0].Tools[0].Validation != "server" || !strings.Contains(string(r.requests[0].Tools[0].Parameters), "arguments_json") {
		t.Fatal("raw envelope tier lost")
	}
	r = &nativeScript{replies: []Completion{nativeReply("b", "unknown", `{}`)}}
	_, err = RunToolLoop(t.Context(), r, ModelLocal, c, nil)
	requireNativeKind(t, err, KindToolCallInvalid)
}

func hybridStatus(native, hybrid bool) string {
	return nativeLine("status", map[string]any{"available": true, "capabilities": map[string]any{"tools": map[string]any{
		"supported": true, "tool_calling_mode": true, "native_definitions": native, "hybrid_tools": hybrid,
	}}})
}

func TestHybridNativeWireAndCapabilityPreflight(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw-%v", raw), func(t *testing.T) {
			c := ToolConversation{Offers: []ToolOffer{mustMCPOffer(t, "server__t", raw)}, Choice: ToolChoiceAuto, Messages: userTurn("hello")}
			tools, offers, err := prepareToolOffers(c)
			if err != nil {
				t.Fatal(err)
			}
			instructions, history, err := nativeHistory(c.Messages, tools)
			if err != nil {
				t.Fatal(err)
			}
			step := NativeToolStep{Protocol: NativeProtocol, Operation: "tool_step", RequestID: "test-request", Step: 1, RemainingSteps: 8, Choice: nativeChoice(c.Choice), Instructions: instructions, Tools: nativeDefinitions(offers), History: history}
			request := ToolRoundRequest{Native: &step}
			marker := filepath.Join(t.TempDir(), "generation")
			r := fakeNative(t, "if [ \"$#\" -ne 0 ]; then touch '"+marker+"'; fi\ncat >/dev/null\n"+nativePrint(metadataStatus(false)))
			_, err = r.RunToolRound(t.Context(), ModelLocal, request)
			requireNativeKind(t, err, KindUnsupportedCapability)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("metadata-only helper received native request")
			}
			wirePath := filepath.Join(t.TempDir(), "request")
			r = fakeNative(t, "if [ \"$#\" -eq 0 ]; then\ncat >/dev/null\n"+nativePrint(metadataStatus(true))+"exit 0\nfi\n"+
				"test \"$1\" = '--native-tools'\ncat > '"+wirePath+"'\n"+nativePrint(nativeRequestStatus(true))+nativeRequestDone("done"))
			if _, err = r.RunToolRound(t.Context(), ModelLocal, request); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(wirePath)
			if err != nil {
				t.Fatal(err)
			}
			var wire NativeToolStep
			if err = json.Unmarshal(data, &wire); err != nil || !reflect.DeepEqual(wire, step) {
				t.Fatalf("native wire changed: %s %v", data, err)
			}
			if raw && (wire.Tools[0].Validation != "server" || !strings.Contains(string(wire.Tools[0].Parameters), "arguments_json")) {
				t.Fatal("server-validated tier not explicit")
			}
			text := strings.Repeat("x", NativeMaxPromptBytes)
			request.Native.History[0].Text = &text
			_, err = (&NativeRunner{}).RunToolRound(t.Context(), ModelLocal, request)
			requireNativeKind(t, err, KindContextCapacity)
		})
	}
}

func rawNativeReply(id, name, arguments string) Completion {
	return nativeReply(id, name, encodeCompact(map[string]string{"arguments_json": arguments}))
}

func nativeAdmissionRoom(t *testing.T, c ToolConversation, calls []ToolCall) int {
	t.Helper()
	tools, offers, err := prepareToolOffers(c)
	if err != nil {
		t.Fatal(err)
	}
	fits := func(size int) bool {
		messages := append([]store.Message(nil), c.Messages...)
		messages = append(messages, store.Message{Role: "assistant", Content: chat.EncodeToolCalls(toolCallRecords(calls))})
		for i, call := range calls {
			text := ""
			if i == 0 {
				text = strings.Repeat("x", size)
			}
			messages = append(messages, store.Message{Role: chat.RoleTool, Content: chat.EncodeToolResult(call.ID, text)})
		}
		instructions, history, err := nativeHistory(messages, tools)
		if err != nil {
			t.Fatal(err)
		}
		step := NativeToolStep{Protocol: NativeProtocol, Operation: "tool_step", RequestID: "test", Step: 2, RemainingSteps: 7, Choice: nativeChoice(c.Choice), Instructions: instructions, Tools: nativeDefinitions(offers), History: history}
		return step.validate() == nil
	}
	low, high := 0, NativeMaxPromptBytes
	for low < high {
		mid := (low + high + 1) / 2
		if fits(mid) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if !fits(low) || fits(low+1) {
		t.Fatal("could not locate exact byte boundary")
	}
	return low
}
