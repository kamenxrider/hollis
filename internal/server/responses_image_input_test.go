// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPrepareResponsesImageRequestPreservesLegacyTextAndInstructions(t *testing.T) {
	request := responsesRequest{
		Model:        "cloud",
		Instructions: "Always answer briefly",
		Input:        json.RawMessage(`[{"role":"developer","content":"Use plain words"},{"role":"user","content":[{"type":"input_text","text":"What is 2+2?"}]},{"role":"assistant","content":[{"type":"output_text","text":"Four"}]},{"role":"user","content":"And 3+3?"}]`),
	}

	prepared, err := prepareResponsesImageRequest(request)
	if err != nil {
		t.Fatalf("prepareResponsesImageRequest: %v", err)
	}
	if prepared.Model != "cloud" || len(prepared.Images) != 0 {
		t.Fatalf("prepared request = %+v", prepared)
	}
	for _, want := range []string{
		"SYSTEM:\nAlways answer briefly",
		"SYSTEM:\nUse plain words",
		"USER:\nWhat is 2+2?",
		"ASSISTANT:\nFour",
		"USER:\nAnd 3+3?",
	} {
		if !strings.Contains(prepared.Prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prepared.Prompt)
		}
	}
}

func TestPrepareResponsesImageRequestPreservesImageOrderAndPromptText(t *testing.T) {
	request := responsesRequest{
		Input: json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"Describe "},{"type":"input_image","image_url":"data:image/png;base64,first"},{"type":"input_text","text":"both"},{"type":"input_image","image_url":"data:image/jpeg;base64,second"}]}]`),
	}

	prepared, err := prepareResponsesImageRequest(request)
	if err != nil {
		t.Fatalf("prepareResponsesImageRequest: %v", err)
	}
	if got, want := len(prepared.Images), 2; got != want {
		t.Fatalf("image count = %d, want %d", got, want)
	}
	if got, want := prepared.Images[0].DataURL, "data:image/png;base64,first"; got != want {
		t.Fatalf("first image = %q, want %q", got, want)
	}
	if got, want := prepared.Images[1].DataURL, "data:image/jpeg;base64,second"; got != want {
		t.Fatalf("second image = %q, want %q", got, want)
	}
	if !strings.Contains(prepared.Prompt, "USER:\nDescribe both") {
		t.Fatalf("prompt = %q", prepared.Prompt)
	}
}

func TestPrepareResponsesImageRequestPreservesStringHistoryWithImage(t *testing.T) {
	request := responsesRequest{Input: json.RawMessage(`[{"role":"system","content":"Be concise"},{"role":"user","content":"Remember the blue square"},{"role":"assistant","content":"I will"},{"role":"user","content":[{"type":"input_text","text":"Compare this image"},{"type":"input_image","image_url":"data:image/png;base64,image"}]}]`)}
	prepared, err := prepareResponsesImageRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SYSTEM:\nBe concise", "USER:\nRemember the blue square", "ASSISTANT:\nI will", "USER:\nCompare this image"} {
		if !strings.Contains(prepared.Prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prepared.Prompt)
		}
	}
	if len(prepared.Images) != 1 {
		t.Fatalf("image count = %d, want 1", len(prepared.Images))
	}
}

func TestPrepareResponsesImageRequestRejectsUnsupportedImageForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "remote URL",
			input: `[{"role":"user","content":[{"type":"input_text","text":"Describe"},{"type":"input_image","image_url":"https://example.invalid/image.png"}]}]`,
			want:  "inline PNG or JPEG data URL",
		},
		{
			name:  "historical image",
			input: `[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,earlier"}]},{"role":"user","content":[{"type":"input_text","text":"Describe"}]}]`,
			want:  "final user message",
		},
		{
			name:  "missing input text",
			input: `[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,image"}]}]`,
			want:  "nonempty input_text",
		},
		{
			name:  "unsupported MIME",
			input: `[{"role":"user","content":[{"type":"input_text","text":"Describe"},{"type":"input_image","image_url":"data:image/gif;base64,image"}]}]`,
			want:  "inline PNG or JPEG data URL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := prepareResponsesImageRequest(responsesRequest{Input: json.RawMessage(test.input)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPrepareResponsesImageRequestRejectsUnknownImageFields(t *testing.T) {
	_, err := prepareResponsesImageRequest(responsesRequest{Input: json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"Describe"},{"type":"input_image","image_url":"data:image/png;base64,image","detail":"high"}]}]`)})
	if err == nil {
		t.Fatal("unknown image field accepted")
	}
	var validationErr *requestValidationError
	if !errors.As(err, &validationErr) || validationErr.code != "unsupported_parameter" {
		t.Fatalf("error = %v, want unsupported_parameter", err)
	}
}

func TestPrepareResponsesImageRequestRejectsUnknownMessageFields(t *testing.T) {
	_, err := prepareResponsesImageRequest(responsesRequest{Input: json.RawMessage(`[{"role":"user","content":"Describe","name":"legacy"}]`)})
	if err == nil {
		t.Fatal("unknown message field accepted")
	}
	var validationErr *requestValidationError
	if !errors.As(err, &validationErr) || validationErr.code != "unsupported_parameter" {
		t.Fatalf("error = %v, want unsupported_parameter", err)
	}
}
