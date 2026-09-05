// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kamenxrider/hollis/internal/imagegen"
)

const defaultImageStyle = "animation"

type imageGenerationOptions struct {
	Style       string `json:"style"`
	AspectRatio string `json:"aspect_ratio"`
	Size        string `json:"size"`
	Fit         string `json:"fit"`
}

type imageGenerationsRequest struct {
	Model          string          `json:"model"`
	Prompt         string          `json:"prompt"`
	Style          string          `json:"style"`
	AspectRatio    string          `json:"aspect_ratio"`
	Size           string          `json:"size"`
	Fit            string          `json:"fit"`
	N              json.RawMessage `json:"n"`
	ResponseFormat string          `json:"response_format"`
}

type generatedImage struct {
	Base64           string
	Style            string
	Width            int
	Height           int
	SHA256           string
	NativeWidth      int
	NativeHeight     int
	OutputProcessing imagegen.OutputOptions
}

func (s *Server) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	var request imageGenerationsRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if len(request.N) != 0 {
		var n int
		if bytes.Equal(bytes.TrimSpace(request.N), []byte("null")) || json.Unmarshal(request.N, &n) != nil || n != 1 {
			writeRequestValidationError(w, unsupportedParameter("n must be 1"))
			return
		}
	}
	if request.ResponseFormat != "" && request.ResponseFormat != "b64_json" {
		writeRequestValidationError(w, unsupportedParameter("response_format must be \"b64_json\""))
		return
	}
	if !validImageRoute(request.Model) {
		writeRequestValidationError(w, unsupportedParameter("model must be \"hollis-image\" when provided"))
		return
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		writeRequestValidationError(w, errors.New("prompt must not be empty"))
		return
	}
	if !validatePrompt(w, request.Prompt) {
		return
	}
	options := imageGenerationOptions{
		Style: request.Style, AspectRatio: request.AspectRatio,
		Size: request.Size, Fit: request.Fit,
	}
	image, ok := s.generateImage(r.Context(), w, request.Prompt, options)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": time.Now().Unix(),
		"data": []map[string]any{{
			"b64_json":          image.Base64,
			"style":             image.Style,
			"width":             image.Width,
			"height":            image.Height,
			"sha256":            image.SHA256,
			"native_width":      image.NativeWidth,
			"native_height":     image.NativeHeight,
			"output_processing": image.OutputProcessing,
		}},
	})
}

func (s *Server) handleChatImageGeneration(w http.ResponseWriter, r *http.Request, request chatCompletionsRequest) {
	if !validImageRoute(request.Model) {
		writeRequestValidationError(w, unsupportedParameter("model must be \"hollis-image\" with image_generation"))
		return
	}
	options, err := parseImageGenerationOptions(request.ImageGeneration)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	messages, err := parseGenerationChatMessages(request.Messages)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	prompt := transcriptFrom(messages)
	if !validatePrompt(w, prompt) {
		return
	}
	image, ok := s.generateImage(r.Context(), w, prompt, options)
	if !ok {
		return
	}
	marker := generationMarker(image, messages[len(messages)-1].Content)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": "hollis-image",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": marker},
					{
						"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + image.Base64},
						"style": image.Style, "width": image.Width, "height": image.Height, "sha256": image.SHA256,
						"native_width": image.NativeWidth, "native_height": image.NativeHeight, "output_processing": image.OutputProcessing,
					},
				},
			},
			"finish_reason": "stop",
		}},
	})
}

