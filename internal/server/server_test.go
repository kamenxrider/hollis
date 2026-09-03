// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

// echoRunner returns the transcript it received so tests can assert the
// exact prompt sent to the transport.
type echoRunner struct {
	err error
}

func (f *echoRunner) Run(_ context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	if f.err != nil {
		return "", m, f.err
	}
	return prompt, m, nil
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func TestHealth(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := do(t, srv.Handler(), http.MethodGet, "/health", "")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"ok"`) {
		t.Fatalf("health body: %s", res.Body.String())
	}
}

func TestModelsListsAllTiers(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := do(t, srv.Handler(), http.MethodGet, "/v1/models", "")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	var got struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("models JSON: %v", err)
	}
	ownedBy := map[string]string{}
	for _, m := range got.Data {
		ownedBy[m.ID] = m.OwnedBy
	}
	for _, want := range []string{"auto", "cloud", "cloud-pro", "on-device", "chatgpt"} {
		if _, ok := ownedBy[want]; !ok {
			t.Fatalf("models missing %q: %v", want, ownedBy)
		}
	}
	// owned_by must be honest per tier (ChatGPT is OpenAI's model).
	wantOwner := map[string]string{
		"auto": "hollis", "cloud": "Apple", "cloud-pro": "Apple",
		"on-device": "Apple", "chatgpt": "OpenAI",
	}
	for id, owner := range wantOwner {
		if ownedBy[id] != owner {
			t.Fatalf("model %q owned_by = %q, want %q", id, ownedBy[id], owner)
		}
	}
}

func TestChatCompletionsShapeAndTranscript(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"cloud","messages":[{"role":"system","content":"Be brief"},{"role":"user","content":"Hi there"}]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body.String())
	}
	var got struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("JSON: %v (%s)", err, res.Body.String())
	}
	if !strings.HasPrefix(got.ID, "chatcmpl-") || got.Object != "chat.completion" || got.Model != "cloud" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if len(got.Choices) != 1 || got.Choices[0].Message.Role != "assistant" || got.Choices[0].FinishReason != "stop" {
		t.Fatalf("unexpected choices: %+v", got.Choices)
	}
	// The echo runner returned the transcript: it must be the tested
	// replay format with the system message and the final user turn.
	content := got.Choices[0].Message.Content
	for _, want := range []string{"You are continuing an existing conversation.", "SYSTEM:\nBe brief", "USER:\nHi there"} {
		if !strings.Contains(content, want) {
			t.Fatalf("transcript missing %q: %q", want, content)
		}
	}
}

func TestChatCompletionsStreamUnsupported(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"cloud","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	if !strings.Contains(res.Body.String(), "streaming is not supported") {
		t.Fatalf("body: %s", res.Body.String())
	}
}

func TestChatCompletionsLastMessageMustBeUser(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"cloud","messages":[{"role":"assistant","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
}

type callCountingRunner struct {
	calls int
}

func (r *callCountingRunner) Run(_ context.Context, model runner.Model, _ string) (string, runner.Model, error) {
	r.calls++
	return "unexpected", model, nil
}

func TestChatCompletionsRejectsUnknownNestedMessageFieldsBeforeRunner(t *testing.T) {
	r := &callCountingRunner{}
	srv := New(r, "")
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"cloud","messages":[{"role":"user","content":"hi","name":"ignored"}]}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"unsupported_parameter"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if r.calls != 0 {
		t.Fatalf("runner called %d times", r.calls)
	}
}

func TestModelsOmitUnavailableTiers(t *testing.T) {
	// the catalog is what resolves.
	// A 26 machine (no Pro bridge) lists auto + its three tiers only.
	srv := New(&echoRunner{}, "")
	srv.Available = map[string]bool{"cloud": true, "on-device": true, "chatgpt": true, "cloud-pro": false}
	res := do(t, srv.Handler(), http.MethodGet, "/v1/models", "")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	if strings.Contains(res.Body.String(), "cloud-pro") {
		t.Fatalf("unavailable tier surfaced: %s", res.Body.String())
	}
	for _, want := range []string{"auto", "\"cloud\"", "on-device", "chatgpt"} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("models missing %s: %s", want, res.Body.String())
		}
	}
}

func TestChatCompletionsUnavailableModel(t *testing.T) {
	srv := New(&echoRunner{}, "")
	srv.Available = map[string]bool{"cloud": true, "on-device": true, "chatgpt": true, "cloud-pro": false}
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"cloud-pro","messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"code":"model_unavailable"`) {
		t.Fatalf("body: %s", res.Body.String())
	}
}

