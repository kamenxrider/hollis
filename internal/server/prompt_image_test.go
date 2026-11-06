// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
)

type adjustedImageRecorder struct {
	nativeImageRecorder
	prompt string
}

func (r *adjustedImageRecorder) RunCompleteWithImages(ctx context.Context, model runner.Model, prompt string, paths []string) (runner.Completion, error) {
	r.prompt = prompt
	return r.nativeImageRecorder.RunCompleteWithImages(ctx, model, prompt, paths)
}
func (r *adjustedImageRecorder) StreamWithImages(ctx context.Context, model runner.Model, prompt string, paths []string, emit func(string) error) (runner.Completion, error) {
	result, err := r.RunCompleteWithImages(ctx, model, prompt, paths)
	result.Usage = nil
	if err == nil {
		err = emit(result.Text)
	}
	return result, err
}
func TestHTTPImagePromptAdjustment(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, keep := range []bool{false, true} {
			for _, streaming := range []bool{false, true} {
				r := &adjustedImageRecorder{}
				s := NewUnauthenticated(r)
				s.stageImages = func(context.Context, []encodedImage) (stagedImages, error) {
					return stagedImages{Paths: []string{"/private/staged.png"}}, nil
				}
				var body string
				if endpoint == "/v1/chat/completions" {
					body = `{"model":"local","messages":[{"role":"user","content":[{"type":"text","text":"Describe these images."},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`
				} else {
					body = `{"model":"local","input":[{"role":"user","content":[{"type":"input_text","text":"Describe these images."},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`
				}
				if streaming {
					body = strings.Replace(body, `"model":"local"`, `"model":"local","stream":true`, 1)
				}
				req := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				if keep {
					req.Header.Set("X-Hollis-Keep-Prompt", "1")
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, req)
				want := "Describe this image."
				if keep {
					want = "Describe these images."
				}
				if w.Code != 200 || r.prompt != chat.RenderTranscript(nil, want) || (w.Header().Get("X-Hollis-Prompt-Adjusted") == "true") == keep || (!streaming && strings.Contains(w.Body.String(), `"prompt_adjusted":true`) == keep) {
					t.Fatalf("keep=%v prompt=%q status=%d header=%v body=%s", keep, r.prompt, w.Code, w.Header(), w.Body.String())
				}
			}
		}
	}
}