func (s *Server) handleResponsesImageGeneration(w http.ResponseWriter, r *http.Request, request responsesRequest) {
	if !validImageRoute(request.Model) {
		writeRequestValidationError(w, unsupportedParameter("model must be \"hollis-image\" with image_generation"))
		return
	}
	options, err := parseImageGenerationOptions(request.ImageGeneration)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	messages, err := parseGenerationResponsesInput(request.Input)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append([]reqMessage{{Role: "system", Content: request.Instructions}}, messages...)
	}
	prompt := transcriptFrom(messages)
	if !validatePrompt(w, prompt) {
		return
	}
	image, ok := s.generateImage(r.Context(), w, prompt, options)
	if !ok {
		return
	}
	marker := generationMarker(image, messages[len(messages)-1].Content)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "resp_" + randomID(), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": "hollis-image",
		"output": []map[string]any{{
			"type": "message", "id": "msg_" + randomID(), "role": "assistant", "status": "completed",
			"content": []map[string]any{
				{"type": "output_text", "text": marker, "annotations": []any{}},
				{"type": "output_image", "b64_json": image.Base64, "mime_type": "image/png", "style": image.Style, "width": image.Width, "height": image.Height, "sha256": image.SHA256, "native_width": image.NativeWidth, "native_height": image.NativeHeight, "output_processing": image.OutputProcessing},
			},
		}},
	})
}

func parseImageGenerationOptions(raw json.RawMessage) (imageGenerationOptions, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return imageGenerationOptions{}, errors.New("image_generation must be an object")
	}
	var options imageGenerationOptions
	if err := strictUnmarshal(trimmed, &options); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return imageGenerationOptions{}, unsupportedParameter("image_generation contains an unsupported parameter")
		}
		return imageGenerationOptions{}, errors.New("image_generation must be an object")
	}
	return options, nil
}

func (s *Server) generateImage(ctx context.Context, w http.ResponseWriter, prompt string, options imageGenerationOptions) (generatedImage, bool) {
	outputOptions := imagegen.OutputOptions{
		AspectRatio: strings.TrimSpace(options.AspectRatio),
		Size:        strings.TrimSpace(options.Size),
		Fit:         strings.TrimSpace(options.Fit),
	}
	if err := imagegen.ValidateOutputOptions(outputOptions); err != nil {
		writeRequestValidationError(w, unsupportedParameter(err.Error()))
		return generatedImage{}, false
	}
	style := strings.TrimSpace(options.Style)
	if style == "" {
		style = defaultImageStyle
	}
	if !validImageStyle(style) {
		writeRequestValidationError(w, unsupportedParameter(fmt.Sprintf("unsupported image style %q", style)))
		return generatedImage{}, false
	}
	bridge := strings.TrimSpace(s.ImageBridges[style])
	requestStyle := ""
	if bridge == "" {
		bridge = strings.TrimSpace(s.ImageBridge)
		requestStyle = style
	}
	if bridge == "" || s.ImageGenerator == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "style_unavailable", "the selected image style is unavailable")
		return generatedImage{}, false
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return generatedImage{}, false
	}
	defer release()

	runCtx, cancel := context.WithTimeout(ctx, imagegen.MaxTimeout)
	defer cancel()
	result, err := s.ImageGenerator.Generate(runCtx, imagegen.Request{
		Prompt: prompt, BridgeRef: bridge, Style: requestStyle, Timeout: imagegen.MaxTimeout,
	})
	cleanup := s.cleanupImage
	if cleanup == nil {
		cleanup = func(result imagegen.Result) error { return result.Cleanup() }
	}
	var cleanupOnce sync.Once
	var cleanupErr error
	cleanupResult := func() error {
		cleanupOnce.Do(func() { cleanupErr = cleanup(result) })
		return cleanupErr
	}
	defer func() { _ = cleanupResult() }()
	if err != nil {
		writeImageGenerationError(w, err)
		return generatedImage{}, false
	}
	nativeWidth, nativeHeight := result.Width, result.Height
	processed, err := imagegen.TransformOutput(runCtx, result, outputOptions)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeAPIError(w, http.StatusGatewayTimeout, "server_error", "image_generation_timeout", "image generation did not complete before its deadline")
		} else {
			writeAPIError(w, http.StatusBadGateway, "server_error", "image_processing_failed", "generated image output processing failed")
		}
		return generatedImage{}, false
	}
	result = processed
	image, err := readGeneratedImage(result, style)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "server_error", "image_generation_failed", "image generation returned an invalid result")
		return generatedImage{}, false
	}
	image.NativeWidth = nativeWidth
	image.NativeHeight = nativeHeight
	image.OutputProcessing = outputOptions
	if err := cleanupResult(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "image_cleanup_failed", "generated image staging could not be cleaned")
		return generatedImage{}, false
	}
	return image, true
}

