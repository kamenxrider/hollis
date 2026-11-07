// Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

const errorPrivacyMarker = "ZZ-SECRET-MARKER-ZZ"

func TestNativeErrorMessagesNeverExposeCauses(t *testing.T) {
	for _, kind := range []runner.Kind{runner.KindUsage, runner.KindToolRoundCap, runner.KindToolCallInvalid} {
		for _, cause := range []error{nil, errors.New(strings.Repeat(errorPrivacyMarker, 1024))} {
			t.Run(string(kind)+fmt.Sprint(cause == nil), func(t *testing.T) {
				status, detail := nativeError(&runner.Error{Kind: kind, Err: cause})
				contract := runner.ErrorContractFor(kind)
				if status != contract.HTTPStatus || detail["type"] != contract.HTTPType ||
					detail["code"] != contract.HTTPCode || detail["message"] != contract.HTTPMessage ||
					contract.HTTPMessage == "" || strings.Contains(detail["message"].(string), errorPrivacyMarker) {
					t.Fatalf("status=%d detail=%v contract=%v", status, detail, contract)
				}
			})
		}
	}
}

func TestNativeHTTPErrorPrivacy(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		for _, kind := range []runner.Kind{runner.KindUsage, runner.KindToolRoundCap, runner.KindToolCallInvalid} {
			for _, stream := range []bool{false, true} {
				t.Run(endpoint.path+"/"+string(kind)+fmt.Sprint(stream), func(t *testing.T) {
					failure := func(prompt string) error {
						if !strings.Contains(prompt, errorPrivacyMarker) {
							t.Fatal("marked user content did not reach the runner")
						}
						// A backend cause might quote user text, arguments,
						// results, file paths/contents or an unbounded name.
						return &runner.Error{Kind: kind, Err: fmt.Errorf("user, tool arguments/results, file contents: %s %s",
							prompt, strings.Repeat(errorPrivacyMarker, 1024))}
					}
					fake := &nativeFake{
						complete: func(_ context.Context, prompt string) (runner.Completion, error) {
							return runner.Completion{}, failure(prompt)
						},
						stream: func(_ context.Context, prompt string, emit func(string) error) (runner.Completion, error) {
							if err := emit("safe prefix"); err != nil {
								t.Fatal(err)
							}
							return runner.Completion{}, failure(prompt)
						},
					}
					body := strings.ReplaceAll(nativeBody(endpoint.body, stream), `"hi"`, `"`+errorPrivacyMarker+`"`)
					res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
					wantStatus := runner.ErrorContractFor(kind).HTTPStatus
					if stream {
						wantStatus = http.StatusOK // Failure after the first SSE event.
					}
					if res.Code != wantStatus || fake.calls.Load() != 1 ||
						!strings.Contains(res.Body.String(), runner.ErrorContractFor(kind).HTTPCode) ||
						strings.Contains(res.Body.String(), errorPrivacyMarker) {
						t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
					}
				})
			}
		}
	}
}

func TestToolHTTPErrorPrivacy(t *testing.T) {
	for _, endpoint := range []struct{ path, tools, history string }{
		{"/v1/chat/completions", httpTools, `"messages":[{"role":"user","content":"MARKER"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"MARKER\"}"}}]},
			{"role":"tool","tool_call_id":"c","content":"MARKER"}]`},
		{"/v1/responses", responsesHTTPTools, `"input":[{"role":"user","content":"MARKER"},
			{"type":"function_call","call_id":"c","name":"get_weather","arguments":"{\"city\":\"MARKER\"}"},
			{"type":"function_call_output","call_id":"c","output":"MARKER"}]`},
	} {
		for _, kind := range []runner.Kind{runner.KindUsage, runner.KindToolRoundCap, runner.KindToolCallInvalid} {
			t.Run(endpoint.path+"/"+string(kind), func(t *testing.T) {
				fake := &toolFake{err: &runner.Error{Kind: kind, Err: errors.New(errorPrivacyMarker)}}
				body := `{"model":"local","tools":` + endpoint.tools + `,` + strings.ReplaceAll(endpoint.history, "MARKER", errorPrivacyMarker) + `}`
				res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
				if res.Code != runner.ErrorContractFor(kind).HTTPStatus || fake.calls.Load() != 1 ||
					strings.Contains(res.Body.String(), errorPrivacyMarker) {
					t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
				}
				history := fake.requests[0].History
				if len(history) != 3 || history[0].Text == nil || !strings.Contains(*history[0].Text, errorPrivacyMarker) ||
					len(history[1].Calls) != 1 || !strings.Contains(history[1].Calls[0].Arguments, errorPrivacyMarker) ||
					history[2].Text == nil || !strings.Contains(*history[2].Text, errorPrivacyMarker) {
					t.Fatal("marked user content, arguments and results were not preserved for the runner")
				}
			})
		}
	}
}

