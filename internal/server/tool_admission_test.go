// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const responsesHTTPTools = `[{"type":"function","name":"get_weather","description":"Get the weather for a city",` +
	`"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]},"strict":true}]`

func TestEndpointToolDefinitionAdmission(t *testing.T) {
	for _, endpoint := range []struct{ path, input, tools, wrong string }{
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"weather"}]`, strings.Replace(httpTools, `"description":`, `"strict":true,"description":`, 1), responsesHTTPTools},
		{"/v1/responses", `"input":"weather"`, responsesHTTPTools, httpTools},
	} {
		for _, tc := range []struct {
			name, tools string
			status      int
		}{
			{"correct shape", endpoint.tools, 200},
			{"wrong endpoint shape", endpoint.wrong, 400},
			{"strict false", strings.ReplaceAll(endpoint.tools, `"strict":true`, `"strict":false`), 200},
			{"strict omitted", strings.ReplaceAll(strings.ReplaceAll(endpoint.tools, `,"strict":true`, ""), `"strict":true,`, ""), 200},
			{"strict wrong type", strings.ReplaceAll(endpoint.tools, `"strict":true`, `"strict":"yes"`), 400},
			{"unknown member", strings.Replace(endpoint.tools, `"description":`, `"invented":true,"description":`, 1), 400},
			{"unsupported schema", strings.Replace(endpoint.tools, `"type":"object"`, `"type":"object","additionalProperties":true`, 1), 400},
			{"strict false still validates schema", strings.ReplaceAll(strings.Replace(endpoint.tools, `"type":"object"`, `"type":"object","additionalProperties":true`, 1), `"strict":true`, `"strict":false`), 400},
			{"null parameters", strings.Replace(endpoint.tools, `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`, `null`, 1), 400},
			{"wrong tool type", strings.Replace(endpoint.tools, `"type":"function"`, `"type":"web_search"`, 1), 400},
			{"missing description", strings.Replace(endpoint.tools, `"Get the weather for a city"`, `null`, 1), 400},
			{"duplicate names", "[" + strings.Trim(endpoint.tools, "[]") + "," + strings.Trim(endpoint.tools, "[]") + "]", 400},
			{"too many tools", "[" + strings.Repeat(strings.Trim(endpoint.tools, "[]")+",", 32) + strings.Trim(endpoint.tools, "[]") + "]", 400},
			{"non-array", `{}`, 400},
			{"null entry", `[null]`, 400},
			{"scalar entry", `[true]`, 400},
		} {
			t.Run(endpoint.path+"/"+tc.name, func(t *testing.T) {
				fake := &toolFake{replies: []string{weatherReply}}
				res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path,
					`{"model":"local","tools":`+tc.tools+`,`+endpoint.input+`}`)
				wantCalls := int32(0)
				if tc.status == 200 {
					wantCalls = 1
				}
				if res.Code != tc.status || fake.calls.Load() != wantCalls {
					t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
				}
				if tc.status == 400 && !strings.Contains(res.Body.String(), `"code":"unsupported_parameter"`) {
					t.Fatal(res.Body.String())
				}
			})
		}
	}
}

func toolResultHistory(responses bool, value string) string {
	member := ""
	if value != "" {
		member = `,"content":` + value
		if responses {
			member = `,"output":` + value
		}
	}
	if responses {
		return `[{"type":"function_call","call_id":"c","name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"},` +
			`{"type":"function_call_output","call_id":"c"` + member + `}]`
	}
	return `[{"role":"assistant","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}]},` +
		`{"role":"tool","tool_call_id":"c"` + member + `}]`
}

func TestToolResultNullRejectedByParsers(t *testing.T) {
	// Independent of definition admission: null must not decode as an empty string.
	if _, err := parseChatToolMessages(json.RawMessage(toolResultHistory(false, "null"))); err == nil {
		t.Error("Chat tool content:null accepted")
	}
	if _, err := parseResponsesToolInput(json.RawMessage(toolResultHistory(true, "null")), ""); err == nil {
		t.Error("Responses function_call_output output:null accepted")
	}
}

func TestToolResultEndpointAdmission(t *testing.T) {
	for _, responses := range []bool{false, true} {
		path, tools, member := "/v1/chat/completions", httpTools, "messages"
		if responses {
			path, tools, member = "/v1/responses", responsesHTTPTools, "input"
		}
		for _, tc := range []struct {
			name, value string
			valid       bool
		}{
			{"missing", "", false}, {"null", "null", false},
			{"object", `{}`, false}, {"number", `42`, false}, {"boolean", `false`, false},
			{"empty string", `""`, true}, {"string", `"sunny"`, true},
			{"text parts", `[{"type":"text","text":"sunny"}]`, !responses},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				fake := &toolFake{replies: []string{"Done."}}
				res := post(t, NewUnauthenticated(fake).Handler(), path,
					`{"model":"local","tools":`+tools+`,"`+member+`":`+toolResultHistory(responses, tc.value)+`}`)
				status, calls := http.StatusBadRequest, int32(0)
				if tc.valid {
					status, calls = http.StatusOK, 1
				}
				if res.Code != status || fake.calls.Load() != calls {
					t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
				}
				if !tc.valid && !strings.Contains(res.Body.String(), `"code":"invalid_input"`) {
					t.Fatal(res.Body.String())
				}
				if tc.valid && tc.value == `""` && !strings.Contains(fake.prompts[0], `"output":""`) {
					t.Fatal("empty string result was lost")
				}
			})
		}
	}
}