func TestChatCompletionsAutoNotGated(t *testing.T) {
	// auto has no bridge of its own; either discovered constituent makes it viable.
	srv := New(&echoRunner{}, "")
	srv.Available = map[string]bool{"cloud": true, "on-device": true, "chatgpt": true, "cloud-pro": false}
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body.String())
	}
}

func TestAutoUnavailableWhenNeitherConstituentResolved(t *testing.T) {
	srv := New(&echoRunner{}, "")
	srv.Available = map[string]bool{"cloud": false, "on-device": false, "chatgpt": true, "cloud-pro": false}
	models := do(t, srv.Handler(), http.MethodGet, "/v1/models", "")
	if strings.Contains(models.Body.String(), `"id":"auto"`) {
		t.Fatalf("unavailable auto surfaced: %s", models.Body.String())
	}
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"model_unavailable"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestChatCompletionsUnknownModel(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
}

func TestResponsesStringInput(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := post(t, srv.Handler(), "/v1/responses",
		`{"model":"cloud","input":"Reply with RESP-OK"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body.String())
	}
	var got struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("JSON: %v (%s)", err, res.Body.String())
	}
	if !strings.HasPrefix(got.ID, "resp_") || got.Object != "response" || got.Status != "completed" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if len(got.Output) != 1 || got.Output[0].Type != "message" || got.Output[0].Role != "assistant" {
		t.Fatalf("unexpected output: %+v", got.Output)
	}
	if len(got.Output[0].Content) != 1 || got.Output[0].Content[0].Type != "output_text" {
		t.Fatalf("unexpected content: %+v", got.Output[0].Content)
	}
	// Echo runner: text is the transcript; single string input means the
	// final user turn is the input itself.
	if !strings.Contains(got.Output[0].Content[0].Text, "USER:\nReply with RESP-OK") {
		t.Fatalf("transcript missing input: %q", got.Output[0].Content[0].Text)
	}
}

func TestResponsesArrayInputWithInstructions(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := post(t, srv.Handler(), "/v1/responses", `{
		"model": "cloud",
		"instructions": "Always answer in one word",
		"input": [
			{"role": "developer", "content": "legacy system prompt"},
			{"role": "user", "content": [{"type": "input_text", "text": "What is 2+2?"}]},
			{"role": "assistant", "content": [{"type": "output_text", "text": "Four"}]},
			{"role": "user", "content": "And 3+3?"}
		]
	}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body.String())
	}
	var got struct {
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	text := got.Output[0].Content[0].Text
	for _, want := range []string{
		"SYSTEM:\nAlways answer in one word",
		"SYSTEM:\nlegacy system prompt", // developer normalizes to system
		"USER:\nWhat is 2+2?",
		"ASSISTANT:\nFour",
		"USER:\nAnd 3+3?",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("transcript missing %q: %q", want, text)
		}
	}
}

