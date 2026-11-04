// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestPrefixLessMCPNameUsesOneCorrection(t *testing.T) {
	const bare = `{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"save","arguments":{"note":"x"}}}]}`
	const fixed = `{"tool_calls":[{"function":{"name":"notes__save","arguments":{"note":"x"}}}]}`
	for _, tc := range []struct {
		name, second string
		wantError    bool
		wantCalls    int
		wantText     string
	}{
		{"corrected call executes", fixed, false, 1, ""},
		{"still prefix-less", bare, true, 0, ""},
		{"plain answer after correction", "I cannot save notes.", false, 0, "I cannot save notes."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var executed []ToolCall
			executor := toolExecutorFunc(func(_ context.Context, calls []ToolCall) (ToolExecution, error) {
				executed = append(executed, calls...)
				return ToolExecution{Pending: calls}, nil
			})
			r := &scriptedRounds{replies: []string{bare, tc.second}}
			out, err := RunToolLoop(context.Background(), r, ModelLocal, prefixLessConversation(t, "notes__save", "notes__list"), executor)
			if len(r.prompts) != 2 || out.Rounds != 2 {
				t.Fatalf("rounds=%d, prompts=%d; want exactly one correction", out.Rounds, len(r.prompts))
			}
			if !strings.Contains(r.prompts[1], `call the declared tool name "notes__save"`) {
				t.Fatalf("correction does not name the declared tool:\n%s", r.prompts[1])
			}
			var fault *Error
			if tc.wantError {
				if !errors.As(err, &fault) || fault.Kind != KindToolCallInvalid || !strings.Contains(fault.Error(), "notes__save") {
					t.Fatalf("error=%v; want tool_call_invalid naming the declared tool", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			for _, call := range executed {
				if call.Name != "notes__save" {
					t.Fatalf("executed a call that was not the declared name: %+v", call)
				}
			}
			if len(executed) != tc.wantCalls || len(out.Calls) != tc.wantCalls || out.Text != tc.wantText {
				t.Fatalf("executed=%+v outcome=%+v", executed, out)
			}
		})
	}
}
