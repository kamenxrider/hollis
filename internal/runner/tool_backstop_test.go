// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"testing"
)

const notValidJSON = "the tool-call reply is not valid JSON"

func backstopTools(t *testing.T) []loopTool {
	t.Helper()
	conversation := prefixLessConversation(t, "notes__save", "notes__list")
	conversation.Tools = mustTools(t)
	tools, _, err := prepareToolOffers(conversation)
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func TestToolCallsBackstopClassification(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		repair     bool
		fault      string
	}{
		{"garbled second header, caller name", `{"tool_calls":[{"id":"a1","type":"function","function":"save","arguments":{"memo":"buy milk"}},{"id":"a2","type":"function":"get_weather","arguments":{"town":"Oslo"}}]}`, true, notValidJSON},
		{"garbled second header, bare MCP name", `{"tool_calls":[{"id":"a1","type":"function","function":"list","arguments":{}},{"id":"a2","type":"function":"lookup","arguments":{"q":1}}]}`, true, notValidJSON},
		{"heavily garbled", `}} {"tool_calls" :: [[ "get_weather" ,,, {town= Oslo`, true, notValidJSON},
		{"heavily garbled, declared MCP name", `<<"tool_calls">> ("notes__save" memo`, true, notValidJSON},
		{"marker without an offered name", `{"tool_calls":[{"id":"a1","type":"function":"invented","arguments":{"town":"Oslo"}}]}`, false, notValidJSON},
		{"valid marker without an offered name", `{"tool_calls":[{"function":{"name":"invented","arguments":{"town":"Oslo"}}}]}`, false, notValidJSON},
		{"prose naming a tool without a marker", `You could call "get_weather" or "notes__save" with {"town":"Oslo"}.`, false, notValidJSON},
		{"unquoted name with a marker", `The "tool_calls" field would hold get_weather here.`, false, notValidJSON},
		{"specific fault kept", `{"tool_calls":[{"function":"save","arguments":{}},{"function":"get_weather"}`, true, `call the declared tool name "notes__save"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := interpretOfferedRound(tc.text, backstopTools(t), 1, map[string]bool{})
			if len(got.calls) != 0 {
				t.Fatalf("broken reply surfaced calls: %+v", got.calls)
			}
			if !tc.repair {
				if !got.plain {
					t.Fatalf("reply without an offered name changed policy: %+v", got)
				}
				return
			}
			if got.plain || got.framing || got.fault != tc.fault {
				t.Fatalf("want repairable %q, got %+v", tc.fault, got)
			}
		})
	}
}

func TestToolCallsBackstopBareNameNeedsOneMatch(t *testing.T) {
	conversation := prefixLessConversation(t, "notes__save", "backup__save")
	tools, _, err := prepareToolOffers(conversation)
	if err != nil {
		t.Fatal(err)
	}
	got := interpretOfferedRound(`{"tool_calls":[{"id":"a1","type":"function":"save","arguments":{}}]}`, tools, 1, map[string]bool{})
	if !got.plain {
		t.Fatalf("ambiguous bare name changed policy: %+v", got)
	}
}
