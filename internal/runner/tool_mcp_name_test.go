// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"encoding/json"
	"testing"
)

const noteSchema = `{"type":"object","properties":{"note":{"type":"string"}},"required":["note"]}`

func mustNoteOffer(t *testing.T, name string) ToolOffer {
	t.Helper()
	offer, err := NewMCPToolOffer(name, "Store a note.", json.RawMessage(noteSchema), false)
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func prefixLessConversation(t *testing.T, offers ...string) ToolConversation {
	t.Helper()
	conversation := ToolConversation{Choice: ToolChoiceAuto, Messages: userTurn("Save a note.")}
	for _, name := range offers {
		conversation.Offers = append(conversation.Offers, mustNoteOffer(t, name))
	}
	return conversation
}

func TestPrefixLessMCPNameClassification(t *testing.T) {
	const declared = `call the declared tool name "notes__save"`
	for _, tc := range []struct {
		name, text string
		offers     []string
		fault      string
	}{
		{"valid JSON, unique match", `{"tool_calls":[{"function":{"name":"save","arguments":{"note":"x"}}}]}`, []string{"notes__save", "notes__list"}, declared},
		{"bare function shape", `{"tool_calls":[{"function":"save","arguments":{"note":"x"}}]}`, []string{"notes__save"}, declared},
		{"malformed JSON, unique match", `{"tool_calls":[{"function":{"name":"save","arguments":{"note":"x"}},"extra":""x"}]}`, []string{"notes__save"}, declared},
		{"malformed multiple calls", `{"tool_calls":[{"function":{"name":"save","arguments":?}},{"function":{"name":"other","arguments":?}}]}`, []string{"notes__save"}, declared},
		{"mixed with an offered call", `{"tool_calls":[{"function":{"name":"notes__list","arguments":{"note":"x"}}},{"function":{"name":"save","arguments":{"note":"y"}}}]}`, []string{"notes__save", "notes__list"}, declared},
		{"two servers match", `{"tool_calls":[{"function":{"name":"save","arguments":{"note":"x"}}}]}`, []string{"notes__save", "backup__save"}, ""},
		{"two servers match, malformed", `{"tool_calls":[{"function":{"name":"save","arguments":{"note":"x"}},"extra":""x"}]}`, []string{"notes__save", "backup__save"}, ""},
		{"no match", `{"tool_calls":[{"function":{"name":"store","arguments":{"note":"x"}}}]}`, []string{"notes__save"}, ""},
		{"no match, malformed", `{"tool_calls":[{"function":{"name":"store","arguments":{"note":"x"}},"extra":""x"}]}`, []string{"notes__save"}, ""},
		{"partial suffix is not a match", `{"tool_calls":[{"function":{"name":"ave","arguments":{"note":"x"}}}]}`, []string{"notes__save"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, _, err := prepareToolOffers(prefixLessConversation(t, tc.offers...))
			if err != nil {
				t.Fatal(err)
			}
			got := interpretOfferedRound(tc.text, tools, 1, map[string]bool{})
			if len(got.calls) != 0 {
				t.Fatalf("prefix-less reply surfaced calls: %+v", got.calls)
			}
			if tc.fault == "" {
				if !got.plain {
					t.Fatalf("unmatched or ambiguous name changed policy: %+v", got)
				}
				return
			}
			if got.plain || got.framing || got.fault != tc.fault {
				t.Fatalf("want repairable fault %q, got %+v", tc.fault, got)
			}
		})
	}
}

func TestPrefixLessMCPNameCallerToolsUnchanged(t *testing.T) {
	tool := mustTools(t)[0]
	tool.Name = "notes__save"
	got := interpretRound(`{"tool_calls":[{"function":{"name":"save","arguments":{"note":"x"}}}]}`, []Tool{tool}, 1, map[string]bool{})
	if !got.plain {
		t.Fatalf("caller tool names must not be matched by suffix: %+v", got)
	}
}
