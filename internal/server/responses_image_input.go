// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// prepareResponsesImageRequest parses the supported Responses input forms.
// Image data stays encoded here; validation, decoding, and staging belong to
// stageImages.
func prepareResponsesImageRequest(request responsesRequest) (preparedRequest, error) {
	messages, images, err := parseResponsesInput(request.Input)
	if err != nil {
		return preparedRequest{}, err
	}
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append([]reqMessage{{Role: "system", Content: request.Instructions}}, messages...)
	}
	return preparedRequest{
		Model:  request.Model,
		Prompt: transcriptFrom(messages),
		Images: images,
		Stream: request.Stream,
	}, nil
}

func parseResponsesInput(input json.RawMessage) ([]reqMessage, []encodedImage, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, errors.New("input is required and must not be null")
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil, errors.New("input must not be empty")
		}
		return []reqMessage{{Role: "user", Content: text}}, nil, nil
	}

	var incoming []inMessage
	if err := strictUnmarshal(trimmed, &incoming); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return nil, nil, unsupportedParameter("input contains an unsupported parameter")
		}
		return nil, nil, errors.New("input must be a string or an array of messages")
	}

	for _, message := range incoming {
		if responsesContentHasInputImage(message.Content) {
			return parseResponsesMessagesWithImages(incoming)
		}
	}

	messages, err := parseMessages(incoming)
	if err != nil {
		return nil, nil, err
	}
	return messages, nil, nil
}

func parseResponsesMessagesWithImages(incoming []inMessage) ([]reqMessage, []encodedImage, error) {
	messages := make([]reqMessage, 0, len(incoming))
	var images []encodedImage

	for i, message := range incoming {
		role, err := responsesMessageRole(message.Role, i)
		if err != nil {
			return nil, nil, err
		}
		content, messageImages, hasInputText, err := parseResponsesImageContent(message.Content)
		if err != nil {
			return nil, nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		messages = append(messages, reqMessage{Role: role, Content: content})

		if len(messageImages) == 0 {
			continue
		}
		if i != len(incoming)-1 || role != "user" {
			return nil, nil, errors.New("image input is supported only in the final user message")
		}
		if !hasInputText {
			return nil, nil, errors.New("image input requires a nonempty input_text in the final user message")
		}
		images = append(images, messageImages...)
	}

	if len(messages) == 0 {
		return nil, nil, errors.New("messages must not be empty")
	}
	if messages[len(messages)-1].Role != "user" {
		return nil, nil, errors.New("the final message must have role \"user\"")
	}
	return messages, images, nil
}

func responsesMessageRole(role string, index int) (string, error) {
	switch role {
	case "system", "user", "assistant":
		return role, nil
	case "developer":
		return "system", nil
	default:
		return "", fmt.Errorf("messages[%d]: unsupported role %q", index, role)
	}
}

func responsesContentHasInputImage(content rawContent) bool {
	var parts []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(content), &parts); err != nil {
		return false
	}
	for _, part := range parts {
		if part.Type == "input_image" {
			return true
		}
	}
	return false
}

func parseResponsesImageContent(content rawContent) (string, []encodedImage, bool, error) {
	// Earlier turns may use the same string form as text-only requests.
	// Images in the final turn must not change how that history is read.
	var text string
	if err := json.Unmarshal([]byte(content), &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return "", nil, false, errors.New("content must not be empty")
		}
		return text, nil, false, nil
	}
	var parts []json.RawMessage
	if err := strictUnmarshal([]byte(content), &parts); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return "", nil, false, unsupportedParameter("content contains an unsupported parameter")
		}
		return "", nil, false, errors.New("content must be an array when it contains an image")
	}
	if len(parts) == 0 {
		return "", nil, false, errors.New("content parts must not be empty")
	}

	var prompt strings.Builder
	var images []encodedImage
	hasInputText := false
	for _, data := range parts {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			return "", nil, false, errors.New("content parts must be JSON objects")
		}

		switch header.Type {
		case "input_image":
			var part struct {
				Type     string `json:"type"`
				ImageURL string `json:"image_url"`
			}
			if err := strictUnmarshal(data, &part); err != nil {
				if strings.Contains(err.Error(), "unknown field") {
					return "", nil, false, unsupportedParameter("content contains an unsupported parameter")
				}
				return "", nil, false, invalidImage("input_image.image_url must be a string")
			}
			if !responsesInlineImageDataURL(part.ImageURL) {
				return "", nil, false, invalidImage("image_url must be an inline PNG or JPEG data URL")
			}
			images = append(images, encodedImage{DataURL: part.ImageURL})

		case "text", "input_text", "output_text":
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := strictUnmarshal(data, &part); err != nil {
				if strings.Contains(err.Error(), "unknown field") {
					return "", nil, false, unsupportedParameter("content contains an unsupported parameter")
				}
				return "", nil, false, errors.New("content parts must contain text")
			}
			if strings.TrimSpace(part.Text) == "" {
				return "", nil, false, errors.New("content part text must not be empty")
			}
			prompt.WriteString(part.Text)
			if part.Type == "input_text" {
				hasInputText = true
			}

		default:
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := strictUnmarshal(data, &part); err != nil {
				if strings.Contains(err.Error(), "unknown field") {
					return "", nil, false, unsupportedParameter("content contains an unsupported parameter")
				}
				return "", nil, false, errors.New("content contains an unsupported parameter")
			}
			return "", nil, false, fmt.Errorf("unsupported content part type %q", header.Type)
		}
	}
	return prompt.String(), images, hasInputText, nil
}

func responsesInlineImageDataURL(value string) bool {
	return strings.HasPrefix(value, "data:image/png;base64,") || strings.HasPrefix(value, "data:image/jpeg;base64,")
}
