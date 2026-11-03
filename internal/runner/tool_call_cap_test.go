// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/store"
)

func priorCallTurns(count int) []store.Message {
	messages := userTurn("weather")
	for i := 0; i < count; i++ {
		id := strings.Repeat("c", i+1)
		messages = append(messages,
			store.Message{Role: "assistant", Content: chat.EncodeToolCalls([]chat.ToolCallRecord{{ID: id, Name: "get_weather", Arguments: `{"city":"Tokyo"}`}})},
			store.Message{Role: chat.RoleTool, Content: chat.EncodeToolResult(id, "18C")})
	}
	return messages
}

func TestToolPhysicalCallCapIncludesCorrections(t *testing.T) {
	good := `{"tool_calls":[{"function":"get_weather","arguments":{"city":"Tokyo"}}]}`
	r := &scriptedRounds{replies: []string{malformedWeatherCall, good, malformedWeatherCall, good}}
	executor := &recordingExecutor{output: "18C"}
	out, err := RunToolLoop(t.Context(), r, ModelLocal,
		ToolConversation{Tools: mustTools(t), Choice: ToolChoiceAuto, Messages: userTurn("weather")}, executor)
	requireNativeKind(t, err, KindToolRoundCap)
	if len(r.prompts) != 3 || out.Rounds != 3 || executor.calls != 1 {
		t.Fatalf("physical calls=%d rounds=%d executed=%d", len(r.prompts), out.Rounds, executor.calls)
	}
}

func TestToolPhysicalCallCapContinuationAndNone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prior  int
		choice ToolChoice
		reply  string
		calls  int
		fail   bool
	}{
		{"last call may answer", 2, ToolChoiceAuto, "Done.", 1, false},
		{"last call may not ask for another", 2, ToolChoiceAuto, `{"tool_calls":[{"function":"get_weather","arguments":{"city":"Tokyo"}}]}`, 1, true},
		{"last call has no repair budget", 2, ToolChoiceAuto, malformedWeatherCall, 1, true},
		{"none consumes last call", 2, ToolChoiceNone, "Done.", 1, false},
		{"none cannot bypass exhausted history", 3, ToolChoiceNone, "Done.", 0, true},
		{"auto exhausted history", 3, ToolChoiceAuto, "Done.", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &scriptedRounds{replies: []string{tc.reply, "Done."}}
			out, err := RunToolLoop(t.Context(), r, ModelLocal,
				ToolConversation{Tools: mustTools(t), Choice: tc.choice, Messages: priorCallTurns(tc.prior)}, nil)
			if tc.fail {
				requireNativeKind(t, err, KindToolRoundCap)
			} else if err != nil {
				t.Fatal(err)
			}
			if len(r.prompts) != tc.calls || out.Rounds != tc.calls {
				t.Fatalf("calls=%d reported=%d", len(r.prompts), out.Rounds)
			}
		})
	}
}

func TestToolPhysicalCallCapAdmissionFailuresDoNotDispatch(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		messages := userTurn(strings.Repeat("x", NativeMaxPromptBytes))
		kind := KindContextCapacity
		if canceled {
			cancel()
			messages = userTurn("weather")
			kind = KindContextCanceled
		}
		r := &scriptedRounds{replies: []string{"Done."}}
		out, err := RunToolLoop(ctx, r, ModelLocal,
			ToolConversation{Tools: mustTools(t), Choice: ToolChoiceAuto, Messages: messages}, nil)
		cancel()
		requireNativeKind(t, err, kind)
		if len(r.prompts) != 0 || out.Rounds != 0 {
			t.Fatalf("calls=%d rounds=%d", len(r.prompts), out.Rounds)
		}
	}
}
