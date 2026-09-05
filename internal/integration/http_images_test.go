// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/server"
)

type httpImageCall struct {
	model   runner.Model
	prompt  string
	digests [][32]byte
	paths   []string
}

type httpImageRunner struct {
	mu         sync.Mutex
	textCalls  int
	imageCalls []httpImageCall
}

func (r *httpImageRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.mu.Lock()
	r.textCalls++
	r.mu.Unlock()
	return "text:" + prompt, model, nil
}

func (r *httpImageRunner) RunWithImages(_ context.Context, model runner.Model, prompt string, paths []string) (string, runner.Model, error) {
	call := httpImageCall{model: model, prompt: prompt, paths: append([]string(nil), paths...)}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", model, err
		}
		call.digests = append(call.digests, sha256.Sum256(data))
	}
	r.mu.Lock()
	r.imageCalls = append(r.imageCalls, call)
	r.mu.Unlock()
	return "provider-free image answer", model, nil
}

func TestHTTPImageEndpointsProviderFree(t *testing.T) {
	imageBytes := integrationPNG(t)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	token := "integration-token"
	fake := &httpImageRunner{}
	handler := server.New(fake, token).Handler()

	chatBody := fmt.Sprintf(`{"model":"cloud","messages":[{"role":"system","content":"Use plain words"},{"role":"user","content":[{"type":"text","text":"Describe this"},{"type":"image_url","image_url":{"url":%q}}]}]}`, dataURL)
	unauthorized := integrationPost(handler, "/v1/chat/completions", chatBody, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	chat := integrationPost(handler, "/v1/chat/completions", chatBody, token)
	if chat.Code != http.StatusOK || !strings.Contains(chat.Body.String(), `"object":"chat.completion"`) || !strings.Contains(chat.Body.String(), `"content":"provider-free image answer"`) {
		t.Fatalf("chat status=%d body=%s", chat.Code, chat.Body.String())
	}
	responsesBody := fmt.Sprintf(`{"model":"cloud","instructions":"Use plain words","input":[{"role":"user","content":[{"type":"input_text","text":"Describe this"},{"type":"input_image","image_url":%q}]}]}`, dataURL)
	responses := integrationPost(handler, "/v1/responses", responsesBody, token)
	if responses.Code != http.StatusOK || !strings.Contains(responses.Body.String(), `"object":"response"`) || !strings.Contains(responses.Body.String(), `"text":"provider-free image answer"`) {
		t.Fatalf("responses status=%d body=%s", responses.Code, responses.Body.String())
	}

	fake.mu.Lock()
	textCalls := fake.textCalls
	calls := append([]httpImageCall(nil), fake.imageCalls...)
	fake.mu.Unlock()
	if textCalls != 0 || len(calls) != 2 {
		t.Fatalf("calls text=%d image=%d", textCalls, len(calls))
	}
	wantDigest := sha256.Sum256(imageBytes)
	for i, call := range calls {
		if call.model != runner.ModelCloud || len(call.digests) != 1 || call.digests[0] != wantDigest {
			t.Fatalf("call %d model=%q digests=%x", i, call.model, call.digests)
		}
		for _, want := range []string{"SYSTEM:\nUse plain words", "USER:\nDescribe this"} {
			if !strings.Contains(call.prompt, want) {
				t.Fatalf("call %d prompt missing %q: %q", i, want, call.prompt)
			}
		}
		for _, path := range call.paths {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("call %d staged path remains: %s (%v)", i, path, err)
			}
		}
	}
}

func TestHTTPTextRequestsKeepUsingRunnerRun(t *testing.T) {
	fake := &httpImageRunner{}
	handler := server.New(fake, "").Handler()
	res := integrationPost(handler, "/v1/responses", `{"model":"cloud","input":"plain text"}`, "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "text:") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.textCalls != 1 || len(fake.imageCalls) != 0 {
		t.Fatalf("calls text=%d image=%d", fake.textCalls, len(fake.imageCalls))
	}
}

func integrationPost(handler http.Handler, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func integrationPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 0x24, G: 0x68, B: 0xac, A: 0xff})
	img.Set(1, 0, color.RGBA{A: 0xff})
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
