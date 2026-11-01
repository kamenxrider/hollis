// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

const guidedTestSchema = `{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"},"age":{"type":"integer"}},"required":["name"]}`

const guidedTestPayload = `{"name":"Ada","age":36}`

// guidedFake records every transport call so a test can prove a rejected
// request never reached the runner. It implements the complete and schema
// interfaces the real native runner provides.
type guidedFake struct {
	mu            sync.Mutex
	schemaCalls   int
	completeCalls int
	prompts       []string
	schemas       [][]byte
	result        runner.Completion
	err           error
	completeText  string
}

func (f *guidedFake) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	panic("local must use the native interface")
}

func (f *guidedFake) RunComplete(_ context.Context, model runner.Model, prompt string) (runner.Completion, error) {
	f.mu.Lock()
	f.completeCalls++
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	text := f.completeText
	if text == "" {
		text = "plain"
	}
	return runner.Completion{Text: text, Model: model, Usage: &runner.Usage{InputTokens: 1, OutputTokens: 2}}, nil
}

func (f *guidedFake) RunSchema(_ context.Context, model runner.Model, prompt string, schema *runner.Schema) (runner.Completion, error) {
	f.mu.Lock()
	f.schemaCalls++
	f.prompts = append(f.prompts, prompt)
	f.schemas = append(f.schemas, append([]byte(nil), schema.Bytes()...))
	result, err := f.result, f.err
	f.mu.Unlock()
	if err != nil {
		return runner.Completion{Model: model}, err
	}
	if result.Model == "" {
		result.Model = model
	}
	return result, nil
}

func (f *guidedFake) snapshot() (schemaCalls, completeCalls int, prompts []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.schemaCalls, f.completeCalls, append([]string(nil), f.prompts...)
}

func (f *guidedFake) schemaPayloads() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.schemas...)
}

func newGuidedFake() *guidedFake {
	return &guidedFake{result: runner.Completion{
		Usage:      &runner.Usage{InputTokens: 84, OutputTokens: 19, ReasoningTokens: 3},
		Structured: json.RawMessage(guidedTestPayload),
	}}
}

// guidedBodies builds the same logical request for both endpoints.
func guidedBodies(schema string) map[string]string {
	return map[string]string{
		"/v1/chat/completions": `{"model":"local","messages":[{"role":"user","content":"hi"}],` +
			`"response_format":{"type":"json_schema","json_schema":{"name":"person","schema":` + schema + `}}}`,
		"/v1/responses": `{"model":"local","input":"hi",` +
			`"text":{"format":{"type":"json_schema","name":"person","schema":` + schema + `}}}`,
	}
}

func TestGuidedOutputSuccessBothEndpoints(t *testing.T) {
	for path, body := range guidedBodies(guidedTestSchema) {
		t.Run(path, func(t *testing.T) {
			fake := newGuidedFake()
			res := post(t, NewUnauthenticated(fake).Handler(), path, body)
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["model"] != "local" {
				t.Fatalf("model = %v", result["model"])
			}
			var content string
			switch path {
			case "/v1/chat/completions":
				choices := result["choices"].([]any)
				choice := choices[0].(map[string]any)
				if choice["finish_reason"] != "stop" {
					t.Fatalf("finish_reason = %v", choice["finish_reason"])
				}
				message := choice["message"].(map[string]any)
				if message["role"] != "assistant" {
					t.Fatalf("role = %v", message["role"])
				}
				content = message["content"].(string)
				usage := result["usage"].(map[string]any)
				if usage["prompt_tokens"] != float64(84) || usage["completion_tokens"] != float64(19) {
					t.Fatalf("usage = %#v", usage)
				}
			default:
				output := result["output"].([]any)
				message := output[0].(map[string]any)
				if message["type"] != "message" || message["role"] != "assistant" {
					t.Fatalf("message = %#v", message)
				}
				parts := message["content"].([]any)
				part := parts[0].(map[string]any)
				if part["type"] != "output_text" {
					t.Fatalf("content type = %v", part["type"])
				}
				content = part["text"].(string)
				usage := result["usage"].(map[string]any)
				if usage["input_tokens"] != float64(84) || usage["output_tokens"] != float64(19) {
					t.Fatalf("usage = %#v", usage)
				}
			}
			if content != guidedTestPayload {
				t.Fatalf("content = %q; want the validated object as a JSON string", content)
			}
			schemaCalls, completeCalls, prompts := fake.snapshot()
			if schemaCalls != 1 || completeCalls != 0 {
				t.Fatalf("schema calls=%d complete calls=%d; want exactly one schema call", schemaCalls, completeCalls)
			}
			if len(prompts) != 1 || !strings.Contains(prompts[0], "hi") {
				t.Fatalf("prompts = %q", prompts)
			}
			payloads := fake.schemaPayloads()
			if len(payloads) != 1 || !json.Valid(payloads[0]) {
				t.Fatalf("schema forwarded = %q", payloads)
			}
		})
	}
}

