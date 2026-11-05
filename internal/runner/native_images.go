// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
)

const NativeMaxImages = 1

type ImageCompleteRunner interface {
	RunCompleteWithImages(context.Context, Model, string, []string) (Completion, error)
}

type ImageStreamRunner interface {
	StreamWithImages(context.Context, Model, string, []string, func(string) error) (Completion, error)
}

func (r *RoutedRunner) RunCompleteWithImages(ctx context.Context, model Model, prompt string, images []string) (Completion, error) {
	if model == ModelLocal {
		return r.Native.RunCompleteWithImages(ctx, model, prompt, images)
	}
	text, used, err := r.ShortcutRunner.RunWithImages(ctx, model, prompt, images)
	return Completion{Text: text, Model: used}, err
}

func (r *RoutedRunner) StreamWithImages(ctx context.Context, model Model, prompt string, images []string, emit func(string) error) (Completion, error) {
	return r.Native.StreamWithImages(ctx, model, prompt, images, emit)
}

func (r *NativeRunner) RunWithImages(ctx context.Context, model Model, prompt string, images []string) (string, Model, error) {
	result, err := r.RunCompleteWithImages(ctx, model, prompt, images)
	return result.Text, model, err
}

func (r *NativeRunner) RunCompleteWithImages(ctx context.Context, model Model, prompt string, images []string) (Completion, error) {
	return r.invokeImages(ctx, model, prompt, images, nil)
}

func (r *NativeRunner) StreamWithImages(ctx context.Context, model Model, prompt string, images []string, emit func(string) error) (Completion, error) {
	return r.invokeImages(ctx, model, prompt, images, emit)
}

func (r *NativeRunner) invokeImages(ctx context.Context, model Model, prompt string, images []string, emit func(string) error, options ...*GenerationOptions) (Completion, error) {
	if model != ModelLocal {
		return Completion{}, nativeError(KindUsage, "native backend only supports model local")
	}
	if len(images) != NativeMaxImages {
		return Completion{}, nativeError(KindUsage, "local accepts exactly 1 image; the maximum is 1")
	}
	if strings.TrimSpace(prompt) == "" {
		return Completion{}, nativeError(KindEmptyPrompt, "image requests require a non-empty prompt")
	}
	if !utf8.ValidString(prompt) {
		return Completion{}, nativeError(KindUsage, "native prompt must be valid UTF-8")
	}
	if len(prompt) > NativeMaxPromptBytes {
		return Completion{}, nativeError(KindContextCapacity, "image prompt exceeds the 128 KiB input limit")
	}
	staged, cleanup, err := stageImageInputs(images)
	if err != nil {
		return Completion{}, nativeImageStagingError(err)
	}
	defer cleanup()
	operation := "complete"
	if emit != nil {
		operation = "stream"
	}
	opts := nativeOptions{Images: []nativeSource{{Path: staged[0]}}}
	if len(options) > 0 {
		opts.GenerationOptions = options[0]
	}
	result, _, err := r.invoke(ctx, model, operation, prompt, emit, nil, opts)
	return result, err
}

// Staging is shared with Shortcuts, whose errors include filesystem context.
// Native callers receive only fixed causes and measured limits, never that
// diagnostic chain (including paths buried in wrapped PathErrors).
func nativeImageStagingError(err error) *Error {
	var classified *Error
	if errors.As(err, &classified) && classified.Kind == KindTransport {
		return nativeError(KindTransport, "could not create or write private native image staging")
	}
	if errors.Is(err, errUnsupportedImageMedia) {
		return nativeError(KindUnsupportedMedia, "native image input supports PNG and JPEG only")
	}
	var limit *imageInputLimitError
	if errors.As(err, &limit) {
		unit := "bytes"
		if limit.pixels {
			unit = "pixels"
		}
		failure := nativeError(KindUsage, fmt.Sprintf("native image input has %d %s; the limit is %d %s", limit.actual, unit, limit.limit, unit))
		failure.Limit, failure.Actual = int(limit.limit), int(limit.actual)
		return failure
	}
	if errors.Is(err, os.ErrNotExist) {
		return nativeError(KindUsage, "native image source does not exist")
	}
	if errors.Is(err, os.ErrPermission) {
		return nativeError(KindUsage, "native image source cannot be read with current permissions")
	}
	return nativeError(KindUsage, "native image source must be a readable direct regular file containing a valid PNG or JPEG")
}

func requireImages(status LocalStatus) *Error {
	if status.Capabilities == nil || status.Capabilities.Images == nil {
		return capabilityRefusal("images", "")
	}
	images := status.Capabilities.Images
	if !images.Supported {
		return capabilityRefusal("images", images.Reason)
	}
	if images.MaxImages < NativeMaxImages || images.MaxSidePX <= 0 ||
		!slices.Contains(images.Formats, "png") || !slices.Contains(images.Formats, "jpeg") {
		return capabilityRefusal("images", "")
	}
	return nil
}
