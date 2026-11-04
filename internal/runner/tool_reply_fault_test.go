// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// faultyReplyTools are the weather and add tools the fabricated faulty replies
// call.
const faultyReplyTools = `[
 {"type":"function","function":{"name":"get_weather","description":"Get the current weather for a city","strict":true,
  "parameters":{"type":"object","properties":{"city":{"type":"string","description":"City name"},"unit":{"anyOf":[{"type":"string","enum":["c","f"]},{"type":"null"}]}},"required":["city"]}}},
 {"type":"function","function":{"name":"add","description":"Add two integers","strict":true,
  "parameters":{"type":"object","properties":{"a":{"type":"integer","minimum":0,"maximum":20},"b":{"type":"integer","minimum":0,"maximum":20}},"required":["a","b"]}}}
]`

// Each file is a fabricated reply with a structural fault that once let a
// reply calling an offered tool reach the caller as plain text. The
// over-limit reply holds more call headers than one round allows, so it is
// refused as framing and never corrected.
var faultyToolReplies = []struct {
	file, fault string
	framing     bool
}{
	{"tool_reply_extra_element.txt", "the reply must contain only calls to the declared tools", false},
	{"tool_reply_unoffered_with_prose.txt", "the tool-call reply is not valid JSON", false},
	{"tool_reply_over_limit_broken.txt", "the reply contained more tool calls than the 16 allowed in one round", true},
	{"tool_reply_numeric_id_wrong_closer.txt", "the tool-call reply is not valid JSON", false},
}

func faultyReply(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestFaultyToolRepliesAreRepairableFaults(t *testing.T) {
	tools, err := ParseTools([]byte(faultyReplyTools), "tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range faultyToolReplies {
		t.Run(tc.file, func(t *testing.T) {
			got := interpretRound(faultyReply(t, tc.file), tools, 1, map[string]bool{})
			if got.plain || got.framing != tc.framing || len(got.calls) != 0 || got.fault != tc.fault {
				t.Fatalf("outcome = %+v; want fault %q framing=%v", got, tc.fault, tc.framing)
			}
		})
	}
}

func TestFaultyToolRepliesNeverReachTheCallerAsText(t *testing.T) {
	tools, err := ParseTools([]byte(faultyReplyTools), "tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range faultyToolReplies {
		t.Run(tc.file, func(t *testing.T) {
			reply := faultyReply(t, tc.file)
			r := &scriptedRounds{replies: []string{reply, reply}}
			out, err := RunToolLoop(t.Context(), r, ModelLocal, ToolConversation{
				Tools: tools, Choice: ToolChoiceAuto, Messages: userTurn("Call the tool."),
			}, ReturnToCaller{})
			var fault *Error
			if !errors.As(err, &fault) || fault.Kind != KindToolCallInvalid {
				t.Fatalf("error=%v; want tool_call_invalid", err)
			}
			want := 2
			if tc.framing {
				want = 1
			}
			if len(r.prompts) != want || out.Rounds != want || out.Text != "" || len(out.Calls) != 0 {
				t.Fatalf("rounds=%d prompts=%d out=%+v; want %d rounds and no answer", out.Rounds, len(r.prompts), out, want)
			}
		})
	}
}

func TestFaultyToolReplyCorrectedToValidCall(t *testing.T) {
	tools, err := ParseTools([]byte(faultyReplyTools), "tools")
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"tool_calls":[{"id":"call_7","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Lisbon\",\"unit\":\"f\"}"}}]}`
	r := &scriptedRounds{replies: []string{faultyReply(t, "tool_reply_extra_element.txt"), valid}}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, ToolConversation{
		Tools: tools, Choice: ToolChoiceAuto, Messages: userTurn("Call get_weather for Lisbon with unit f."),
	}, ReturnToCaller{})
	if err != nil || out.Rounds != 2 || len(out.Calls) != 1 || out.Calls[0].Name != "get_weather" ||
		out.Calls[0].Arguments != `{"city":"Lisbon","unit":"f"}` {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestNoOfferedToolCallStaysPlain(t *testing.T) {
	tools := mustTools(t)
	for _, text := range []string{
		`{"tool_calls":[{"function":{"name":"nope","arguments":"{}"}},{"context":"x"}]}`,
		`{"tool_calls":[{"function":{"name":"nope","arguments":?}},{"function":{"name":"other","arguments":?}}]}`,
	} {
		if got := interpretRound(text, tools, 1, map[string]bool{}); !got.plain || got.fault != "" {
			t.Fatalf("reply calling no offered tool changed policy: %+v", got)
		}
	}
}
