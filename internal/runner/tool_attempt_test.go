// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

const malformedWeatherCall = `{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":{"city":<ctrl46>Tokyo<ctrl46>}}}]}`
const validWeatherCall = `{"tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Tokyo"}}}]}`

// Any reply with a "tool_calls" marker and a quoted offered name is a call
// attempt and gets one correction, however its JSON is broken. Only text
// lacking either stays plain.
func TestMalformedToolAttemptClassification(t *testing.T) {
	tools := mustTools(t)
	for _, tc := range []struct {
		name, text string
		repair     bool
	}{
		{"captured control tokens", malformedWeatherCall, true},
		{"preamble", "Let me check.\n" + malformedWeatherCall, true},
		{"fence", fence + "json\n" + malformedWeatherCall + "\n" + fence, true},
		{"whitespace", " { \"tool_calls\" : [ { \"function\" : { \"name\" : \"get_weather\", \"arguments\" : ? } } ] } ", true},
		{"unescaped argument quotes", `{"tool_calls":[{"function":{"name":"get_weather","arguments":"{"city":"Tokyo"}"}}]}`, true},
		{"bare function", `{"tool_calls":[{"function":"get_weather","arguments":{"city":?}}]}`, true},
		{"ordinary answer", "Tokyo is a city.", false},
		{"ordinary mention", `The "tool_calls" and "function" fields describe a protocol.`, false},
		{"encoded example", `"{\"tool_calls\":[{\"function\":{\"name\":\"get_weather\",\"arguments\":?}}]}"`, false},
		{"unknown tool", strings.Replace(malformedWeatherCall, "get_weather", "invented", 1), false},
		{"offered name in arguments under the marker is corrected", `{"tool_calls":[{"function":{"arguments":{"name":"get_weather","city":?}}}]}`, true},
		{"unrelated name", `{"name":"get_weather","arguments":?}`, false},
		{"offered name beside the marker is corrected", `{"name":"get_weather"} {"tool_calls":[{"arguments":?}]}`, true},
		{"no function name", `{"tool_calls":[{"function":{"arguments":?}}]}`, false},
		{"nested offered name is corrected", `{"tool_calls":[{"function":{"name":{"name":"get_weather"},"arguments":?}}]}`, true},
		{"duplicate function keys are corrected", `{"tool_calls":[{"function":{"name":"get_weather","arguments":?},"function":{"name":"invented","arguments":{}}}]}`, true},
		{"duplicate name keys are corrected", `{"tool_calls":[{"function":{"name":"get_weather","name":"invented","arguments":?}}]}`, true},
		{"duplicate header fields are corrected", `{"tool_calls":[{"id":"a","id":"b","function":{"name":"get_weather","arguments":?}}]}`, true},
		{"malformed calls with one offered name are corrected", `{"tool_calls":[{"function":{"name":"get_weather","arguments":?}},{"function":{"name":"invented","arguments":{}}}]}`, true},
		{"malformed multiple known calls are corrected", `{"tool_calls":[{"function":{"name":"get_weather","arguments":?}},{"function":{"name":"add","arguments":?}}]}`, true},
		{"offered name before a malformed marker is corrected", `{"function":{"name":"get_weather"}} {"tool_calls":[?]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := interpretRound(tc.text, tools, 1, map[string]bool{})
			if len(got.calls) != 0 {
				t.Fatalf("malformed text surfaced calls: %+v", got.calls)
			}
			if tc.repair {
				if got.plain || got.framing || got.fault == "" {
					t.Fatalf("want repairable failure, got %+v", got)
				}
				if strings.Contains(got.fault, "Tokyo") || strings.Contains(got.fault, "<ctrl46>") {
					t.Fatalf("failure quotes argument data: %s", got.fault)
				}
			} else if !got.plain {
				t.Fatalf("text without a marker and a quoted offered name changed policy: %+v", got)
			}
		})
	}
}

func TestMalformedToolAttemptBoundedRecognition(t *testing.T) {
	if got := malformedToolAttemptName(strings.Repeat(" ", maxDetectionSteps) + malformedWeatherCall); got != "" {
		t.Fatalf("over-limit reply recognized as %q", got)
	}
	text := strings.Repeat("{", 100_000) + malformedWeatherCall
	got := interpretRound(text, mustTools(t), 1, map[string]bool{})
	if got.fault == "" || len(got.calls) != 0 {
		t.Fatalf("bounded scanner must only classify the known attempt: %+v", got)
	}
}

type contextToolRounds struct {
	scriptedRounds
	contexts []context.Context
}

func (r *contextToolRounds) RunToolRound(ctx context.Context, model Model, request ToolRoundRequest) (Completion, error) {
	r.contexts = append(r.contexts, ctx)
	return r.scriptedRounds.RunToolRound(ctx, model, request)
}

func TestMalformedToolAttemptKeepsLoopGuards(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Minute))
	defer cancel()
	for _, kind := range []Kind{KindRequestDeclined, KindRateLimited, KindNativeProtocol, KindTimeout} {
		r := &nativeScript{errs: []error{nativeError(kind, "stopped")}}
		executor := &recordingExecutor{output: "never"}
		out, err := RunToolLoop(ctx, r, ModelLocal, nativeConversation(t, ToolChoiceAuto), executor)
		requireNativeKind(t, err, kind)
		if out.Rounds != 1 || len(r.contexts) != 1 || r.contexts[0] != ctx || executor.calls != 0 {
			t.Fatal("typed refusal retried, changed deadline, or dispatched")
		}
	}
	c := nativeConversation(t, ToolChoiceAuto)
	c.Messages = userTurn(strings.Repeat("x", NativeMaxPromptBytes))
	r := &nativeScript{}
	_, err := RunToolLoop(ctx, r, ModelLocal, c, nil)
	requireNativeKind(t, err, KindContextCapacity)
	if len(r.requests) != 0 {
		t.Fatal("oversize reached inference")
	}
	r = &nativeScript{replies: []Completion{{Text: malformedWeatherCall}}}
	out, err := RunToolLoop(ctx, r, ModelLocal, nativeConversation(t, ToolChoiceNone), nil)
	if err != nil || out.Text != malformedWeatherCall || len(out.Calls) != 0 || r.requests[0].Choice.Type != "none" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
