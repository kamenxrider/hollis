// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"errors"
	"strings"
	"testing"
)

const missingWeatherArrayCloser = `{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":{"city":"Tokyo"}}}}`

func TestToolCallMissingTerminalArrayCloser(t *testing.T) {
	for _, text := range []string{
		missingWeatherArrayCloser,
		" \n" + missingWeatherArrayCloser + "\t ",
		strings.Replace(missingWeatherArrayCloser, "Tokyo", `a]}{[b\"c`, 1),
	} {
		got := interpretRound(text, mustTools(t), 1, map[string]bool{})
		if got.plain || got.fault != "" || len(got.calls) != 1 || got.calls[0].Name != "get_weather" {
			t.Fatal("unambiguous terminal array closer was not recovered")
		}
	}
}

func TestToolCallTerminalCloserKeepsValidation(t *testing.T) {
	for _, tc := range []struct {
		name, text string
	}{
		{"wrong argument type", strings.Replace(missingWeatherArrayCloser, `"Tokyo"`, `["Tokyo"]`, 1)},
		{"unknown tool", strings.Replace(missingWeatherArrayCloser, "get_weather", "invented", 1)},
		{"missing required argument", strings.Replace(missingWeatherArrayCloser, `{"city":"Tokyo"}`, `{}`, 1)},
		{"duplicate argument", strings.Replace(missingWeatherArrayCloser, `"city":"Tokyo"`, `"city":"Tokyo","city":"Paris"`, 1)},
		{"mismatched argument container", strings.Replace(missingWeatherArrayCloser, `"Tokyo"`, `["Tokyo"}`, 1)},
		{"extra closer", missingWeatherArrayCloser + "}"},
		{"trailing text", missingWeatherArrayCloser + " trailing"},
		{"trailing object", missingWeatherArrayCloser + `{}`},
		{"trailing comma", strings.TrimSuffix(missingWeatherArrayCloser, "}") + ",}"},
		{"quoted whole reply", `"` + strings.ReplaceAll(missingWeatherArrayCloser, `"`, `\"`) + `"`},
		{"ambiguous wrappers", missingWeatherArrayCloser + missingWeatherArrayCloser},
		{"truncated string", `{"tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Tok}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := interpretRound(tc.text, mustTools(t), 1, map[string]bool{})
			if len(got.calls) != 0 {
				t.Fatal("invalid, ambiguous or unknown call surfaced")
			}
		})
	}
}

func TestToolCallTerminalCloserBounds(t *testing.T) {
	for _, text := range []string{
		"prefix " + missingWeatherArrayCloser,
		"```json\n" + missingWeatherArrayCloser + "\n```",
		strings.Repeat(" ", maxDetectionSteps) + missingWeatherArrayCloser,
		strings.TrimSuffix(missingWeatherArrayCloser, "}"),
	} {
		if _, ok := closeTerminalToolArray(text); ok {
			t.Fatal("repair escaped its whole-reply, terminal or size boundary")
		}
	}
	for _, count := range []int{2, MaxToolCallsPerRound + 1} {
		call := `{"function":{"name":"get_weather","arguments":{"city":"Tokyo"}}}`
		text := `{"tool_calls":[` + strings.TrimSuffix(strings.Repeat(call+",", count), ",") + `}`
		got := interpretRound(text, mustTools(t), 1, map[string]bool{})
		if count == 2 {
			if got.fault != "" || len(got.calls) != count {
				t.Fatal("repair lost a complete call")
			}
			text = strings.Replace(text, `"Tokyo"`, `["Tokyo"]`, 1)
			if invalid := interpretRound(text, mustTools(t), 1, map[string]bool{}); len(invalid.calls) != 0 {
				t.Fatal("repair surfaced a partially valid batch")
			}
		} else if got.fault == "" || len(got.calls) != 0 {
			t.Fatal("repair bypassed the per-round call limit")
		}
	}
}

func TestToolCallTerminalCloserUsesNoExtraGeneration(t *testing.T) {
	invalid := strings.Replace(validWeatherCall, `"Tokyo"`, `["Tokyo"]`, 1)
	rounds := &scriptedRounds{replies: []string{invalid, missingWeatherArrayCloser}}
	out, err := RunToolLoop(t.Context(), rounds, ModelLocal, ToolConversation{
		Tools: mustTools(t), Choice: ToolChoiceAuto, Messages: userTurn("Weather in Tokyo?"),
	}, ReturnToCaller{})
	if err != nil || out.Rounds != 2 || len(rounds.requests) != 2 || len(out.Calls) != 1 {
		t.Fatal("structural repair did not finish within the existing single correction")
	}
	if out.Calls[0].Arguments != `{"city":"Tokyo"}` {
		t.Fatal("argument value changed")
	}
	rounds = &scriptedRounds{replies: []string{invalid, invalid, missingWeatherArrayCloser}}
	out, err = RunToolLoop(t.Context(), rounds, ModelLocal, ToolConversation{
		Tools: mustTools(t), Choice: ToolChoiceAuto, Messages: userTurn("Weather in Tokyo?"),
	}, ReturnToCaller{})
	var fault *Error
	if !errors.As(err, &fault) || fault.Kind != KindToolCallInvalid ||
		out.Rounds != 2 || len(rounds.requests) != 2 || len(out.Calls) != 0 {
		t.Fatal("invalid repair added generation or escaped validation")
	}
}
