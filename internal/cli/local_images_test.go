// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type localImageRecorder struct {
	pdfRecordingRunner
	streamed bool
	images   []string
}

func (r *localImageRecorder) RunCompleteWithImages(_ context.Context, model runner.Model, prompt string, images []string) (runner.Completion, error) {
	r.prompt = prompt
	r.calls++
	r.images = append([]string(nil), images...)
	return runner.Completion{Text: "red circle", Model: model, Usage: &runner.Usage{InputTokens: 10, OutputTokens: 2}}, nil
}

func TestLocalImagesPublicAdmission(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("stream=%v/count=%d", streaming, count), func(t *testing.T) {
				r := &localImageRecorder{}
				args := []string{"--model", "local", "--image", "/synthetic-fixture.png"}
				if count == 2 {
					args = append(args, "--image", "/second-fixture.png")
				}
				if streaming {
					args = append(args, "--stream")
				} else {
					args = append(args, "--agent")
				}
				args = append(args, "Describe")
				out, _, err := runRespond(t, r, args...)
				if count == 2 {
					if ExitCode(err) != 2 || r.calls != 0 {
						t.Fatalf("two-image refusal: err=%v calls=%d", err, r.calls)
					}
					return
				}
				if err != nil || r.calls != 1 || r.streamed != streaming ||
					len(r.images) != 1 || r.images[0] != "/synthetic-fixture.png" ||
					!strings.Contains(out, "red circle") {
					t.Fatalf("image admission: err=%v calls=%d images=%v out=%s", err, r.calls, r.images, out)
				}
			})
		}
	}
}
func (r *localImageRecorder) StreamWithImages(ctx context.Context, model runner.Model, prompt string, images []string, emit func(string) error) (runner.Completion, error) {
	r.streamed = true
	result, _ := r.RunCompleteWithImages(ctx, model, prompt, images)
	result.Usage = nil
	return result, emit("red circle")
}

func TestLocalImageOutputModePreservesUsageAndStreaming(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		r := &localImageRecorder{}
		var out bytes.Buffer
		result, err := runLocalImages(t.Context(), r, "Describe", []string{"fixture.png"}, streaming, &out)
		if err != nil || result.Text != "red circle" || r.calls != 1 || r.streamed != streaming {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if streaming && (result.Usage != nil || out.String() != "red circle") {
			t.Fatal("stream output or usage changed")
		}
		if !streaming && (result.Usage == nil || out.Len() != 0) {
			t.Fatal("complete usage was lost or output was streamed")
		}
	}
}
