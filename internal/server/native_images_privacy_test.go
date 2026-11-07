// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestNativeImagePreHelperErrorHTTPPrivacy(t *testing.T) {
	for _, mode := range []string{"complete", "chat-stream", "responses-stream"} {
		for _, source := range []string{"missing", "corrupt", "staging-failure"} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "PRIVATE_SOURCE_CANARY.png")
				stage := filepath.Join(dir, "PRIVATE_STAGE_CANARY")
				if err := os.Mkdir(stage, 0o700); err != nil {
					t.Fatal(err)
				}
				if source == "corrupt" {
					if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nPRIVATE_CONTENT_CANARY"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if source == "staging-failure" {
					stage = filepath.Join(stage, "absent")
				}
				t.Setenv("TMPDIR", stage)
				cleaned := false
				s := &Server{Runner: &runner.NativeRunner{}, stageImages: func(context.Context, []encodedImage) (stagedImages, error) {
					return stagedImages{Paths: []string{path}, cleanup: func() { cleaned = true }}, nil
				}}
				request := preparedRequest{Prompt: "PRIVATE_PROMPT_CANARY", Images: []encodedImage{{DataURL: "fixture"}}}
				w := httptest.NewRecorder()
				if mode == "complete" {
					_, ok := s.completePrepared(t.Context(), w, runner.ModelLocal, request)
					if ok {
						t.Fatal("invalid image unexpectedly succeeded")
					}
				} else {
					s.streamPrepared(w, loopbackRequest("POST", "/", nil), runner.ModelLocal, request, mode == "responses-stream")
				}
				if w.Code < 400 || !cleaned {
					t.Fatalf("status=%d cleanup=%v", w.Code, cleaned)
				}
				if strings.Contains(w.Body.String(), "CANARY") || strings.Contains(w.Body.String(), dir) ||
					strings.Contains(w.Body.String(), "hollis-image-inputs-") {
					t.Fatal("private image diagnostic escaped HTTP error reporting")
				}
				if source != "staging-failure" {
					entries, err := os.ReadDir(stage)
					if err != nil || len(entries) != 0 {
						t.Fatal("native private staging survived HTTP failure")
					}
				}
			})
		}
	}
}
