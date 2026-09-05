// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPrepareChatImageRequestPreservesHistoryAndImageOrder(t *testing.T) {
	request := chatCompletionsRequest{
		Model:  "cloud-pro",
		Stream: true,
		Messages: json.RawMessage(`[
			{"role":"developer","content":"Use plain words"},
			{"role":"assistant","content":[{"type":"text","text":"Sure."}]},
			{"role":"user","content":[{"type":"text","text":"Describe "},{"type":"image_url","image_url":{"url":"data:image/png;base64,first"}},{"type":"text","text":"both"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,second"}}]}
		]`),
	}

	prepared, err := prepareChatImageRequest(request)
	if err != nil {
		t.Fatalf("prepareChatImageRequest: %v", err)
	}
	if prepared.Model != "cloud-pro" || !prepared.Stream || len(prepared.Images) != 2 {
		t.Fatalf("prepared request = %+v", prepared)
	}
	if got, want := prepared.Images[0].DataURL, "data:image/png;base64,first"; got != want {
		t.Fatalf("first image = %q, want %q", got, want)
	}
	if got, want := prepared.Images[1].DataURL, "data:image/jpeg;base64,second"; got != want {
		t.Fatalf("second image = %q, want %q", got, want)
	}
	for _, want := range []string{
		"SYSTEM:\nUse plain words",
		"ASSISTANT:\nSure.",
		"USER:\nDescribe both",
	} {
		if !strings.Contains(prepared.Prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prepared.Prompt)
		}
	}
}

func TestPrepareChatImageRequestPreservesLegacyTextRequests(t *testing.T) {
	request := chatCompletionsRequest{
		Model: "cloud",
		Messages: json.RawMessage(`[
			{"role":"system","content":"Be brief"},
			{"role":"user","content":"What is 2+2?"}
		]`),
	}

	prepared, err := prepareChatImageRequest(request)
	if err != nil {
		t.Fatalf("prepareChatImageRequest: %v", err)
	}
	if prepared.Model != "cloud" || prepared.Stream || len(prepared.Images) != 0 {
		t.Fatalf("prepared request = %+v", prepared)
	}
	if !strings.Contains(prepared.Prompt, "USER:\nWhat is 2+2?") {
		t.Fatalf("prompt = %q", prepared.Prompt)
	}
}

func TestPrepareChatImageRequestRejectsUnsupportedImageForms(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  string
		code  string
		exact bool
	}{
		{
			name: "earlier image",
			raw:  `[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,earlier"}}]},{"role":"user","content":[{"type":"text","text":"Describe"}]}]`,
			want: "final user message",
		},
		{
			name: "image in system message",
			raw:  `[{"role":"system","content":[{"type":"text","text":"Be brief"},{"type":"image_url","image_url":{"url":"data:image/png;base64,image"}}]},{"role":"user","content":[{"type":"text","text":"Describe"}]}]`,
			want: "final user message",
		},
		{
			name: "image without text",
			raw:  `[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,image"}}]}]`,
			want: "nonempty text part",
		},
		{
			name: "remote URL",
			raw:  `[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":"https://example.invalid/image.png"}}]}]`,
			want: "inline PNG or JPEG data URL",
		},
		{
			name: "local path",
			raw:  `[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":"/tmp/image.png"}}]}]`,
			want: "inline PNG or JPEG data URL",
		},
		{
			name: "unsupported MIME",
			raw:  `[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":"data:image/gif;base64,image"}}]}]`,
			want: "inline PNG or JPEG data URL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := prepareChatImageRequest(chatCompletionsRequest{Messages: json.RawMessage(test.raw)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPrepareChatImageRequestRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "image option",
			raw:  `[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,image","detail":"high"}}]}]`,
		},
		{
			name: "text option",
			raw:  `[{"role":"user","content":[{"type":"text","text":"Describe","id":"text-1"},{"type":"image_url","image_url":{"url":"data:image/png;base64,image"}}]}]`,
		},
		{
			name: "message option",
			raw:  `[{"role":"user","content":"Describe","name":"legacy"}]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := prepareChatImageRequest(chatCompletionsRequest{Messages: json.RawMessage(test.raw)})
			var validationErr *requestValidationError
			if !errors.As(err, &validationErr) || validationErr.code != "unsupported_parameter" {
				t.Fatalf("error = %v, want unsupported_parameter", err)
			}
		})
	}
}

func TestPrepareChatImageRequestRejectsEmptyHistoryWithImage(t *testing.T) {
	for _, content := range []string{`""`, `"  "`, `[]`, `null`} {
		request := chatCompletionsRequest{Messages: json.RawMessage(`[{"role":"assistant","content":` + content + `},{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,image"}}]}]`)}
		if _, err := prepareChatImageRequest(request); err == nil {
			t.Fatalf("accepted empty history content %s", content)
		}
	}
}
