// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// prepareChatImageRequest parses the supported Chat Completions input forms.
// Image data stays encoded here; validation, decoding, and staging belong to
// stageImages.
func prepareChatImageRequest(request chatCompletionsRequest) (preparedRequest, error) {
	incoming, err := parseChatMessagesInput(request.Messages)
	if err != nil {
		return preparedRequest{}, err
	}

	var hasImage bool
	for _, message := range incoming {
		if chatContentHasImage(message.Content) {
			hasImage = true
			break
		}
	}

	var messages []reqMessage
	var images []encodedImage
	if !hasImage {
		messages, err = parseMessages(incoming)
	} else {
		messages, images, err = parseChatMessagesWithImages(incoming)
	}
	if err != nil {
		return preparedRequest{}, err
	}

	return preparedRequest{
		Model:  request.Model,
		Prompt: transcriptFrom(messages),
		Images: images,
		Stream: request.Stream,
	}, nil
}

func parseChatMessagesInput(raw json.RawMessage) ([]inMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("messages must not be empty")
	}

	var incoming []inMessage
	if err := strictUnmarshal(trimmed, &incoming); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return nil, unsupportedParameter("messages contains an unsupported parameter")
		}
		return nil, errors.New("messages must be an array of messages")
	}
	return incoming, nil
}

func parseChatMessagesWithImages(incoming []inMessage) ([]reqMessage, []encodedImage, error) {
	messages := make([]reqMessage, 0, len(incoming))
	var images []encodedImage

	for i, message := range incoming {
		role := message.Role
		switch role {
		case "system", "user", "assistant":
		case "developer":
			role = "system"
		default:
			return nil, nil, fmt.Errorf("messages[%d]: unsupported role %q", i, message.Role)
		}

		content, messageImages, err := parseChatContent(message.Content)
		if err != nil {
			return nil, nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		if len(messageImages) == 0 && strings.TrimSpace(content) == "" {
			return nil, nil, fmt.Errorf("messages[%d]: content must not be empty", i)
		}
		messages = append(messages, reqMessage{Role: role, Content: content})

		if len(messageImages) == 0 {
			continue
		}
		if i != len(incoming)-1 || role != "user" {
			return nil, nil, errors.New("image input is supported only in the final user message")
		}
		if strings.TrimSpace(content) == "" {
			return nil, nil, errors.New("image input requires a nonempty text part in the final user message")
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

func chatContentHasImage(content rawContent) bool {
	var parts []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(content), &parts); err != nil {
		return false
	}
	for _, part := range parts {
		if part.Type == "image_url" {
			return true
		}
	}
	return false
}

func parseChatContent(content rawContent) (string, []encodedImage, error) {
	if !chatContentHasImage(content) {
		text, err := content.text()
		return text, nil, err
	}
	return parseChatImageContent(content)
}

func parseChatImageContent(content rawContent) (string, []encodedImage, error) {
	var parts []json.RawMessage
	if err := strictUnmarshal([]byte(content), &parts); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return "", nil, unsupportedParameter("content contains an unsupported parameter")
		}
		return "", nil, errors.New("content must be an array when it contains an image")
	}

	var prompt strings.Builder
	var images []encodedImage
	for _, data := range parts {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			return "", nil, errors.New("content parts must be JSON objects")
		}

		switch header.Type {
		case "image_url":
			var part struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			if err := strictUnmarshal(data, &part); err != nil {
				if strings.Contains(err.Error(), "unknown field") {
					return "", nil, unsupportedParameter("content contains an unsupported parameter")
				}
				return "", nil, invalidImage("image_url must be an inline PNG or JPEG data URL")
			}
			if !chatInlineImageDataURL(part.ImageURL.URL) {
				return "", nil, invalidImage("image_url must be an inline PNG or JPEG data URL")
			}
			images = append(images, encodedImage{DataURL: part.ImageURL.URL})

		case "text":
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := strictUnmarshal(data, &part); err != nil {
				if strings.Contains(err.Error(), "unknown field") {
					return "", nil, unsupportedParameter("content contains an unsupported parameter")
				}
				return "", nil, errors.New("content parts must contain text")
			}
			if strings.TrimSpace(part.Text) == "" {
				return "", nil, errors.New("content part text must not be empty")
			}
			prompt.WriteString(part.Text)

		default:
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := strictUnmarshal(data, &part); err != nil {
				if strings.Contains(err.Error(), "unknown field") {
					return "", nil, unsupportedParameter("content contains an unsupported parameter")
				}
				return "", nil, errors.New("content contains an unsupported parameter")
			}
			return "", nil, fmt.Errorf("unsupported content part type %q", header.Type)
		}
	}
	return prompt.String(), images, nil
}

func chatInlineImageDataURL(value string) bool {
	return strings.HasPrefix(value, "data:image/png;base64,") || strings.HasPrefix(value, "data:image/jpeg;base64,")
}
