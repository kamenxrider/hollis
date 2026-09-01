// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// echoRunner returns the transcript it received so tests can assert the
// exact prompt sent to the transport.
type echoRunner struct {
	err error
}

func (f *echoRunner) Run(_ context.Context, _ runner.Model, prompt string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return prompt, nil
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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
