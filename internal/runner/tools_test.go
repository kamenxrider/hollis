// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"errors"
	"github.com/kamenxrider/hollis/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three tools from the measured spike, written to the widened subset.
const spikeTools = `[
 {"type":"function","function":{"name":"get_weather","description":"Get the current weather for a city",
  "parameters":{"type":"object","properties":{"city":{"type":"string","description":"City name"}},"required":["city"]}}},
 {"type":"function","function":{"name":"add","description":"Add two integers",
  "parameters":{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}}},
 {"type":"function","function":{"name":"lookup_user","description":"Look up a user profile by numeric id",
  "parameters":{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"]}}}
]`

func mustTools(t *testing.T) []Tool {
	t.Helper()
	tools, err := ParseTools([]byte(spikeTools), "tools")
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

// scriptedRounds is a fake ToolRoundRunner: each round returns the next
// scripted reply and records the prompt and mode it was given.
type scriptedRounds struct {
	replies  []string
	errs     []error
	prompts  []string
	modes    []string
	requests []ToolRoundRequest
}

func (s *scriptedRounds) RunToolRound(_ context.Context, model Model, request ToolRoundRequest) (Completion, error) {
	index := len(s.prompts)
	s.prompts = append(s.prompts, request.Prompt)
	s.modes = append(s.modes, request.Mode)
	s.requests = append(s.requests, request)
	if index < len(s.errs) && s.errs[index] != nil {
		return Completion{}, s.errs[index]
	}
	if index >= len(s.replies) {
		return Completion{}, errors.New("unscripted round")
	}
	return Completion{Text: s.replies[index], Model: model, Usage: &Usage{InputTokens: 10, OutputTokens: 2}}, nil
}

func userTurn(text string) []store.Message { return []store.Message{{Role: "user", Content: text}} }

const fence = "```"

func repeatCalls(n int) string {
	return `{"tool_calls":[` + strings.TrimSuffix(strings.Repeat(`{"function":{"name":"add","arguments":{"a":1,"b":1}}},`, n), ",") + `]}`
}

// TestToolNormalizationTable has one row per malformation observed live, plus
// the validation outcomes the repair round depends on.
func TestToolNormalizationTable(t *testing.T) {
	tools := mustTools(t)
	cases := []struct {
		name      string
		text      string
		plain     bool
		fault     string
		framing   bool
		wantName  string
		wantArgs  string
		wantID    string
		wantCalls int
	}{
		{name: "canonical whole text", text: `{"tool_calls":[{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}]}`,
			wantName: "get_weather", wantArgs: `{"city":"Tokyo"}`, wantID: "call_a", wantCalls: 1},
		{name: "wrapped in prose", text: "Sure, let me check.\n" + `{"tool_calls":[{"function":{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}}]}` + " Done.",
			wantName: "get_weather", wantArgs: `{"city":"Oslo"}`, wantID: "call_2_0", wantCalls: 1},
		{name: "fenced block", text: "Here:\n" + fence + "json\n" + `{"tool_calls":[{"function":{"name":"add","arguments":{"a":2,"b":3}}}]}` + "\n" + fence + "\n",
			wantName: "add", wantArgs: `{"a":2,"b":3}`, wantCalls: 1},
		{name: "function as a plain string with top-level arguments", text: `{"tool_calls":[{"id":"call_1","type":"function","function":"lookup_user","arguments":"{\"id\":7}"}]}`,
			wantName: "lookup_user", wantArgs: `{"id":7}`, wantID: "call_1", wantCalls: 1},
		{name: "arguments as an object", text: `{"tool_calls":[{"function":{"name":"add","arguments":{"a":1,"b":1}}}]}`,
			wantName: "add", wantArgs: `{"a":1,"b":1}`, wantCalls: 1},
		{name: "numeric strings coerced", text: `{"tool_calls":[{"function":{"name":"add","arguments":"{\"a\":\"2\",\"b\":\"40\"}"}}]}`,
			wantName: "add", wantArgs: `{"a":2,"b":40}`, wantCalls: 1},
		{name: "non-numeric string not coerced", text: `{"tool_calls":[{"function":{"name":"add","arguments":"{\"a\":\"two\",\"b\":1}"}}]}`,
			fault: `call 1 to tool "add": the argument at /a has the wrong type`},
		{name: "missing closing brackets repaired once", text: `{"tool_calls":[{"function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}}` + "\n",
			wantName: "get_weather", wantArgs: `{"city":"Rome"}`, wantCalls: 1},
		{name: "invented tool name is plain text", text: `{"tool_calls":[{"function":{"name":"write_haiku","arguments":"{}"}}]} Autumn wind...`, plain: true},
		{name: "an invented name beside an offered call is corrected", text: `{"tool_calls":[{"function":{"name":"add","arguments":"{\"a\":1,\"b\":2}"}},{"function":{"name":"nope","arguments":"{}"}}]}`,
			fault: "the reply must contain only calls to the declared tools"},
		{name: "no candidate is plain", text: "Paris is the capital of France.", plain: true},
		{name: "unknown property refused", text: `{"tool_calls":[{"function":"lookup_user","arguments":"{\"user_id\":7}"}]}`,
			fault: `call 1 to tool "lookup_user": the argument at / contains a property that is not declared`},
		{name: "missing required", text: `{"tool_calls":[{"function":{"name":"get_weather","arguments":"{}"}}]}`,
			fault: `call 1 to tool "get_weather": the argument at /city is required and missing`},
		{name: "bound violated", text: `{"tool_calls":[{"function":{"name":"lookup_user","arguments":{"id":0}}}]}`,
			fault: `call 1 to tool "lookup_user": the argument at /id is outside the allowed range`},
		{name: "arguments absent", text: `{"tool_calls":[{"function":{"name":"add"}}]}`,
			fault: `call 1 to tool "add" has no arguments`},
		{name: "arguments not an object", text: `{"tool_calls":[{"function":{"name":"add","arguments":"[1,2]"}}]}`,
			fault: `call 1 to tool "add" has arguments that are not a JSON object`},
		{name: "empty call list", text: `{"tool_calls":[]}`, fault: "tool_calls must contain at least one call"},
		{name: "seventeen calls is framing", text: repeatCalls(17),
			framing: true, fault: "the reply contained 17 tool calls; at most 16 are allowed in one round"},
		{name: "sixteen calls accepted", text: repeatCalls(16), wantName: "add", wantCalls: 16},
		{name: "quoted braces in prose do not confuse the scan", text: `Use "{" carefully: {"tool_calls":[{"function":{"name":"get_weather","arguments":"{\"city\":\"a}b\"}"}}]}`,
			wantName: "get_weather", wantArgs: `{"city":"a}b"}`, wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := interpretRound(tc.text, tools, 2, map[string]bool{})
			if got.plain != tc.plain || got.fault != tc.fault || got.framing != tc.framing {
				t.Fatalf("outcome = %+v; want plain=%v fault=%q framing=%v", got, tc.plain, tc.fault, tc.framing)
			}
			if tc.wantCalls == 0 {
				return
			}
			if len(got.calls) != tc.wantCalls {
				t.Fatalf("calls = %d; want %d", len(got.calls), tc.wantCalls)
			}
			call := got.calls[0]
			if call.Name != tc.wantName || (tc.wantArgs != "" && call.Arguments != tc.wantArgs) {
				t.Fatalf("call = %+v; want %s %s", call, tc.wantName, tc.wantArgs)
			}
			if tc.wantID != "" && call.ID != tc.wantID {
				t.Fatalf("id = %q; want %q", call.ID, tc.wantID)
			}
			ids := map[string]bool{}
			for _, call := range got.calls {
				if ids[call.ID] {
					t.Fatalf("duplicate id %q", call.ID)
				}
				ids[call.ID] = true
			}
		})
	}
}

