package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/store"
)

type nativeScript struct {
	replies  []Completion
	errs     []error
	contexts []context.Context
	onRound  func(context.Context)
	requests []NativeToolStep
	checks   []NativeToolStep
	checkErr error
}

func (s *nativeScript) RunToolRound(ctx context.Context, _ Model, r ToolRoundRequest) (Completion, error) {
	if r.Native == nil {
		return Completion{}, errors.New("missing native history")
	}
	s.requests = append(s.requests, *r.Native)
	s.contexts = append(s.contexts, ctx)
	if s.onRound != nil {
		s.onRound(ctx)
	}
	index := len(s.requests) - 1
	if index < len(s.errs) && s.errs[index] != nil {
		return Completion{}, s.errs[index]
	}
	if len(s.replies) == 0 {
		return Completion{}, errors.New("unexpected model decision")
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return reply, nil
}

func TestNativeLoopCancellationAndDenial(t *testing.T) {
	t.Run("cancellation after inference prevents approval", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r := &nativeScript{replies: []Completion{nativeReply("a", "get_weather", `{"city":"Tokyo"}`)}, onRound: func(context.Context) { cancel() }}
		executor := &recordingExecutor{output: "never"}
		_, err := RunToolLoop(ctx, r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, KindContextCanceled)
		if executor.calls != 0 || len(r.requests) != 1 {
			t.Fatal("canceled decision reached executor")
		}
	})
	t.Run("approval denial is terminal and preserves typed error", func(t *testing.T) {
		r := &nativeScript{replies: []Completion{nativeReply("a", "get_weather", `{"city":"Tokyo"}`), {Text: "must not retry"}}}
		denial := nativeError(Kind("tool_denied"), "denied")
		approvals := 0
		executor := toolExecutorFunc(func(_ context.Context, calls []ToolCall) (ToolExecution, error) {
			approvals++
			if len(calls) != 1 || calls[0].Arguments != `{"city":"Tokyo"}` {
				t.Fatal("approval did not receive validated bytes")
			}
			return ToolExecution{}, denial
		})
		out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		if !errors.Is(err, denial) || approvals != 1 || out.Rounds != 1 || len(r.requests) != 1 || len(out.Messages) != 2 {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	})
	t.Run("cancellation during execution preserves completed result", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		reply := nativeReply("a", "get_weather", `{"city":"Tokyo"}`)
		reply.NativeCalls = append(reply.NativeCalls, NativeToolCall{"b", "get_weather", `{"city":"Berlin"}`})
		r := &nativeScript{replies: []Completion{reply}}
		dispatched := 0
		executor := toolExecutorFunc(func(_ context.Context, calls []ToolCall) (ToolExecution, error) {
			dispatched++
			cancel()
			return ToolExecution{Results: []ToolResult{{CallID: calls[0].ID, Output: "completed"}}}, nativeError(KindContextCanceled, "canceled")
		})
		out, err := RunToolLoop(ctx, r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, KindContextCanceled)
		if dispatched != 1 || len(out.Messages) != 3 || len(r.requests) != 1 {
			t.Fatal("cancellation lost partial result or retried")
		}
	})
}

func TestNativeLoopBatchIdentityAndCount(t *testing.T) {
	for _, batch := range [][]NativeToolCall{
		{{"a", "get_weather", `{"city":"Tokyo"}`}, {"a", "get_weather", `{"city":"Berlin"}`}},
		{{"a", "save", `{}`}},
	} {
		r := &nativeScript{replies: []Completion{{NativeCalls: batch}}}
		executor := &recordingExecutor{output: "never"}
		_, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, KindToolCallInvalid)
		if executor.calls != 0 {
			t.Fatal("invalid batch approved")
		}
	}
	batch := []NativeToolCall{}
	for i := 0; i < 17; i++ {
		batch = append(batch, NativeToolCall{string(rune('a' + i)), "get_weather", `{"city":"Tokyo"}`})
	}
	r := &nativeScript{replies: []Completion{{NativeCalls: batch}}}
	executor := &recordingExecutor{output: "never"}
	_, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
	requireNativeKind(t, err, KindToolCallInvalid)
	if executor.calls != 0 || len(r.requests) != 1 {
		t.Fatal("over-limit batch executed or repaired")
	}
}

func (s *nativeScript) CheckToolHistory(_ context.Context, step NativeToolStep) error {
	s.checks = append(s.checks, step)
	return s.checkErr
}

