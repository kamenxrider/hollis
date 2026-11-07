// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type nativeImageRecorder struct {
	plainCalls, imageCalls int
	err                    error
}

func TestNativeImagesPublicEndpoints(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, streaming := range []bool{false, true} {
			for _, count := range []int{1, 2} {
				t.Run(fmt.Sprintf("%s/stream=%v/count=%d", endpoint, streaming, count), func(t *testing.T) {
					r := &nativeImageRecorder{}
					s := NewUnauthenticated(r)
					staged, cleaned := false, false
					s.stageImages = func(_ context.Context, images []encodedImage) (stagedImages, error) {
						if len(images) != 1 || images[0].DataURL != "data:image/png;base64,AAAA" {
							t.Fatal("image parts changed before staging")
						}
						staged = true
						return stagedImages{Paths: []string{"/private/staged.png"}, cleanup: func() { cleaned = true }}, nil
					}
					var body string
					if endpoint == "/v1/chat/completions" {
						image := `{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}`
						body = fmt.Sprintf(`{"model":"local","stream":%v,"messages":[{"role":"user","content":[{"type":"text","text":"Describe"},%s]}]}`, streaming, strings.TrimSuffix(strings.Repeat(image+",", count), ","))
					} else {
						image := `{"type":"input_image","image_url":"data:image/png;base64,AAAA"}`
						body = fmt.Sprintf(`{"model":"local","stream":%v,"input":[{"role":"user","content":[{"type":"input_text","text":"Describe"},%s]}]}`, streaming, strings.TrimSuffix(strings.Repeat(image+",", count), ","))
					}
					w := httptest.NewRecorder()
					req := loopbackRequest(http.MethodPost, endpoint, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					s.Handler().ServeHTTP(w, req)
					if count == 2 {
						if w.Code != 400 || staged || r.imageCalls != 0 || r.plainCalls != 0 ||
							!strings.Contains(w.Body.String(), "unsupported_parameter") {
							t.Fatalf("two-image refusal: status=%d staged=%v body=%s", w.Code, staged, w.Body)
						}
						return
					}
					if w.Code != 200 || r.imageCalls != 1 || r.plainCalls != 0 || !staged || !cleaned ||
						!strings.Contains(w.Body.String(), "red circle") {
						t.Fatalf("image admission: status=%d images=%d plain=%d body=%s", w.Code, r.imageCalls, r.plainCalls, w.Body)
					}
					if streaming && w.Header().Get("Content-Type") != "text/event-stream" {
						t.Fatal("image response did not stream")
					}
				})
			}
		}
	}
}

func (r *nativeImageRecorder) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	r.plainCalls++
	return "", runner.ModelLocal, errors.New("pixels must not be dropped")
}
func (r *nativeImageRecorder) RunComplete(context.Context, runner.Model, string) (runner.Completion, error) {
	r.plainCalls++
	return runner.Completion{}, errors.New("pixels must not be dropped")
}
func (r *nativeImageRecorder) Stream(context.Context, runner.Model, string, func(string) error) (runner.Completion, error) {
	r.plainCalls++
	return runner.Completion{}, errors.New("pixels must not be dropped")
}
func (r *nativeImageRecorder) RunCompleteWithImages(_ context.Context, model runner.Model, _ string, images []string) (runner.Completion, error) {
	r.imageCalls++
	if len(images) != 1 || images[0] != "/private/staged.png" {
		return runner.Completion{}, errors.New("missing staged image")
	}
	return runner.Completion{Text: "red circle", Model: model, Usage: &runner.Usage{InputTokens: 10, OutputTokens: 2}}, r.err
}
func (r *nativeImageRecorder) StreamWithImages(ctx context.Context, model runner.Model, prompt string, images []string, emit func(string) error) (runner.Completion, error) {
	result, err := r.RunCompleteWithImages(ctx, model, prompt, images)
	result.Usage = nil
	if err == nil {
		err = emit(result.Text)
	}
	return result, err
}

func TestNativePreparedImagesNeverFallBackToText(t *testing.T) {
	for _, mode := range []string{"complete", "chat-stream", "responses-stream"} {
		for _, fail := range []bool{false, true} {
			t.Run(mode+map[bool]string{true: "-failure"}[fail], func(t *testing.T) {
				r := &nativeImageRecorder{}
				if fail {
					r.err = &runner.Error{Kind: runner.KindInvalidImage, ExitCode: -1}
				}
				cleaned := false
				s := &Server{Runner: r, stageImages: func(context.Context, []encodedImage) (stagedImages, error) {
					return stagedImages{Paths: []string{"/private/staged.png"}, cleanup: func() { cleaned = true }}, nil
				}}
				request := preparedRequest{Prompt: "Describe", Images: []encodedImage{{DataURL: "fixture"}}}
				w := httptest.NewRecorder()
				if mode == "complete" {
					result, ok := s.completePrepared(context.Background(), w, runner.ModelLocal, request)
					if ok == fail || (ok && (result.Usage == nil || result.Text != "red circle")) {
						t.Fatalf("result=%+v ok=%v", result, ok)
					}
				} else {
					s.streamPrepared(w, loopbackRequest("POST", "/", nil), runner.ModelLocal, request, mode == "responses-stream")
					if !fail && !strings.Contains(w.Body.String(), "red circle") {
						t.Fatal("stream lost image answer")
					}
				}
				if r.plainCalls != 0 || r.imageCalls != 1 || !cleaned {
					t.Fatalf("plain=%d image=%d cleaned=%v", r.plainCalls, r.imageCalls, cleaned)
				}
				if fail && (w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "invalid_image")) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body)
				}
			})
		}
	}
}

func TestNativeImageLimitBeforeStaging(t *testing.T) {
	s := &Server{stageImages: func(context.Context, []encodedImage) (stagedImages, error) {
		t.Fatal("over-limit images reached staging")
		return stagedImages{}, nil
	}}
	w := httptest.NewRecorder()
	_, ok := s.stageNativeImages(context.Background(), w, preparedRequest{Images: make([]encodedImage, 2)})
	if ok || w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_parameter") || !strings.Contains(w.Body.String(), "maximum is 1") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}