func TestGuidedOutputRejectsUnsupportedSchemasBeforeTheRunner(t *testing.T) {
	deepArrays := func(count int) string {
		schema := `{"type":"string"}`
		for index := 0; index < count; index++ {
			schema = `{"type":"array","items":` + schema + `}`
		}
		return `{"type":"object","additionalProperties":false,"properties":{"p":` + schema + `}}`
	}
	largeEnum := `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string","enum":["` +
		strings.Repeat("a", 65*1024) + `"]}}}`
	cases := []struct {
		name   string
		schema string
		wantIn string
	}{
		{"unknown keyword", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string","pattern":"x"}}}`, "/properties/p/pattern"},
		{"duplicate key", `{"type":"object","additionalProperties":false,"type":"array"}`, "schema at /type: schema must not repeat an object key"},
		{"root not object", `"not a schema"`, "root schema must be an object"},
		{"non-ascii name", `{"type":"object","additionalProperties":false,"properties":{"caf\u00e9":{"type":"string"}}}`, "property names must be printable ASCII"},
		{"deep array chain", deepArrays(20), "schema nesting exceeds depth 16"},
		{"larger than 64 KiB", largeEnum, "schema is larger than 64 KiB"},
	}
	for path := range guidedBodies(guidedTestSchema) {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				fake := newGuidedFake()
				res := post(t, NewUnauthenticated(fake).Handler(), path, guidedBodies(tc.schema)[path])
				if res.Code != http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
				}
				assertAPIError(t, res, "invalid_request_error", "invalid_schema")
				if !strings.Contains(res.Body.String(), tc.wantIn) {
					t.Fatalf("body=%s; want %q", res.Body.String(), tc.wantIn)
				}
				if schemaCalls, completeCalls, _ := fake.snapshot(); schemaCalls != 0 || completeCalls != 0 {
					t.Fatalf("runner calls schema=%d complete=%d; a rejected schema must not reach the runner", schemaCalls, completeCalls)
				}
			})
		}
	}
}

func TestGuidedSchemaValidationPrecedesCapacity(t *testing.T) {
	// Fill the only slot, then prove a rejected schema returns its own error
	// (not 429): validation happens before capacity is acquired.
	srv := NewUnauthenticated(newGuidedFake())
	srv.MaxConcurrency = 1
	srv.slots() <- struct{}{}
	defer func() { <-srv.slots() }()

	res := post(t, srv.Handler(), "/v1/responses", guidedBodies(`{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string","pattern":"x"}}}`)["/v1/responses"])
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s; want 400 from schema validation", res.Code, res.Body.String())
	}
	assertAPIError(t, res, "invalid_request_error", "invalid_schema")
}

