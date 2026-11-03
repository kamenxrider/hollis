// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type localImageRecorder struct {
	pdfRecordingRunner
	streamed bool
}

func (r *localImageRecorder) RunCompleteWithImages(_ context.Context, model runner.Model, prompt string, images []string) (runner.Completion, error) {
	r.prompt = prompt
	r.calls++
	return runner.Completion{Text: "red circle", Model: model, Usage: &runner.Usage{InputTokens: 10, OutputTokens: 2}}, nil
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
