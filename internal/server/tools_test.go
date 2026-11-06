// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// toolFake scripts tool rounds. It deliberately implements no plain or
// streaming interface beyond Run, so a tool request that strays onto another
// path panics instead of passing.
type toolFake struct {
	calls    atomic.Int32
	replies  []string
	err      error
	prompts  []string
	modes    []string
	requests []runner.NativeToolStep
}

func (f *toolFake) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	panic("tool requests must use tool rounds")
}

func (f *toolFake) RunToolRound(_ context.Context, model runner.Model, request runner.ToolRoundRequest) (runner.Completion, error) {
	index := int(f.calls.Add(1)) - 1
	if request.Native == nil {
		panic("Local tools must use a native decision")
	}
	f.requests = append(f.requests, *request.Native)
	text := request.Native.Instructions
	for _, entry := range request.Native.History {
		if entry.Text != nil {
			text += "\n" + *entry.Text
		}
	}
	f.prompts = append(f.prompts, text)
	mode := "allowed"
	if request.Native.Choice.Type == "none" {
		mode = "disallowed"
	}
	f.modes = append(f.modes, mode)
	if f.err != nil {
		return runner.Completion{}, f.err
	}
	reply := f.replies[index]
	result := runner.Completion{Text: reply, Model: model, Usage: &runner.Usage{InputTokens: 30, OutputTokens: 6}}
	if request.Native.Choice.Type == "none" || !strings.HasPrefix(reply, `{"tool_calls":`) {
		return result, nil
	}
	var batch struct {
		Calls []struct {
			Function  string          `json:"function"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(reply), &batch) != nil {
		return runner.Completion{}, &runner.Error{Kind: runner.KindToolCallInvalid, ExitCode: -1, Err: errors.New("invalid native fixture arguments")}
	}
	result.Text = ""
	for i, call := range batch.Calls {
		arguments := string(call.Arguments)
		var encoded string
		if json.Unmarshal(call.Arguments, &encoded) == nil {
			arguments = encoded
		}
		result.NativeCalls = append(result.NativeCalls, runner.NativeToolCall{ID: fmt.Sprintf("call_%d_%d", request.Native.Step, i), Name: call.Function, Arguments: arguments})
	}
	return result, nil
}

const httpTools = `[{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city",` +
	`"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]`

const weatherReply = `{"tool_calls":[{"function":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}]}`

func TestToolsPreserveStartupProtocolDiagnosis(t *testing.T) {
	for _, endpoint := range []struct{ path, input, tools string }{
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"weather"}]`, httpTools},
		{"/v1/responses", `"input":"weather"`, responsesHTTPTools},
	} {
		for _, mismatch := range []bool{false, true} {
			probe := &runner.Error{Kind: runner.KindNativeProtocol, Err: errors.New("invalid native event")}
			message := "the native local helper returned an invalid or incomplete response"
			if mismatch {
				probe.NativeReason = "helper_mismatch"
				message = wantMismatchMessage
			}
			fake := &toolFake{replies: []string{weatherReply}}
			body := `{"model":"local","tools":` + endpoint.tools + `,` + endpoint.input + `}`
			res := post(t, localStatusServer(fake, probe).Handler(), endpoint.path, body)
			if res.Code != http.StatusBadGateway || fake.calls.Load() != 0 {
				t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
			}
			assertErrorBody(t, res, "server_error", "native_protocol", message)
		}
	}
}

func TestInvalidNativeArgumentsNeverRepairOverHTTP(t *testing.T) {
	for _, endpoint := range []struct{ path, input, tools string }{
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"weather"}]`, httpTools},
		{"/v1/responses", `"input":"weather"`, responsesHTTPTools},
	} {
		fake := &toolFake{replies: []string{
			`{"tool_calls":[{"function":"get_weather","arguments":{"city":1}}]}`, weatherReply,
		}}
		res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, `{"model":"local","tools":`+endpoint.tools+`,`+endpoint.input+`}`)
		if res.Code != http.StatusBadGateway || fake.calls.Load() != 1 || !strings.Contains(res.Body.String(), `"code":"tool_call_invalid"`) || strings.Contains(res.Body.String(), "tool_calls") {
			t.Fatalf("status=%d decisions=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
		}
	}
}

func decodeBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("body is not JSON: %v %s", err, body)
	}
	return value
}