func TestGuidedOutputRefusesEveryNonLocalRoute(t *testing.T) {
	for path := range guidedBodies(guidedTestSchema) {
		for _, model := range []string{`"cloud"`, `"cloud-pro"`, `"on-device"`, `"chatgpt"`, `"auto"`, `""`} {
			t.Run(path+"/"+model, func(t *testing.T) {
				fake := newGuidedFake()
				body := guidedBodies(guidedTestSchema)[path]
				if model == "" {
					body = strings.Replace(body, `"model":"local",`, "", 1)
				} else {
					body = strings.Replace(body, `"model":"local"`, `"model":`+model, 1)
				}
				res := post(t, NewUnauthenticated(fake).Handler(), path, body)
				if res.Code != http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
				}
				assertAPIError(t, res, "invalid_request_error", "unsupported_parameter")
				if !strings.Contains(res.Body.String(), "requires model") {
					t.Fatalf("body=%s; want the local-only refusal", res.Body.String())
				}
				if schemaCalls, completeCalls, _ := fake.snapshot(); schemaCalls != 0 || completeCalls != 0 {
					t.Fatalf("runner calls schema=%d complete=%d; a non-local route must not fall back", schemaCalls, completeCalls)
				}
			})
		}
		// An unknown model is still an unsupported model, not a fallback.
		fake := newGuidedFake()
		body := strings.Replace(guidedBodies(guidedTestSchema)[path], `"model":"local"`, `"model":"bogus"`, 1)
		res := post(t, NewUnauthenticated(fake).Handler(), path, body)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "unsupported model") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		if schemaCalls, _, _ := fake.snapshot(); schemaCalls != 0 {
			t.Fatalf("runner called %d times for an unknown model", schemaCalls)
		}
	}
}

func TestGuidedOutputRefusesStreamAndImages(t *testing.T) {
	chat := guidedBodies(guidedTestSchema)["/v1/chat/completions"]
	responses := guidedBodies(guidedTestSchema)["/v1/responses"]
	cases := []struct{ path, body, want string }{
		{"/v1/chat/completions", strings.Replace(chat, `"messages":`, `"stream":true,"messages":`, 1), "complete-only"},
		{"/v1/responses", strings.Replace(responses, `"input":"hi"`, `"stream":true,"input":"hi"`, 1), "complete-only"},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}],"response_format":{"type":"json_schema","json_schema":{"name":"p","schema":` + guidedTestSchema + `}}}`, "image input"},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"hi"}],"image_generation":{},"response_format":{"type":"json_schema","json_schema":{"name":"p","schema":` + guidedTestSchema + `}}}`, "image generation"},
		{"/v1/responses", `{"model":"local","input":"hi","image_generation":{},"text":{"format":{"type":"json_schema","name":"p","schema":` + guidedTestSchema + `}}}`, "image generation"},
	}
	for _, tc := range cases {
		t.Run(tc.path+"/"+tc.want, func(t *testing.T) {
			fake := newGuidedFake()
			res := post(t, NewUnauthenticated(fake).Handler(), tc.path, tc.body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "invalid_request_error", "unsupported_parameter")
			if !strings.Contains(res.Body.String(), tc.want) {
				t.Fatalf("body=%s; want %q", res.Body.String(), tc.want)
			}
			if schemaCalls, completeCalls, _ := fake.snapshot(); schemaCalls != 0 || completeCalls != 0 {
				t.Fatalf("runner calls schema=%d complete=%d", schemaCalls, completeCalls)
			}
		})
	}
}