func TestInvalidHistoryHTTPErrorPrivacy(t *testing.T) {
	cases := []struct{ name, path, body string }{
		{"chat role", "/v1/chat/completions", `"messages":[{"role":"MARKER","content":"MARKER"}]`},
		{"chat part", "/v1/chat/completions", `"messages":[{"role":"user","content":[{"type":"MARKER","text":"MARKER"}]}]`},
		{"responses role", "/v1/responses", `"input":[{"role":"MARKER","content":"MARKER"}]`},
		{"responses item", "/v1/responses", `"input":[{"type":"MARKER","role":"user","content":"MARKER"}]`},
		{"chat tool role", "/v1/chat/completions", `"tools":` + httpTools + `,"messages":[{"role":"MARKER","content":"MARKER"}]`},
		{"responses tool item", "/v1/responses", `"tools":` + responsesHTTPTools + `,"input":[{"type":"MARKER","content":"MARKER"}]`},
		{"chat orphan result", "/v1/chat/completions", `"tools":` + httpTools + `,"messages":[{"role":"user","content":"MARKER"},{"role":"tool","tool_call_id":"MARKER","content":"MARKER"}]`},
		{"responses orphan result", "/v1/responses", `"tools":` + responsesHTTPTools + `,"input":[{"role":"user","content":"MARKER"},{"type":"function_call_output","call_id":"MARKER","output":"MARKER"}]`},
		{"chat invalid arguments", "/v1/chat/completions", `"tools":` + httpTools + `,"messages":[{"role":"user","content":"MARKER"},{"role":"assistant","tool_calls":[{"id":"MARKER","type":"function","function":{"name":"MARKER","arguments":"MARKER"}}]}]`},
		{"responses invalid arguments", "/v1/responses", `"tools":` + responsesHTTPTools + `,"input":[{"type":"function_call","call_id":"MARKER","name":"MARKER","arguments":"MARKER"}]`},
		{"choice name", "/v1/chat/completions", `"tools":` + httpTools + `,"tool_choice":{"type":"function","function":{"name":"MARKER"}},"messages":[{"role":"user","content":"MARKER"}]`},
		{"long tool name", "/v1/chat/completions", `"tools":` + strings.ReplaceAll(httpTools, "get_weather", strings.Repeat("MARKER", 100)) + `,"messages":[{"role":"user","content":"MARKER"}]`},
		{"offered name", "/v1/chat/completions", `"tools":` + strings.ReplaceAll(httpTools, "get_weather", "MARKER") + `,"tool_choice":{"type":"function","function":{"name":"missing"}},"messages":[{"role":"user","content":"MARKER"}]`},
		{"tool schema pointer", "/v1/chat/completions", `"tools":` + strings.ReplaceAll(httpTools, `"city":{"type":"string"}`, `"MARKER":{"type":"string","const":"MARKER"}`) + `,"messages":[{"role":"user","content":"MARKER"}]`},
		{"guided schema pointer", "/v1/chat/completions", `"messages":[{"role":"user","content":"MARKER"}],"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object","properties":{"MARKER":{"type":"string","const":"MARKER"}},"additionalProperties":false}}}`},
		{"chat format", "/v1/chat/completions", `"messages":[{"role":"user","content":"MARKER"}],"response_format":{"type":"MARKER"}`},
		{"responses format", "/v1/responses", `"input":"MARKER","text":{"format":{"type":"MARKER"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &toolFake{}
			body := `{"model":"local",` + strings.ReplaceAll(tc.body, "MARKER", errorPrivacyMarker) + `}`
			res := post(t, NewUnauthenticated(fake).Handler(), tc.path, body)
			if res.Code != http.StatusBadRequest || fake.calls.Load() != 0 ||
				strings.Contains(res.Body.String(), errorPrivacyMarker) || len(res.Body.Bytes()) > 1024 {
				t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
			}
		})
	}
}