func validImageStyle(style string) bool {
	switch style {
	case "any", "animation", "genmoji", "illustration", "sketch", "chatgpt":
		return true
	default:
		return false
	}
}

func validImageRoute(model string) bool {
	return model == "" || model == "hollis-image"
}

func readGeneratedImage(result imagegen.Result, style string) (generatedImage, error) {
	if result.Path == "" || result.Width < 1 || result.Height < 1 || result.Bytes < 1 || result.Bytes > imagegen.MaxOutputBytes {
		return generatedImage{}, errors.New("invalid image metadata")
	}
	file, err := os.Open(result.Path)
	if err != nil {
		return generatedImage{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, imagegen.MaxOutputBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) != result.Bytes || int64(len(data)) > imagegen.MaxOutputBytes {
		return generatedImage{}, errors.New("invalid image data")
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	if result.SHA256 == "" || !strings.EqualFold(checksum, result.SHA256) {
		return generatedImage{}, errors.New("image checksum mismatch")
	}
	return generatedImage{
		Base64: base64.StdEncoding.EncodeToString(data), Style: style,
		Width: result.Width, Height: result.Height, SHA256: checksum,
	}, nil
}

func writeImageGenerationError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeAPIError(w, http.StatusGatewayTimeout, "server_error", "image_generation_timeout", "image generation did not complete before its deadline")
		return
	}
	var generationErr *imagegen.Error
	if errors.As(err, &generationErr) {
		switch generationErr.Kind {
		case imagegen.KindTimeout, imagegen.KindCanceled:
			writeAPIError(w, http.StatusGatewayTimeout, "server_error", "image_generation_timeout", "image generation did not complete before its deadline")
		case imagegen.KindMissingBridge:
			writeAPIError(w, http.StatusBadGateway, "server_error", "style_unavailable", "the selected image style is unavailable")
		default:
			writeAPIError(w, http.StatusBadGateway, "server_error", "image_generation_failed", "image generation failed")
		}
		return
	}
	writeAPIError(w, http.StatusBadGateway, "server_error", "image_generation_failed", "image generation failed")
}

func generationMarker(image generatedImage, request string) string {
	processing := "no output transform"
	if image.OutputProcessing.AspectRatio != "" {
		processing = fmt.Sprintf("local %s fit to aspect ratio %s", image.OutputProcessing.Fit, image.OutputProcessing.AspectRatio)
	} else if image.OutputProcessing.Size != "" {
		processing = fmt.Sprintf("local %s fit to size %s", image.OutputProcessing.Fit, image.OutputProcessing.Size)
	}
	return fmt.Sprintf("[Hollis generated an image for %q with style %s (native %dx%d, final %dx%d, %s, SHA-256 %s). The image pixels are included in this response but are not retained or reused; replay this text to preserve the generation record. A later request uses text context to generate a new image, not pixel editing.]", request, image.Style, image.NativeWidth, image.NativeHeight, image.Width, image.Height, processing, image.SHA256)
}

func parseGenerationChatMessages(raw json.RawMessage) ([]reqMessage, error) {
	return parseGenerationMessages(raw, "messages", false)
}

func parseGenerationResponsesInput(raw json.RawMessage) ([]reqMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("input must not be empty")
		}
		return []reqMessage{{Role: "user", Content: text}}, nil
	}
	return parseGenerationMessages(raw, "input", true)
}