func TestGuidedResponseFormatShapeErrors(t *testing.T) {
	chat := func(format string) string {
		return `{"model":"local","messages":[{"role":"user","content":"hi"}],"response_format":` + format + `}`
	}
	responses := func(text string) string {
		return `{"model":"local","input":"hi","text":` + text + `}`
	}
	inner := func(fields string) string {
		return `{"type":"json_schema","json_schema":{` + fields + `}}`
	}
	cases := []struct {
		name string
		path string
		body string
		code string
	}{
		{"format is a string", "/v1/chat/completions", chat(`"json_schema"`), "invalid_response_format"},
		{"format is null", "/v1/chat/completions", chat(`null`), "invalid_response_format"},
		{"format missing type", "/v1/chat/completions", chat(`{}`), "invalid_response_format"},
		{"format type not a string", "/v1/chat/completions", chat(`{"type":5}`), "invalid_response_format"},
		{"json_object type", "/v1/chat/completions", chat(`{"type":"json_object"}`), "unsupported_parameter"},
		{"unknown type", "/v1/chat/completions", chat(`{"type":"xml"}`), "unsupported_parameter"},
		{"text type with json_schema", "/v1/chat/completions", chat(`{"type":"text","json_schema":{"name":"p","schema":` + guidedTestSchema + `}}`), "unsupported_parameter"},
		{"json_schema missing", "/v1/chat/completions", chat(`{"type":"json_schema"}`), "invalid_response_format"},
		{"json_schema null", "/v1/chat/completions", chat(`{"type":"json_schema","json_schema":null}`), "invalid_response_format"},
		{"json_schema not an object", "/v1/chat/completions", chat(`{"type":"json_schema","json_schema":[]}`), "invalid_response_format"},
		{"name missing", "/v1/chat/completions", chat(inner(`"schema":` + guidedTestSchema)), "invalid_response_format"},
		{"name empty", "/v1/chat/completions", chat(inner(`"name":"","schema":` + guidedTestSchema)), "invalid_response_format"},
		{"name wrong type", "/v1/chat/completions", chat(inner(`"name":5,"schema":` + guidedTestSchema)), "invalid_response_format"},
		{"strict wrong type", "/v1/chat/completions", chat(inner(`"name":"p","strict":"yes","schema":` + guidedTestSchema)), "invalid_response_format"},
		{"schema missing", "/v1/chat/completions", chat(inner(`"name":"p"`)), "invalid_response_format"},
		{"schema null", "/v1/chat/completions", chat(inner(`"name":"p","schema":null`)), "invalid_response_format"},
		{"unknown field in json_schema", "/v1/chat/completions", chat(inner(`"name":"p","schema":` + guidedTestSchema + `,"extra":true`)), "unsupported_parameter"},
		{"unknown field in response_format", "/v1/chat/completions", chat(`{"type":"json_schema","json_schema":{"name":"p","schema":` + guidedTestSchema + `},"extra":true}`), "unsupported_parameter"},

		{"text is a string", "/v1/responses", responses(`"json_schema"`), "invalid_response_format"},
		{"text is null", "/v1/responses", responses(`null`), "invalid_response_format"},
		{"format missing type", "/v1/responses", responses(`{"format":{}}`), "invalid_response_format"},
		{"json_object type", "/v1/responses", responses(`{"format":{"type":"json_object"}}`), "unsupported_parameter"},
		{"unknown type", "/v1/responses", responses(`{"format":{"type":"xml"}}`), "unsupported_parameter"},
		{"name missing", "/v1/responses", responses(`{"format":{"type":"json_schema","schema":` + guidedTestSchema + `}}`), "invalid_response_format"},
		{"name empty", "/v1/responses", responses(`{"format":{"type":"json_schema","name":"","schema":` + guidedTestSchema + `}}`), "invalid_response_format"},
		{"schema missing", "/v1/responses", responses(`{"format":{"type":"json_schema","name":"p"}}`), "invalid_response_format"},
		{"schema null", "/v1/responses", responses(`{"format":{"type":"json_schema","name":"p","schema":null}}`), "invalid_response_format"},
		{"strict wrong type", "/v1/responses", responses(`{"format":{"type":"json_schema","name":"p","strict":1,"schema":` + guidedTestSchema + `}}`), "invalid_response_format"},
		{"unknown field in format", "/v1/responses", responses(`{"format":{"type":"json_schema","name":"p","schema":` + guidedTestSchema + `,"extra":true}}`), "unsupported_parameter"},
		{"unknown field in text", "/v1/responses", responses(`{"verbosity":"low"}`), "unsupported_parameter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newGuidedFake()
			res := post(t, NewUnauthenticated(fake).Handler(), tc.path, tc.body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "invalid_request_error", tc.code)
			if schemaCalls, completeCalls, _ := fake.snapshot(); schemaCalls != 0 || completeCalls != 0 {
				t.Fatalf("runner calls schema=%d complete=%d", schemaCalls, completeCalls)
			}
		})
	}
}