func TestResponsesStreamUnsupported(t *testing.T) {
	srv := New(&echoRunner{}, "")
	res := post(t, srv.Handler(), "/v1/responses",
		`{"model":"cloud","input":"hi","stream":true}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	if !strings.Contains(res.Body.String(), "streaming is not supported") {
		t.Fatalf("body: %s", res.Body.String())
	}
}

func TestRunnerTimeoutMapsTo504(t *testing.T) {
	srv := New(&echoRunner{err: &runner.Error{Kind: runner.KindTimeout, ExitCode: -1}}, "")
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"cloud","messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", res.Code)
	}
}

func TestRunnerFailureMapsTo502(t *testing.T) {
	srv := New(&echoRunner{err: &runner.Error{Kind: runner.KindTransport, ExitCode: 1}}, "")
	res := post(t, srv.Handler(), "/v1/chat/completions",
		`{"model":"cloud","messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", res.Code)
	}
}

func TestBearerAuthOnV1Endpoints(t *testing.T) {
	srv := New(&echoRunner{}, "secret")
	h := srv.Handler()

	// Health stays open.
	if res := do(t, h, http.MethodGet, "/health", ""); res.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", res.Code)
	}
	// /v1/models without the header → 401.
	res := do(t, h, http.MethodGet, "/v1/models", "")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
	// With the header → through.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
}

