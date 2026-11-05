// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/store"
	"strings"
	"testing"
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
	c := nativeConversation(t, ToolChoiceAuto)
	c.RoundLimit = 3
	r := &nativeScript{replies: []Completion{
		nativeReply("a", "get_weather", `{"city":"Tokyo"}`),
		nativeReply("b", "get_weather", `{"city":"Tokyo"}`),
		nativeReply("c", "get_weather", `{"city":"Tokyo"}`),
	}}
	executor := &recordingExecutor{output: "18C"}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, c, executor)
	requireNativeKind(t, err, KindToolRoundCap)
	if len(r.requests) != 3 || out.Rounds != 3 || executor.calls != 2 {
		t.Fatalf("rounds=%d dispatched=%d", out.Rounds, executor.calls)
	}
}

func TestToolPhysicalCallCapContinuationAndNone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prior  int
		choice ToolChoice
		reply  Completion
		calls  int
		fail   bool
	}{
		{"last may answer", 2, ToolChoiceAuto, Completion{Text: "Done."}, 1, false},
		{"last may not dispatch", 2, ToolChoiceAuto, nativeReply("last", "get_weather", `{"city":"Tokyo"}`), 1, true},
		{"none consumes last", 2, ToolChoiceNone, Completion{Text: "Done."}, 1, false},
		{"none cannot bypass exhausted history", 3, ToolChoiceNone, Completion{Text: "Done."}, 0, true},
		{"auto exhausted history", 3, ToolChoiceAuto, Completion{Text: "Done."}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &nativeScript{replies: []Completion{tc.reply}}
			c := nativeConversation(t, tc.choice)
			c.RoundLimit = 3
			c.Messages = priorCallTurns(tc.prior)
			out, err := RunToolLoop(t.Context(), r, ModelLocal, c, nil)
			if tc.fail {
				requireNativeKind(t, err, KindToolRoundCap)
			} else if err != nil {
				t.Fatal(err)
			}
			if len(r.requests) != tc.calls || out.Rounds != tc.calls || len(out.Calls) != 0 {
				t.Fatalf("out=%+v decisions=%d", out, len(r.requests))
			}
		})
	}
}

func TestToolPhysicalCallCapAdmissionFailuresDoNotDispatch(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		c := nativeConversation(t, ToolChoiceAuto)
		c.Messages = userTurn(strings.Repeat("x", NativeMaxPromptBytes))
		kind := KindContextCapacity
		if canceled {
			cancel()
			c.Messages = userTurn("weather")
			kind = KindContextCanceled
		}
		r := &nativeScript{}
		out, err := RunToolLoop(ctx, r, ModelLocal, c, nil)
		cancel()
		requireNativeKind(t, err, kind)
		if len(r.requests) != 0 || out.Rounds != 0 {
			t.Fatal("admission failure dispatched")
		}
	}
}