func TestToolCallIDsAreUniqueAcrossTheConversation(t *testing.T) {
	tools := mustTools(t)
	used := map[string]bool{"call_2_0": true, "call_x": true}
	got := interpretRound(`{"tool_calls":[{"id":"call_x","function":{"name":"add","arguments":{"a":1,"b":1}}},{"function":{"name":"add","arguments":{"a":1,"b":1}}}]}`, tools, 2, used)
	if len(got.calls) != 2 || got.calls[0].ID != "call_2_0_1" || got.calls[1].ID != "call_2_1" {
		t.Fatalf("calls = %+v", got.calls)
	}
}

func TestParseToolsRefusals(t *testing.T) {
	ok := `{"type":"function","function":{"name":"a","description":"d","parameters":{"type":"object","properties":{}}}}`
	cases := map[string]struct{ body, want string }{
		"not an array":      {`{}`, "tools must be a JSON array of tools"},
		"empty":             {`[]`, "tools must contain at least one tool"},
		"too many":          {"[" + strings.TrimSuffix(strings.Repeat(ok+",", 33), ",") + "]", "tools has 33 tools; the maximum is 32"},
		"wrong type":        {`[{"type":"x","function":{}}]`, `tools[0].type must be "function"`},
		"bad name":          {`[{"type":"function","function":{"name":"a b","description":"d"}}]`, "tools[0].function.name must match ^[a-zA-Z0-9_-]{1,64}$"},
		"long name":         {`[{"type":"function","function":{"name":"` + strings.Repeat("a", 65) + `","description":"d"}}]`, "tools[0].function.name must match ^[a-zA-Z0-9_-]{1,64}$"},
		"duplicate":         {"[" + ok + "," + ok + "]", "tools[1].function.name repeats an earlier tool name"},
		"no description":    {`[{"type":"function","function":{"name":"a"}}]`, "tools[0].function.description must be a non-empty string"},
		"blank description": {`[{"type":"function","function":{"name":"a","description":"  "}}]`, "tools[0].function.description must be a non-empty string"},
		"unknown member":    {`[{"type":"function","function":{"name":"a","description":"d","x":1}}]`, "tools[0].function contains an unsupported member"},
		"open object":       {`[{"type":"function","function":{"name":"a","description":"d","parameters":{"type":"object","properties":{},"additionalProperties":true}}}]`, "tools[0].function.parameters: schema at /additionalProperties: additionalProperties:false is required"},
		"multi-type union":  {`[{"type":"function","function":{"name":"a","description":"d","parameters":{"type":"object","properties":{"v":{"anyOf":[{"type":"string"},{"type":"integer"}]}}}}}]`, "tools[0].function.parameters: schema at /properties/v/anyOf: only nullable unions are supported"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTools([]byte(tc.body), "tools")
			var failure *ToolDefinitionError
			if !errors.As(err, &failure) || err.Error() != tc.want {
				t.Fatalf("err = %v; want %q", err, tc.want)
			}
		})
	}
	tools, err := ParseTools([]byte(`[{"type":"function","function":{"name":"now","description":"Current time","strict":true}}]`), "tools")
	if err != nil || len(tools) != 1 || string(tools[0].Parameters.Bytes()) != `{"properties":{},"type":"object"}` {
		t.Fatalf("omitted parameters: %v %v", tools, err)
	}
}

// The instructions block is pinned byte for byte: changing it changes what
// the model is asked to do and must be a deliberate, reviewed edit.
func TestToolInstructionsGolden(t *testing.T) {
	got := toolInstructions
	path := filepath.Join("testdata", "tool_instructions.golden")
	if os.Getenv("HOLLIS_UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("instructions block drifted:\n got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(got, "string length, pattern and format are not enforced") {
		t.Fatal("instructions must state that string constraints are not enforced")
	}
}

type recordingExecutor struct {
	calls  int
	output string
}

func (e *recordingExecutor) ExecuteTools(_ context.Context, calls []ToolCall, admit func(ToolResult) error) (ToolExecution, error) {
	if e.output == "" {
		e.calls += len(calls)
		return ToolExecution{Pending: calls}, nil
	}
	var results []ToolResult
	for _, call := range calls {
		e.calls++
		result := ToolResult{CallID: call.ID, Output: e.output}
		results = append(results, result)
		if err := admit(result); err != nil {
			return ToolExecution{Results: results}, err
		}
	}
	return ToolExecution{Results: results}, nil
}
