package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestNativePublicToolChoices(t *testing.T) {
	for _, endpoint := range []struct{ path, tools, input, named string }{
		{"/v1/chat/completions", httpTools, `"messages":[{"role":"user","content":"weather"}]`, `{"type":"function","function":{"name":"get_weather"}}`},
		{"/v1/responses", responsesHTTPTools, `"input":"weather"`, `{"type":"function","name":"get_weather"}`},
	} {
		for _, choice := range []string{`"required"`, endpoint.named} {
			fake := &toolFake{replies: []string{weatherReply}}
			body := `{"model":"local","tools":` + endpoint.tools + `,"tool_choice":` + choice + `,` + endpoint.input + `}`
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
			if res.Code != 200 || fake.calls.Load() != 1 || !strings.Contains(res.Body.String(), `"get_weather"`) {
				t.Fatalf("choice=%s status=%d body=%s", choice, res.Code, res.Body.String())
			}
			want := "required"
			if choice == endpoint.named {
				want = "function"
			}
			if fake.requests[0].Choice.Type != want || (want == "function" && fake.requests[0].Choice.Name != "get_weather") {
				t.Fatalf("choice lost: %+v", fake.requests[0].Choice)
			}
			fake = &toolFake{replies: []string{"No call."}}
			res = post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
			if res.Code != 502 || !strings.Contains(res.Body.String(), "tool_call_invalid") || fake.calls.Load() != 1 {
				t.Fatalf("forcing obligation ignored: %d %s", res.Code, res.Body.String())
			}
			history := toolResultHistory(endpoint.path == "/v1/responses", `""`)
			member := "messages"
			if endpoint.path == "/v1/responses" {
				member = "input"
			}
			body = `{"model":"local","tools":` + endpoint.tools + `,"tool_choice":` + choice + `,"` + member + `":` + history + `}`
			fake = &toolFake{replies: []string{"Done."}}
			res = post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
			if res.Code != 200 || fake.calls.Load() != 1 || !strings.Contains(res.Body.String(), "Done.") {
				t.Fatalf("continuation forced twice: %d %s", res.Code, res.Body.String())
			}
		}
	}
}

type samplingFake struct {
	nativeFake
	requests []runner.GenerationRequest
}

func (f *samplingFake) RunGeneration(_ context.Context, model runner.Model, request runner.GenerationRequest, emit func(string) error) (runner.Completion, error) {
	f.requests = append(f.requests, request)
	if emit != nil {
		if err := emit("Sampled."); err != nil {
			return runner.Completion{}, err
		}
	}
	return runner.Completion{Text: "Sampled.", Model: model}, nil
}

func TestNativeHTTPSampling(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		tokens := "max_tokens"
		if endpoint.path == "/v1/responses" {
			tokens = "max_output_tokens"
		}
		for _, stream := range []bool{false, true} {
			fake := &samplingFake{}
			options := `,"temperature":0.7,"top_p":0.8,"seed":18446744073709551615,"` + tokens + `":12`
			if stream {
				options += `,"stream":true`
			}
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, strings.Replace(endpoint.body, "%s", options, 1))
			if res.Code != 200 || len(fake.requests) != 1 || fake.calls.Load() != 0 {
				t.Fatalf("sampling not applied: %d %s requests=%d", res.Code, res.Body.String(), len(fake.requests))
			}
			raw, _ := json.Marshal(fake.requests[0].Options)
			if string(raw) != `{"temperature":0.7,"top_p":0.8,"max_tokens":12,"seed":18446744073709551615}` {
				t.Fatalf("options changed: %s", raw)
			}
		}
		for _, fields := range []string{
			`,"temperature":-1`, `,"temperature":2.1`, `,"temperature":"0"`, `,"top_p":0`, `,"top_p":1.1`,
			`,"temperature":0,"seed":1`, `,"temperature":0,"top_p":1`,
			`,"seed":-1`, `,"seed":18446744073709551616`, `,"seed":1.5`,
			`,"` + tokens + `":0`, `,"` + tokens + `":1.5`,
		} {
			fake := &samplingFake{}
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, strings.Replace(endpoint.body, "%s", fields, 1))
			if res.Code != 400 || len(fake.requests) != 0 || fake.calls.Load() != 0 {
				t.Fatalf("invalid sampling dispatched: %s %d %s", fields, res.Code, res.Body.String())
			}
		}
		for _, model := range []string{"auto", "cloud", "cloud-pro", "chatgpt", "on-device"} {
			fake := &samplingFake{}
			body := strings.Replace(strings.Replace(endpoint.body, `"local"`, `"`+model+`"`, 1), "%s", `,"temperature":0`, 1)
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
			if res.Code != 400 || len(fake.requests) != 0 || fake.calls.Load() != 0 {
				t.Fatalf("sampling changed non-Local route: %s %d %s", model, res.Code, res.Body.String())
			}
		}
	}
}

func TestNativeToolSamplingPreserved(t *testing.T) {
	for _, endpoint := range []struct{ path, tools, input string }{
		{"/v1/chat/completions", httpTools, `"messages":[{"role":"user","content":"weather"}]`},
		{"/v1/responses", responsesHTTPTools, `"input":"weather"`},
	} {
		fake := &toolFake{replies: []string{weatherReply}}
		res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path,
			`{"model":"local","temperature":0,"tools":`+endpoint.tools+`,`+endpoint.input+`}`)
		if res.Code != 200 || fake.calls.Load() != 1 || fake.requests[0].Options == nil ||
			fake.requests[0].Options.Temperature == nil || *fake.requests[0].Options.Temperature != 0 {
			t.Fatalf("tool sampling lost: %d %s", res.Code, res.Body.String())
		}
	}
}
