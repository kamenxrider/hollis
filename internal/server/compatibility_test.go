// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestChatCompatibilityFields(t *testing.T) {
	for _, tc := range []struct {
		field  string
		status int
	}{
		{`"n":1`, 200}, {`"n":2`, 400}, {`"n":null`, 400}, {`"n":"1"`, 400},
		{`"logprobs":false`, 200}, {`"logprobs":null`, 200}, {`"logprobs":true`, 400},
		{`"top_logprobs":null`, 200}, {`"top_logprobs":0`, 400},
		{`"user":"private-label"`, 200}, {`"user":4`, 400}, {`"user":null`, 400},
		{`"unknown":false`, 400},
		{`"stream_options":{"include_usage":true}`, 400},
		{`"stream":true,"stream_options":{"unknown":true}`, 400},
		{`"stream":true,"stream_options":{"include_usage":"yes"}`, 400},
	} {
		t.Run(tc.field, func(t *testing.T) {
			fake := &nativeFake{complete: func(_ context.Context, prompt string) (runner.Completion, error) {
				if strings.Contains(prompt, "private-label") {
					t.Fatal("user label reached model")
				}
				return runner.Completion{Text: "ok", Model: runner.ModelLocal}, nil
			}}
			res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"hi"}],`+tc.field+`}`)
			if res.Code != tc.status {
				t.Fatalf("%d %s", res.Code, res.Body.String())
			}
			if tc.status == 400 && fake.calls.Load() != 0 {
				t.Fatal("rejection ran model")
			}
		})
	}
}

func TestChatStreamingIncludeUsage(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, known := range []bool{false, true} {
			fake := &nativeFake{stream: func(_ context.Context, _ string, emit func(string) error) (runner.Completion, error) {
				if err := emit("ok"); err != nil {
					return runner.Completion{}, err
				}
				result := runner.Completion{Text: "ok", Model: runner.ModelLocal}
				if known {
					result.Usage = &runner.Usage{InputTokens: 3, OutputTokens: 2}
				}
				return result, nil
			}}
			value := "false"
			if enabled {
				value = "true"
			}
			res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":`+value+`}}`)
			if res.Code != 200 {
				t.Fatal(res.Body.String())
			}
			_, events := decodeEvents(t, res.Body.String())
			last := events[len(events)-1]
			usage, present := last["usage"]
			if present != enabled {
				t.Fatalf("usage present=%v want %v", present, enabled)
			}
			if enabled {
				if len(last["choices"].([]any)) != 0 {
					t.Fatal("usage choices not empty")
				}
				if (usage != nil) != known {
					t.Fatalf("usage=%v", usage)
				}
			}
		}
	}
}

func TestChatJSONObject(t *testing.T) {
	for _, tc := range []struct {
		output string
		status int
	}{
		{`{"answer":42}`, 200}, {`{}`, 200}, {`[]`, 502}, {`null`, 502}, {`{"broken":`, 502}, {"```json\n{}\n```", 502},
	} {
		fake := &nativeFake{complete: func(_ context.Context, prompt string) (runner.Completion, error) {
			if !strings.Contains(prompt, "one valid JSON object") {
				t.Fatal("missing guidance")
			}
			return runner.Completion{Text: tc.output, Model: runner.ModelLocal}, nil
		}}
		res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`)
		if res.Code != tc.status || !json.Valid(res.Body.Bytes()) {
			t.Fatalf("%d %s", res.Code, res.Body.String())
		}
		if tc.status == 502 && !strings.Contains(res.Body.String(), "invalid_json_output") {
			t.Fatal(res.Body.String())
		}
	}
	for _, extra := range []string{`"model":"cloud","stream":true`, `"model":"local","stream":true`, `"model":"local","tools":[]`} {
		fake := &nativeFake{}
		res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", `{`+extra+`,"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`)
		if res.Code != 400 || fake.calls.Load() != 0 {
			t.Fatalf("%d %s", res.Code, res.Body.String())
		}
	}
}