func TestNoUsageFieldsEver(t *testing.T) {
	// Plan principle 5: never invent token counts. The success payloads
	// must not contain a usage object.
	srv := New(&echoRunner{}, "")
	for _, tc := range [][]string{
		{"/v1/chat/completions", `{"model":"cloud","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/responses", `{"model":"cloud","input":"hi"}`},
	} {
		res := post(t, srv.Handler(), tc[0], tc[1])
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d", tc[0], res.Code)
		}
		if strings.Contains(res.Body.String(), "usage") || strings.Contains(res.Body.String(), "tokens") {
			t.Fatalf("%s invented metadata: %s", tc[0], res.Body.String())
		}
	}
}

func TestEndpointMethodsAndAllowHeaders(t *testing.T) {
	handler := New(&echoRunner{}, "").Handler()
	for _, tc := range []struct {
		path, wrong, allow string
	}{
		{"/health", http.MethodPost, http.MethodGet},
		{"/v1/models", http.MethodPost, http.MethodGet},
		{"/v1/chat/completions", http.MethodGet, http.MethodPost},
		{"/v1/responses", http.MethodGet, http.MethodPost},
	} {
		res := do(t, handler, tc.wrong, tc.path, "")
		if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != tc.allow {
			t.Fatalf("%s %s: status=%d Allow=%q", tc.wrong, tc.path, res.Code, res.Header().Get("Allow"))
		}
		assertAPIError(t, res, "invalid_request_error", "invalid_method")
	}
}

func TestStrictJSONAndContentType(t *testing.T) {
	handler := New(&echoRunner{}, "").Handler()
	for _, tc := range []struct {
		name, body, code string
	}{
		{"malformed", `{`, "invalid_json"},
		{"trailing", `{"input":"hi"} {}`, "invalid_json"},
		{"unknown field", `{"input":"hi","temperature":0}`, "unsupported_parameter"},
		{"unknown nested field", `{"input":[{"role":"user","content":[{"type":"input_text","text":"hi","image":"no"}]}]}`, "unsupported_parameter"},
		{"unknown message field", `{"input":[{"role":"user","content":"hi","name":"no"}]}`, "unsupported_parameter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := post(t, handler, "/v1/responses", tc.body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "invalid_request_error", tc.code)
		})
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d want 415", res.Code)
	}
	assertAPIError(t, res, "invalid_request_error", "unsupported_media_type")
}

func TestInputValidationHappensBeforeRunner(t *testing.T) {
	counting := &countRunner{}
	handler := New(counting, "").Handler()
	for _, body := range []string{
		`{"input":null}`,
		`{"input":""}`,
		`{"input":[{"role":"tool","content":"x"}]}`,
		`{"input":[{"role":"assistant","content":"x"}]}`,
		`{"input":[{"role":"user","content":null}]}`,
	} {
		res := post(t, handler, "/v1/responses", body)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, res.Code, res.Body.String())
		}
	}
	res := post(t, handler, "/v1/responses", `{"input":"`+strings.Repeat("x", MaxPromptBytes)+`"}`)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized prompt status=%d body=%s", res.Code, res.Body.String())
	}
	assertAPIError(t, res, "invalid_request_error", "context_too_large")
	if counting.calls != 0 {
		t.Fatalf("runner called %d times for rejected requests", counting.calls)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	handler := New(&echoRunner{}, "").Handler()
	for _, body := range []string{
		strings.Repeat(" ", MaxRequestBytes+1),
		`{"input":"hi"}` + strings.Repeat(" ", MaxRequestBytes+1),
	} {
		res := post(t, handler, "/v1/responses", body)
		if res.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		assertAPIError(t, res, "invalid_request_error", "context_too_large")
	}
}

func TestAvailabilityMapIsFailClosed(t *testing.T) {
	srv := New(&echoRunner{}, "")
	srv.Available = map[string]bool{"cloud": true}
	models := do(t, srv.Handler(), http.MethodGet, "/v1/models", "")
	if strings.Contains(models.Body.String(), "cloud-pro") || strings.Contains(models.Body.String(), "on-device") || strings.Contains(models.Body.String(), "chatgpt") {
		t.Fatalf("absent availability entries surfaced: %s", models.Body.String())
	}
	if !strings.Contains(models.Body.String(), `"auto"`) || !strings.Contains(models.Body.String(), `"cloud"`) {
		t.Fatalf("available entries missing: %s", models.Body.String())
	}
}

func TestRemoteErrorsAreStableAndSanitized(t *testing.T) {
	secret := "private Apple stderr and prompt"
	srv := New(&echoRunner{err: &runner.Error{Kind: runner.KindRateLimited, ExitCode: 1, Stderr: secret, Err: errors.New(secret)}}, "")
	res := post(t, srv.Handler(), "/v1/responses", `{"input":"quiet"}`)
	if res.Code != http.StatusTooManyRequests || res.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d retry=%q", res.Code, res.Header().Get("Retry-After"))
	}
	assertAPIError(t, res, "rate_limit_error", "rate_limited")
	if strings.Contains(res.Body.String(), secret) {
		t.Fatalf("raw runner error leaked: %s", res.Body.String())
	}
}

func TestConcurrencyLimitRejectsImmediatelyAndHealthStaysOpen(t *testing.T) {
	blocking := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	srv := New(blocking, "")
	srv.MaxConcurrency = 1
	handler := srv.Handler()
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- post(t, handler, "/v1/responses", `{"input":"first"}`) }()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("first runner did not start")
	}

	second := post(t, handler, "/v1/responses", `{"input":"second"}`)
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") != "1" {
		t.Fatalf("second status=%d retry=%q", second.Code, second.Header().Get("Retry-After"))
	}
	assertAPIError(t, second, "rate_limit_error", "server_busy")
	health := do(t, handler, http.MethodGet, "/health", "")
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d while model busy", health.Code)
	}
	close(blocking.release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
}

func TestAuthenticationErrorShape(t *testing.T) {
	res := do(t, New(&echoRunner{}, strings.Repeat("x", 32)).Handler(), http.MethodGet, "/v1/models", "")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", res.Code)
	}
	assertAPIError(t, res, "authentication_error", "invalid_token")
}

type countRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *countRunner) Run(_ context.Context, model runner.Model, _ string) (string, runner.Model, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return "ok", model, nil
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingRunner) Run(_ context.Context, model runner.Model, _ string) (string, runner.Model, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return "ok", model, nil
}

func assertAPIError(t *testing.T, res *httptest.ResponseRecorder, errorType, code string) {
	t.Helper()
	var body struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("error JSON: %v (%s)", err, res.Body.String())
	}
	if body.Error.Type != errorType || body.Error.Code != code {
		t.Fatalf("error=%+v want type=%s code=%s", body.Error, errorType, code)
	}
}
