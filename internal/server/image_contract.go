// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
)

const (
	MaxDecodedImageBytes    = 4 << 20
	MaxImagePixels          = 16_000_000
	MaxAggregateImagePixels = 24_000_000
	MaxCloudImages          = 3
	MaxChatGPTImages        = 1
)

// encodedImage is the parser-to-validator contract. Parsers preserve request
// order and leave all data URL, MIME, structure, and dimension validation to
// stageImages.
type encodedImage struct {
	DataURL string
}

// preparedRequest is the common endpoint-parser result consumed by the HTTP
// integration. Prompt is the complete rendered prompt sent to the runner.
type preparedRequest struct {
	Model  string
	Prompt string
	Images []encodedImage
	Stream bool
}

func (r preparedRequest) hasImages() bool {
	return len(r.Images) != 0
}

// stagedImages owns every path in Paths. Cleanup is idempotent and removes
// only the request-private directory created by stageImages.
type stagedImages struct {
	Paths   []string
	cleanup func()
}

func (s stagedImages) Cleanup() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

// chatCompletionsRequest and responsesRequest are deliberately shallow. Their
// endpoint-specific helpers strictly decode the raw nested payloads while the
// existing HTTP decoder continues to enforce the top-level schema and body
// limit.
type chatCompletionsRequest struct {
	Model           string          `json:"model"`
	Messages        json.RawMessage `json:"messages"`
	Stream          bool            `json:"stream"`
	ImageGeneration json.RawMessage `json:"image_generation"`
}

type responsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions"`
	Input           json.RawMessage `json:"input"`
	Stream          bool            `json:"stream"`
	ImageGeneration json.RawMessage `json:"image_generation"`
}

// Implemented in the endpoint-owned parser files.
//
// prepareChatImageRequest(request chatCompletionsRequest) (preparedRequest, error)
// prepareResponsesImageRequest(request responsesRequest) (preparedRequest, error)
//
// Implemented in image_input.go. The implementation validates all encoded
// inputs, creates a private request directory and files, and cleans partial
// state before returning an error.
//
// stageImages(ctx context.Context, images []encodedImage) (stagedImages, error)
type imageStagingFunc func(context.Context, []encodedImage) (stagedImages, error)
