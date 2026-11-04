// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
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

func TestMalformedToolAttemptUsesOneCorrection(t *testing.T) {
	for _, tc := range []struct {
		name, first, second string
		wantCalls           int
		wantText            string
		wantError           bool
	}{
		{"corrected call", malformedWeatherCall, validWeatherCall, 1, "", false},
		{"direct answer", malformedWeatherCall, "I cannot check the weather.", 0, "I cannot check the weather.", false},
		{"still malformed", malformedWeatherCall, malformedWeatherCall, 0, "", true},
		{"schema invalid after repair", malformedWeatherCall, `{"tool_calls":[{"function":{"name":"get_weather","arguments":{"city":7}}}]}`, 0, "", true},
		{"malformed after schema repair", `{"tool_calls":[{"function":{"name":"get_weather","arguments":{}}}]}`, malformedWeatherCall, 0, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &scriptedRounds{replies: []string{tc.first, tc.second}}
			out, err := RunToolLoop(context.Background(), r, ModelLocal, ToolConversation{
				Tools: mustTools(t), Choice: ToolChoiceAuto, Messages: userTurn("What's the weather in Tokyo?"),
			}, ReturnToCaller{})
			if len(r.prompts) != 2 || out.Rounds != 2 {
				t.Fatalf("rounds=%d, prompts=%d; want exactly one correction", out.Rounds, len(r.prompts))
			}
			var fault *Error
			if tc.wantError {
				if !errors.As(err, &fault) || fault.Kind != KindToolCallInvalid {
					t.Fatalf("error=%v; want tool_call_invalid", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(out.Calls) != tc.wantCalls || out.Text != tc.wantText {
				t.Fatalf("unexpected outcome: %+v", out)
			}
			if tc.wantCalls > 0 && out.Calls[0].Arguments != `{"city":"Tokyo"}` {
				t.Fatalf("unexpected arguments: %s", out.Calls[0].Arguments)
			}
			if strings.Contains(r.prompts[1], "<ctrl46>") {
				t.Fatal("correction copied malformed argument values")
			}
		})
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
	tools := mustTools(t)
	t.Run("same deadline and terminal refusal", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Minute))
		defer cancel()
		for _, kind := range []Kind{KindRequestDeclined, KindRateLimited} {
			r := &contextToolRounds{scriptedRounds: scriptedRounds{
				replies: []string{malformedWeatherCall},
				errs:    []error{nil, nativeError(kind, "stopped")},
			}}
			executor := &recordingExecutor{}
			_, err := RunToolLoop(ctx, r, ModelLocal, ToolConversation{Tools: tools, Choice: ToolChoiceAuto, Messages: userTurn("weather")}, executor)
			requireNativeKind(t, err, kind)
			if len(r.contexts) != 2 || r.contexts[0] != ctx || r.contexts[1] != ctx || executor.calls != 0 {
				t.Fatalf("contexts=%v executor calls=%d", r.contexts, executor.calls)
			}
		}
	})
	t.Run("repair still checks prompt budget", func(t *testing.T) {
		messages := userTurn("")
		base := ToolRoundRequest{Prompt: chat.RenderToolTranscript("", messages, ""), Instructions: toolInstructions, Definitions: nativeToolDefinitions(tools)}
		messages[0].Content = strings.Repeat("x", NativeMaxPromptBytes-base.inputBytes())
		r := &scriptedRounds{replies: []string{malformedWeatherCall}}
		_, err := RunToolLoop(t.Context(), r, ModelLocal, ToolConversation{Tools: tools, Choice: ToolChoiceAuto, Messages: messages}, nil)
		requireNativeKind(t, err, KindContextCapacity)
		if len(r.prompts) != 1 {
			t.Fatalf("oversized correction reached runner: %d rounds", len(r.prompts))
		}
	})
	t.Run("none bypasses attempt recognition", func(t *testing.T) {
		r := &scriptedRounds{replies: []string{malformedWeatherCall}}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, ToolConversation{Tools: tools, Choice: ToolChoiceNone, Messages: userTurn("weather")}, nil)
		if err != nil || out.Text != malformedWeatherCall || len(r.prompts) != 1 || r.modes[0] != "disallowed" {
			t.Fatalf("out=%+v error=%v", out, err)
		}
	})
}