func nativeReply(id, name, arguments string) Completion {
	return Completion{NativeCalls: []NativeToolCall{{ID: id, Name: name, Arguments: arguments}}}
}

func nativeConversation(t *testing.T, choice ToolChoice) ToolConversation {
	return ToolConversation{Tools: mustTools(t), Choice: choice, Messages: userTurn("Weather in Tokyo?")}
}

func TestNativeLoopTextNeverExecutes(t *testing.T) {
	for _, text := range []string{
		`{"tool_calls":[{"id":"x","function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}]}`,
		"CALL: get_weather {\"city\":\"Tokyo\"}",
		"Example:\n```json\n{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Tokyo\"}}\n```",
	} {
		r := &nativeScript{replies: []Completion{{Text: text}}}
		executor := &recordingExecutor{output: "weather"}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		if err != nil || out.Text != text || executor.calls != 0 || len(r.requests) != 1 {
			t.Fatalf("out=%+v err=%v dispatch=%d", out, err, executor.calls)
		}
	}
}

func TestNativeLoopValidationBeforeExecution(t *testing.T) {
	for _, call := range []NativeToolCall{
		{"x", "lookup_user", `{"id":"7"}`},
		{"x", "get_weather", `{"city":"Tokyo","extra":1}`},
		{"x", "get_weather", `{"city":null}`},
		{"x", "get_weather", `{"city":"A","city":"B"}`},
		{"x", "get_weather", `[]`},
		{"x", "unknown", `{}`},
		{"", "get_weather", `{"city":"Tokyo"}`},
	} {
		r := &nativeScript{replies: []Completion{{NativeCalls: []NativeToolCall{call}}, {Text: "must not repair"}}}
		executor := &recordingExecutor{output: "weather"}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, KindToolCallInvalid)
		if executor.calls != 0 || out.Rounds != 1 || len(r.requests) != 1 {
			t.Fatal("invalid call was repaired or dispatched")
		}
	}
}

func TestNativeLoopExactIDAndContinuation(t *testing.T) {
	id := "framework:id/1"
	arguments := "{ \"city\" : \"東京\" }"
	r := &nativeScript{replies: []Completion{
		nativeReply(id, "get_weather", arguments), {Text: "Cloudy.", Usage: &Usage{10, 3, 0}},
	}}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), &recordingExecutor{output: ""})
	if err != nil || len(out.Calls) != 1 || out.Calls[0].ID != id || out.Calls[0].Arguments != arguments {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	r = &nativeScript{replies: []Completion{
		nativeReply(id, "get_weather", arguments), {Text: "Cloudy.", Usage: &Usage{10, 3, 0}},
	}}
	out, err = RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), &recordingExecutor{output: `{"temperature_c":23,"condition":"cloudy"}`})
	if err != nil || out.Text != "Cloudy." || out.Rounds != 2 || out.Usage != nil || len(r.checks) != 1 {
		t.Fatalf("out=%+v err=%v checks=%d", out, err, len(r.checks))
	}
	history := r.requests[1].History
	if len(history) != 3 || history[1].Calls[0].ID != id || history[2].ID != id ||
		history[2].Name != "get_weather" || history[2].Text == nil {
		t.Fatalf("history=%+v", history)
	}
}

func TestNativeLoopChoice(t *testing.T) {
	for _, choice := range []ToolChoice{ToolChoiceRequired, "function:get_weather"} {
		t.Run(string(choice), func(t *testing.T) {
			conversation := nativeConversation(t, choice)
			r := &nativeScript{replies: []Completion{nativeReply("one", "get_weather", `{"city":"Tokyo"}`), {Text: "Sunny."}}}
			out, err := RunToolLoop(t.Context(), r, ModelLocal, conversation, &recordingExecutor{output: "sunny"})
			if err != nil || out.Text != "Sunny." || out.Rounds != 2 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			conversation.Messages = out.Messages
			r = &nativeScript{replies: []Completion{{Text: "Sunny."}}}
			if _, err := RunToolLoop(t.Context(), r, ModelLocal, conversation, nil); err != nil {
				t.Fatal("continuation forced again", err)
			}
			conversation.Messages = append(conversation.Messages, store.Message{Role: "user", Content: "Again"})
			r = &nativeScript{replies: []Completion{{Text: "No call"}}}
			_, err = RunToolLoop(t.Context(), r, ModelLocal, conversation, nil)
			requireNativeKind(t, err, KindToolCallInvalid)
		})
	}
	t.Run("required allows dependencies", func(t *testing.T) {
		r := &nativeScript{replies: []Completion{
			nativeReply("one", "lookup_user", `{"id":7}`),
			nativeReply("two", "get_weather", `{"city":"Tokyo"}`),
			{Text: "Sunny."},
		}}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceRequired), &recordingExecutor{output: "Tokyo"})
		if err != nil || out.Rounds != 3 {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	})
	for _, choice := range []ToolChoice{ToolChoiceNone, "function:lookup_user"} {
		r := &nativeScript{replies: []Completion{nativeReply("one", "get_weather", `{"city":"Tokyo"}`)}}
		executor := &recordingExecutor{output: "sunny"}
		_, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, choice), executor)
		requireNativeKind(t, err, KindNativeProtocol)
		if executor.calls != 0 {
			t.Fatal("choice violation dispatched")
		}
	}
}