func parseGenerationMessages(raw json.RawMessage, field string, responses bool) ([]reqMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("%s must not be empty", field)
	}
	var incoming []json.RawMessage
	if err := strictUnmarshal(trimmed, &incoming); err != nil || len(incoming) == 0 {
		return nil, fmt.Errorf("%s must be a nonempty array of messages", field)
	}
	out := make([]reqMessage, 0, len(incoming))
	for i, rawMessage := range incoming {
		var message struct {
			Role    string     `json:"role"`
			Content rawContent `json:"content"`
			Type    string     `json:"type,omitempty"`
			ID      string     `json:"id,omitempty"`
			Status  string     `json:"status,omitempty"`
		}
		if err := strictUnmarshal(rawMessage, &message); err != nil {
			if strings.Contains(err.Error(), "unknown field") {
				return nil, unsupportedParameter(fmt.Sprintf("%s contains an unsupported parameter", field))
			}
			return nil, fmt.Errorf("%s[%d] is not a valid message", field, i)
		}
		role := message.Role
		if role == "developer" {
			role = "system"
		}
		if role != "system" && role != "user" && role != "assistant" {
			return nil, fmt.Errorf("%s[%d]: unsupported role %q", field, i, message.Role)
		}
		if message.Type != "" && (message.Type != "message" || role != "assistant") {
			return nil, fmt.Errorf("%s[%d]: unsupported item type %q", field, i, message.Type)
		}
		if message.Status != "" && message.Status != "completed" {
			return nil, fmt.Errorf("%s[%d]: unsupported status %q", field, i, message.Status)
		}
		content, err := parseGenerationContent(message.Content, role, responses)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		out = append(out, reqMessage{Role: role, Content: content})
	}
	if out[len(out)-1].Role != "user" {
		return nil, errors.New("the final message must have role \"user\"")
	}
	return out, nil
}

func parseGenerationContent(raw rawContent, role string, responses bool) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return "", errors.New("content must not be empty")
		}
		return text, nil
	}
	var parts []json.RawMessage
	if err := strictUnmarshal(raw, &parts); err != nil || len(parts) == 0 {
		return "", errors.New("content must be text or a nonempty array")
	}
	var output strings.Builder
	for _, rawPart := range parts {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawPart, &header); err != nil {
			return "", errors.New("content parts must be objects")
		}
		switch header.Type {
		case "text", "input_text", "output_text":
			var part struct {
				Type        string            `json:"type"`
				Text        string            `json:"text"`
				Annotations []json.RawMessage `json:"annotations,omitempty"`
			}
			if err := strictUnmarshal(rawPart, &part); err != nil || strings.TrimSpace(part.Text) == "" {
				return "", errors.New("text content part is invalid")
			}
			output.WriteString(part.Text)
		case "image_url":
			if responses || role != "assistant" {
				return "", unsupportedParameter("image generation accepts text history, not image input")
			}
			var part struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
				Style            string                 `json:"style"`
				Width            int                    `json:"width"`
				Height           int                    `json:"height"`
				SHA256           string                 `json:"sha256"`
				NativeWidth      int                    `json:"native_width"`
				NativeHeight     int                    `json:"native_height"`
				OutputProcessing imagegen.OutputOptions `json:"output_processing"`
			}
			if err := strictUnmarshal(rawPart, &part); err != nil || !strings.HasPrefix(part.ImageURL.URL, "data:image/png;base64,") {
				return "", errors.New("replayed generated image part is invalid")
			}
		case "output_image":
			if !responses || role != "assistant" {
				return "", unsupportedParameter("image generation accepts text history, not image input")
			}
			var part struct {
				Type             string                 `json:"type"`
				Base64           string                 `json:"b64_json"`
				MIMEType         string                 `json:"mime_type"`
				Style            string                 `json:"style"`
				Width            int                    `json:"width"`
				Height           int                    `json:"height"`
				SHA256           string                 `json:"sha256"`
				NativeWidth      int                    `json:"native_width"`
				NativeHeight     int                    `json:"native_height"`
				OutputProcessing imagegen.OutputOptions `json:"output_processing"`
			}
			if err := strictUnmarshal(rawPart, &part); err != nil || part.MIMEType != "image/png" || part.Base64 == "" {
				return "", errors.New("replayed generated image part is invalid")
			}
		default:
			return "", fmt.Errorf("unsupported content part type %q", header.Type)
		}
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", errors.New("content requires text; generated pixels alone have no conversation memory")
	}
	return output.String(), nil
}