func TestGuidedLabelsAreAcceptedAndDoNotLoosenValidation(t *testing.T) {
	for path := range guidedBodies(guidedTestSchema) {
		t.Run(path, func(t *testing.T) {
			// strict:false and description are accepted labels. The subset is
			// still enforced, so a non-conforming object is refused.
			body := guidedBodies(guidedTestSchema)[path]
			body = strings.Replace(body, `"name":"person"`, `"name":"person","description":"a person","strict":false`, 1)
			fake := newGuidedFake()
			res := post(t, NewUnauthenticated(fake).Handler(), path, body)
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}

			fake = newGuidedFake()
			fake.result.Structured = json.RawMessage(`{"name":1}`)
			res = post(t, NewUnauthenticated(fake).Handler(), path, body)
			if res.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s; strict:false must not loosen validation", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "server_error", "native_protocol")
		})
	}
}

func TestGuidedOutputRefusesOldHelperBeforeSpawning(t *testing.T) {
	for path := range guidedBodies(guidedTestSchema) {
		t.Run(path, func(t *testing.T) {
			fake := newGuidedFake()
			fake.err = runner.GuidedOutputRefusal()
			res := post(t, NewUnauthenticated(fake).Handler(), path, guidedBodies(guidedTestSchema)[path])
			if res.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "server_error", "native_protocol")
		})
	}
}

func TestGuidedOutputRejectsNonConformingReturnedPayload(t *testing.T) {
	cases := []struct {
		name       string
		structured string
	}{
		{"wrong type", `{"name":1}`},
		{"missing required", `{"age":36}`},
		{"extra property", `{"name":"Ada","age":36,"extra":true}`},
		{"duplicate key", `{"name":"Ada","name":"Bob"}`},
		{"empty object", `{}`},
	}
	for path := range guidedBodies(guidedTestSchema) {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				fake := newGuidedFake()
				fake.result.Structured = json.RawMessage(tc.structured)
				res := post(t, NewUnauthenticated(fake).Handler(), path, guidedBodies(guidedTestSchema)[path])
				if res.Code != http.StatusBadGateway {
					t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
				}
				assertAPIError(t, res, "server_error", "native_protocol")
				for _, forbidden := range []string{"choice", "output_text", `"content"`, "structured"} {
					if strings.Contains(res.Body.String(), forbidden) {
						t.Fatalf("error response leaked %q: %s", forbidden, res.Body.String())
					}
				}
			})
		}
	}
	// A runner that reports success with no structured object at all is a
	// protocol failure, not a success with empty text.
	for path := range guidedBodies(guidedTestSchema) {
		fake := newGuidedFake()
		fake.result.Structured = nil
		res := post(t, NewUnauthenticated(fake).Handler(), path, guidedBodies(guidedTestSchema)[path])
		if res.Code != http.StatusBadGateway {
			t.Fatalf("%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
}

func TestGuidedOutputPromptAndBodyLimits(t *testing.T) {
	for path := range guidedBodies(guidedTestSchema) {
		t.Run(path, func(t *testing.T) {
			fake := newGuidedFake()
			var huge string
			if path == "/v1/chat/completions" {
				huge = `{"model":"local","messages":[{"role":"user","content":"` + strings.Repeat("x", MaxPromptBytes) + `"}],"response_format":{"type":"json_schema","json_schema":{"name":"p","schema":` + guidedTestSchema + `}}}`
			} else {
				huge = `{"model":"local","input":"` + strings.Repeat("x", MaxPromptBytes) + `","text":{"format":{"type":"json_schema","name":"p","schema":` + guidedTestSchema + `}}}`
			}
			res := post(t, NewUnauthenticated(fake).Handler(), path, huge)
			if res.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("prompt limit status=%d body=%s", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "invalid_request_error", "context_too_large")
			if schemaCalls, completeCalls, _ := fake.snapshot(); schemaCalls != 0 || completeCalls != 0 {
				t.Fatalf("runner calls schema=%d complete=%d", schemaCalls, completeCalls)
			}

			oversized := guidedBodies(guidedTestSchema)[path] + strings.Repeat(" ", MaxRequestBytes+1)
			res = post(t, NewUnauthenticated(fake).Handler(), path, oversized)
			if res.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("body limit status=%d", res.Code)
			}
			assertAPIError(t, res, "invalid_request_error", "context_too_large")
		})
	}
}

func TestTextFormatKeepsPlainTextBehavior(t *testing.T) {
	cases := map[string]string{
		"/v1/chat/completions": `{"model":"local","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"text"}}`,
		"/v1/responses":        `{"model":"local","input":"hi","text":{"format":{"type":"text"}}}`,
	}
	for path, body := range cases {
		t.Run(path, func(t *testing.T) {
			fake := newGuidedFake()
			res := post(t, NewUnauthenticated(fake).Handler(), path, body)
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			schemaCalls, completeCalls, _ := fake.snapshot()
			if schemaCalls != 0 || completeCalls != 1 {
				t.Fatalf("schema calls=%d complete calls=%d; text format must use the plain path", schemaCalls, completeCalls)
			}
		})
	}
	// An absent text member on Responses is today's behavior too.
	fake := newGuidedFake()
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/responses", `{"model":"local","input":"hi","text":{}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if schemaCalls, completeCalls, _ := fake.snapshot(); schemaCalls != 0 || completeCalls != 1 {
		t.Fatalf("schema calls=%d complete calls=%d", schemaCalls, completeCalls)
	}
}

