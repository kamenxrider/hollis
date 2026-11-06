package runner

import (
	"context"
	"errors"
	"testing"
)

func TestNativeBusyExecutionFailureNeverRetries(t *testing.T) {
	r := &nativeScript{replies: []Completion{
		nativeReply("one", "get_weather", `{"city":"Tokyo"}`), {Text: "must not run"},
	}}
	busy := nativeError(KindModelBusy, "busy during execution")
	dispatches := 0
	executor := toolExecutorFunc(func(_ context.Context, calls []ToolCall) (ToolExecution, error) {
		dispatches++
		return ToolExecution{Results: []ToolResult{{CallID: calls[0].ID, Output: "completed"}}}, busy
	})
	out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
	if !errors.Is(err, busy) || dispatches != 1 || len(r.requests) != 1 || len(out.Messages) != 3 {
		t.Fatalf("out=%+v err=%v dispatches=%d decisions=%d", out, err, dispatches, len(r.requests))
	}
}