func TestNativeLoopBudgetAndAdmission(t *testing.T) {
	t.Run("eight decisions default reserves final", func(t *testing.T) {
		r := &nativeScript{}
		for i := 0; i < NativeToolRoundCap; i++ {
			r.replies = append(r.replies, nativeReply(string(rune('a'+i)), "get_weather", `{"city":"Tokyo"}`))
		}
		executor := &recordingExecutor{output: "sunny"}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, KindToolRoundCap)
		if out.Rounds != 8 || executor.calls != 7 {
			t.Fatalf("rounds=%d dispatched=%d", out.Rounds, executor.calls)
		}
	})
	t.Run("internal limit and visible history", func(t *testing.T) {
		c := nativeConversation(t, ToolChoiceAuto)
		c.RoundLimit = 3
		for _, id := range []string{"a", "b"} {
			c.Messages = append(c.Messages,
				store.Message{Role: "assistant", Content: chat.EncodeToolCalls([]chat.ToolCallRecord{{ID: id, Name: "get_weather", Arguments: `{"city":"Tokyo"}`}})},
				store.Message{Role: chat.RoleTool, Content: chat.EncodeToolResult(id, "sunny")})
		}
		r := &nativeScript{replies: []Completion{nativeReply("c", "get_weather", `{"city":"Tokyo"}`)}}
		executor := &recordingExecutor{output: "sunny"}
		_, err := RunToolLoop(t.Context(), r, ModelLocal, c, executor)
		requireNativeKind(t, err, KindToolRoundCap)
		if executor.calls != 0 || len(r.requests) != 1 {
			t.Fatal("cap dispatched")
		}
	})
	t.Run("token refusal stops later dispatch and preserves result", func(t *testing.T) {
		reply := nativeReply("a", "get_weather", `{"city":"Tokyo"}`)
		reply.NativeCalls = append(reply.NativeCalls, NativeToolCall{"b", "get_weather", `{"city":"Berlin"}`})
		r := &nativeScript{replies: []Completion{reply}, checkErr: nativeError(KindContextCapacity, "tokens")}
		executor := &recordingExecutor{output: "done"}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, KindContextCapacity)
		if executor.calls != 1 || len(out.Messages) != 3 || len(r.requests) != 1 {
			t.Fatalf("out=%+v dispatched=%d", out, executor.calls)
		}
	})
	t.Run("byte refusal stops later dispatch", func(t *testing.T) {
		reply := nativeReply("a", "get_weather", `{"city":"Tokyo"}`)
		reply.NativeCalls = append(reply.NativeCalls, NativeToolCall{"b", "get_weather", `{"city":"Berlin"}`})
		r := &nativeScript{replies: []Completion{reply}}
		executor := &recordingExecutor{output: strings.Repeat("x", NativeMaxPromptBytes)}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, KindContextCapacity)
		if executor.calls != 1 || len(out.Messages) != 3 {
			t.Fatal("oversize lost completed work or dispatched again")
		}
	})
}

func TestNativeLoopRawEnvelopeBytes(t *testing.T) {
	raw := `{ "n" : 9007199254740993, "city" : "東京" }`
	encoded, _ := json.Marshal(map[string]string{"arguments_json": raw})
	c := ToolConversation{Offers: []ToolOffer{mustMCPOffer(t, "server__raw", true)},
		Choice: ToolChoiceAuto, Messages: userTurn("lookup")}
	r := &nativeScript{replies: []Completion{nativeReply("id", "server__raw", string(encoded))}}
	out, err := RunToolLoop(t.Context(), r, ModelLocal, c, nil)
	if err != nil || len(out.Calls) != 1 || out.Calls[0].Arguments != raw {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