func TestChatCompletionsToolCallContract(t *testing.T) {
	fake := &toolFake{replies: []string{weatherReply}}
	body := `{"model":"local","tools":` + httpTools + `,"messages":[{"role":"user","content":"Weather in Tokyo?"}]}`
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", body)
	if res.Code != http.StatusOK {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	got := decodeBody(t, res.Body.String())
	choice := got["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	if choice["finish_reason"] != "tool_calls" || message["content"] != nil {
		t.Fatalf("choice = %#v", choice)
	}
	if _, present := message["content"]; !present {
		t.Fatal("content must be present and null")
	}
	call := message["tool_calls"].([]any)[0].(map[string]any)
	function := call["function"].(map[string]any)
	if call["id"] != "call_1_0" || call["type"] != "function" || function["name"] != "get_weather" || function["arguments"] != `{"city":"Tokyo"}` {
		t.Fatalf("call = %#v", call)
	}
	if got["usage"].(map[string]any)["prompt_tokens"] != float64(30) || fake.modes[0] != "allowed" {
		t.Fatalf("usage=%v modes=%v", got["usage"], fake.modes)
	}
}

func TestChatCompletionsToolContinuation(t *testing.T) {
	fake := &toolFake{replies: []string{"It is sunny in Tokyo."}}
	body := `{"model":"local","tools":` + httpTools + `,"messages":[` +
		`{"role":"user","content":"Weather in Tokyo?"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1_0","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}]},` +
		`{"role":"tool","tool_call_id":"call_1_0","content":"{\"temp_c\":21}"}]}`
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", body)
	if res.Code != http.StatusOK {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	choice := decodeBody(t, res.Body.String())["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "stop" || choice["message"].(map[string]any)["content"] != "It is sunny in Tokyo." {
		t.Fatalf("choice = %#v", choice)
	}
	history := fake.requests[0].History
	if len(history) != 3 || history[1].Type != "calls" || len(history[1].Calls) != 1 || history[1].Calls[0].ID != "call_1_0" ||
		history[1].Calls[0].Name != "get_weather" || history[2].Type != "output" || history[2].ID != "call_1_0" ||
		history[2].Name != "get_weather" || history[2].Text == nil || !strings.Contains(*history[2].Text, `{"output":"{\"temp_c\":21}"}`) ||
		!strings.Contains(*history[2].Text, "untrusted data") {
		t.Fatalf("invalid native continuation: %+v", history)
	}
}

func TestResponsesFunctionCallContract(t *testing.T) {
	fake := &toolFake{replies: []string{weatherReply}}
	body := `{"model":"local","tools":` + responsesHTTPTools + `,"input":"Weather in Tokyo?"}`
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/responses", body)
	if res.Code != http.StatusOK {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	got := decodeBody(t, res.Body.String())
	output := got["output"].([]any)
	item := output[0].(map[string]any)
	if got["status"] != "completed" || len(output) != 1 || item["type"] != "function_call" || item["status"] != "completed" {
		t.Fatalf("response = %#v", got)
	}
	if !strings.HasPrefix(item["id"].(string), "fc_") || item["call_id"] != "call_1_0" || item["name"] != "get_weather" || item["arguments"] != `{"city":"Tokyo"}` {
		t.Fatalf("item = %#v", item)
	}
	if strings.Contains(res.Body.String(), "output_text") || strings.Contains(res.Body.String(), "tool_calls") {
		t.Fatal("Responses must not carry a message item or the chat shape")
	}
}

func TestResponsesFunctionCallOutputContinuation(t *testing.T) {
	fake := &toolFake{replies: []string{"Sunny."}}
	body := `{"model":"local","tools":` + responsesHTTPTools + `,"input":[` +
		`{"role":"user","content":"Weather in Tokyo?"},` +
		`{"type":"function_call","id":"fc_1","call_id":"call_1_0","name":"get_weather","arguments":"{\"city\":\"Tokyo\"}","status":"completed"},` +
		`{"type":"function_call_output","call_id":"call_1_0","output":"21C"}]}`
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/responses", body)
	if res.Code != http.StatusOK {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	item := decodeBody(t, res.Body.String())["output"].([]any)[0].(map[string]any)
	if item["type"] != "message" || item["content"].([]any)[0].(map[string]any)["text"] != "Sunny." {
		t.Fatalf("item = %#v", item)
	}
	if !strings.Contains(fake.prompts[0], `"output":"21C"`) {
		t.Fatal(fake.prompts[0])
	}
}

func TestToolRefusalsBeforeDispatch(t *testing.T) {
	user := `"messages":[{"role":"user","content":"hi"}]`
	tools := `"tools":` + httpTools
	cases := []struct {
		name, path, body, want string
		status                 int
	}{
		{"chat stream", "/v1/chat/completions", `{"model":"local","stream":true,` + tools + `,` + user + `}`, "tools cannot be combined with stream:true", 400},
		{"responses stream", "/v1/responses", `{"model":"local","stream":true,` + tools + `,"input":"hi"}`, "tools cannot be combined with stream:true", 400},
		{"chat image", "/v1/chat/completions", `{"model":"local",` + tools + `,"messages":[{"role":"user","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`, "tools cannot be combined with image input", 400},
		{"responses image", "/v1/responses", `{"model":"local",` + tools + `,"input":[{"role":"user","content":[{"type":"input_text","text":"x"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`, "tools cannot be combined with image input", 400},
		{"chat guided", "/v1/chat/completions", `{"model":"local",` + tools + `,` + user + `,"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object","properties":{}}}}}`, "tools cannot be combined with response_format json_schema", 400},
		{"responses guided", "/v1/responses", `{"model":"local",` + tools + `,"input":"hi","text":{"format":{"type":"json_schema","name":"x","schema":{"type":"object","properties":{}}}}}`, "tools cannot be combined with text.format json_schema", 400},
		{"parallel false", "/v1/chat/completions", `{"model":"local","parallel_tool_calls":false,` + tools + `,` + user + `}`, "parallel_tool_calls:false is not supported", 400},
		{"unknown named choice", "/v1/chat/completions", `{"model":"local","tool_choice":{"type":"function","function":{"name":"missing"}},` + tools + `,` + user + `}`, `tool_choice function \"missing\" was not offered; offered functions: get_weather`, 400},
		{"invalid choice", "/v1/responses", `{"model":"local","tool_choice":"invalid",` + tools + `,"input":"hi"}`, "tool_choice accepts auto, none, required", 400},
		{"choice without tools", "/v1/chat/completions", `{"model":"local","tool_choice":"auto",` + user + `}`, "tool_choice and parallel_tool_calls require tools", 400},
		{"empty tools", "/v1/responses", `{"model":"local","tools":[],"input":"hi"}`, "tools must contain at least one tool", 400},
		{"open schema", "/v1/chat/completions", `{"model":"local","tools":[{"type":"function","function":{"name":"x","description":"d","parameters":{"type":"object","properties":{},"additionalProperties":true}}}],` + user + `}`, "tools[0].function.parameters: schema at /additionalProperties", 400},
		{"shortcuts route", "/v1/chat/completions", `{"model":"cloud",` + tools + `,` + user + `}`, `tools is supported only for model \"local\"`, 400},
		{"auto route", "/v1/responses", `{` + tools + `,"input":"hi"}`, `tools is supported only for model \"local\"`, 400},
		{"chatgpt route before chat definition", "/v1/chat/completions", `{"model":"chatgpt","tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","properties":{}}}}],` + user + `}`, `tools is supported only for model \"local\"`, 400},
		{"chatgpt route before responses definition", "/v1/responses", `{"model":"chatgpt","tools":[{"type":"function","name":"x","parameters":{"type":"object","properties":{}}}],"input":"hi"}`, `tools is supported only for model \"local\"`, 400},
		{"unknown call id", "/v1/chat/completions", `{"model":"local",` + tools + `,"messages":[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"call_9","content":"x"}]}`, "references a call id that no earlier tool call carries", 400},
		{"final assistant text", "/v1/chat/completions", `{"model":"local",` + tools + `,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"done"}]}`, "the final message must have role", 400},
		{"content beside calls", "/v1/chat/completions", `{"model":"local",` + tools + `,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"x","tool_calls":[{"id":"c","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}]}`, "content beside tool_calls must be null or empty", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &toolFake{}
			body := tc.body
			if tc.path == "/v1/responses" {
				body = strings.ReplaceAll(body, httpTools, responsesHTTPTools)
			}
			res := post(t, NewUnauthenticated(fake).Handler(), tc.path, body)
			if res.Code != tc.status || !strings.Contains(res.Body.String(), tc.want) || fake.calls.Load() != 0 {
				t.Fatalf("%d calls=%d %s; want %d containing %q", res.Code, fake.calls.Load(), res.Body.String(), tc.status, tc.want)
			}
		})
	}
}

func TestToolMessagesWithoutToolsKeepTheExistingRefusals(t *testing.T) {
	fake := &nativeFake{}
	for _, body := range []string{
		`{"model":"local","messages":[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"c","content":"x"}]}`,
		`{"model":"local","messages":[{"role":"assistant","content":null,"tool_calls":[]},{"role":"user","content":"hi"}]}`,
	} {
		res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", body)
		if res.Code != http.StatusBadRequest || fake.calls.Load() != 0 {
			t.Fatalf("%d %s", res.Code, res.Body.String())
		}
	}
}

func TestToolLoopFaultsAreBadGateway(t *testing.T) {
	for _, tc := range []struct {
		replies []string
		code    string
	}{
		{[]string{`{"tool_calls":[{"function":"get_weather","arguments":"{}"}]}`, `{"tool_calls":[{"function":"get_weather","arguments":"{}"}]}`}, "tool_call_invalid"},
	} {
		fake := &toolFake{replies: tc.replies}
		srv := NewUnauthenticated(fake)
		res := post(t, srv.Handler(), "/v1/chat/completions", `{"model":"local","tools":`+httpTools+`,"messages":[{"role":"user","content":"w"}]}`)
		got := decodeBody(t, res.Body.String())["error"].(map[string]any)
		if res.Code != http.StatusBadGateway || got["code"] != tc.code || !strings.Contains(got["message"].(string), "nothing was dispatched") || fake.calls.Load() != 1 {
			t.Fatalf("%d %s", res.Code, res.Body.String())
		}
		if len(srv.slots()) != 0 {
			t.Fatal("leaked capacity")
		}
	}
	fake := &toolFake{err: &runner.Error{Kind: runner.KindUnsupportedCapability, Capability: "tools", ExitCode: -1, Err: errors.New("x")}}
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/responses", `{"model":"local","tools":`+responsesHTTPTools+`,"input":"w"}`)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "unsupported_capability") || fake.calls.Load() != 1 {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
}

func TestToolChoiceNoneOverHTTP(t *testing.T) {
	fake := &toolFake{replies: []string{weatherReply}}
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", `{"model":"local","tool_choice":"none","tools":`+httpTools+`,"messages":[{"role":"user","content":"w"}]}`)
	choice := decodeBody(t, res.Body.String())["choices"].([]any)[0].(map[string]any)
	if res.Code != http.StatusOK || choice["finish_reason"] != "stop" || choice["message"].(map[string]any)["content"] != weatherReply || fake.modes[0] != "disallowed" {
		t.Fatalf("%d %s modes=%v", res.Code, res.Body.String(), fake.modes)
	}
}