func TestGuidedOutputHonorsTheAvailabilityMap(t *testing.T) {
	for path := range guidedBodies(guidedTestSchema) {
		t.Run(path, func(t *testing.T) {
			off := newGuidedFake()
			srv := NewUnauthenticated(off)
			srv.Available = map[string]bool{"local": false}
			res := post(t, srv.Handler(), path, guidedBodies(guidedTestSchema)[path])
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "invalid_request_error", "model_unavailable")
			if schemaCalls, completeCalls, _ := off.snapshot(); schemaCalls != 0 || completeCalls != 0 {
				t.Fatalf("runner calls schema=%d complete=%d", schemaCalls, completeCalls)
			}

			on := newGuidedFake()
			srv = NewUnauthenticated(on)
			srv.Available = map[string]bool{"local": true}
			res = post(t, srv.Handler(), path, guidedBodies(guidedTestSchema)[path])
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}

func TestGuidedOutputWithoutSchemaRunnerFailsClosed(t *testing.T) {
	// A runner with no schema interface must not silently fall back to text.
	plain := &echoRunner{}
	for path := range guidedBodies(guidedTestSchema) {
		res := post(t, NewUnauthenticated(plain).Handler(), path, guidedBodies(guidedTestSchema)[path])
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status=%d body=%s", path, res.Code, res.Body.String())
		}
		assertAPIError(t, res, "server_error", "model_unavailable")
	}
}

// schemaOnlyRunner implements only the interfaces guided output needs. It must
// work even though it cannot serve a plain local completion.
type schemaOnlyRunner struct{ guidedFake }

func (f *schemaOnlyRunner) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	panic("never called")
}

func TestGuidedOutputNeedsOnlyTheSchemaInterface(t *testing.T) {
	for path := range guidedBodies(guidedTestSchema) {
		t.Run(path, func(t *testing.T) {
			fake := &schemaOnlyRunner{}
			fake.result = runner.Completion{
				Usage:      &runner.Usage{InputTokens: 1, OutputTokens: 2},
				Structured: json.RawMessage(guidedTestPayload),
			}
			res := post(t, NewUnauthenticated(fake).Handler(), path, guidedBodies(guidedTestSchema)[path])
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if fake.schemaCalls != 1 {
				t.Fatalf("schema calls = %d", fake.schemaCalls)
			}
		})
	}
}
