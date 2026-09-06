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
	ReferenceImage string          `json:"reference_image"`
	N              json.RawMessage `json:"n"`
	ResponseFormat string          `json:"response_format"`
}

type generatedImage struct {
	Base64               string
	Style                string
	Width                int
	Height               int
	SHA256               string
	NativeWidth          int
	NativeHeight         int
	OutputProcessing     imagegen.OutputOptions
	ReferenceImageSent   bool
	ReferenceImageSHA256 string
}

type imageGenerationPlan struct {
	outputOptions imagegen.OutputOptions
	style         string
	bridge        string
	requestStyle  string
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
	plan, ok := s.prepareImageGeneration(w, options)
	if !ok {
		return
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return
	}
	defer release()
	var reference *imagegen.ReferenceImage
	if strings.TrimSpace(request.ReferenceImage) != "" {
		var err error
		reference, err = parseReferenceDataURL(request.ReferenceImage)
		if err != nil {
			writeRequestValidationError(w, err)
			return
		}
	}
	image, ok := s.generateImageAdmitted(r.Context(), w, request.Prompt, plan, reference)
	if !ok {
		return
	}
	data := map[string]any{
		"b64_json":          image.Base64,
		"style":             image.Style,
		"width":             image.Width,
		"height":            image.Height,
		"sha256":            image.SHA256,
		"native_width":      image.NativeWidth,
		"native_height":     image.NativeHeight,
		"output_processing": image.OutputProcessing,
	}
	addReferenceMetadata(data, image)
	writeJSON(w, http.StatusOK, map[string]any{
		"created": time.Now().Unix(),
		"data":    []map[string]any{data},
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
	plan, ok := s.prepareImageGeneration(w, options)
	if !ok {
		return
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return
	}
	defer release()
	messages, reference, err := parseGenerationChatMessages(request.Messages)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	unfiltered := transcriptFrom(messages)
	if !validatePrompt(w, unfiltered) {
		return
	}
	prompt := renderImageGenerationConversation(messages, reference != nil)
	if !validatePrompt(w, prompt) {
		return
	}
	image, ok := s.generateImageAdmitted(r.Context(), w, prompt, plan, reference)
	if !ok {
		return
	}
	marker := generationMarkerWithReference(image, messages[len(messages)-1].Content, reference != nil)
	imagePart := map[string]any{
		"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + image.Base64},
		"style": image.Style, "width": image.Width, "height": image.Height, "sha256": image.SHA256,
		"native_width": image.NativeWidth, "native_height": image.NativeHeight, "output_processing": image.OutputProcessing,
	}
	addReferenceMetadata(imagePart, image)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": "hollis-image",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": marker},
					imagePart,
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
	plan, ok := s.prepareImageGeneration(w, options)
	if !ok {
		return
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return
	}
	defer release()
	messages, reference, err := parseGenerationResponsesInput(request.Input)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append([]reqMessage{{Role: "system", Content: request.Instructions}}, messages...)
	}
	unfiltered := transcriptFrom(messages)
	if !validatePrompt(w, unfiltered) {
		return
	}
	prompt := renderImageGenerationConversation(messages, reference != nil)
	if !validatePrompt(w, prompt) {
		return
	}
	image, ok := s.generateImageAdmitted(r.Context(), w, prompt, plan, reference)
	if !ok {
		return
	}
	marker := generationMarkerWithReference(image, messages[len(messages)-1].Content, reference != nil)
	imagePart := map[string]any{
		"type": "output_image", "b64_json": image.Base64, "mime_type": "image/png", "style": image.Style,
		"width": image.Width, "height": image.Height, "sha256": image.SHA256, "native_width": image.NativeWidth,
		"native_height": image.NativeHeight, "output_processing": image.OutputProcessing,
	}
	addReferenceMetadata(imagePart, image)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "resp_" + randomID(), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": "hollis-image",
		"output": []map[string]any{{
			"type": "message", "id": "msg_" + randomID(), "role": "assistant", "status": "completed",
			"content": []map[string]any{
				{"type": "output_text", "text": marker, "annotations": []any{}},
				imagePart,
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

func (s *Server) prepareImageGeneration(w http.ResponseWriter, options imageGenerationOptions) (imageGenerationPlan, bool) {
	outputOptions := imagegen.OutputOptions{
		AspectRatio: strings.TrimSpace(options.AspectRatio),
		Size:        strings.TrimSpace(options.Size),
		Fit:         strings.TrimSpace(options.Fit),
	}
	if err := imagegen.ValidateOutputOptions(outputOptions); err != nil {
		writeRequestValidationError(w, unsupportedParameter(err.Error()))
		return imageGenerationPlan{}, false
	}
	style := strings.TrimSpace(options.Style)
	if style == "" {
		style = defaultImageStyle
	}
	if !validImageStyle(style) {
		writeRequestValidationError(w, unsupportedParameter(fmt.Sprintf("unsupported image style %q", style)))
		return imageGenerationPlan{}, false
	}
	bridge := strings.TrimSpace(s.ImageBridges[style])
	requestStyle := ""
	if bridge == "" {
		bridge = strings.TrimSpace(s.ImageBridge)
		requestStyle = style
	}
	if bridge == "" || s.ImageGenerator == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "style_unavailable", "the selected image style is unavailable")
		return imageGenerationPlan{}, false
	}
	return imageGenerationPlan{
		outputOptions: outputOptions,
		style:         style,
		bridge:        bridge,
		requestStyle:  requestStyle,
	}, true
}

// generateImageAdmitted runs only after the caller has reserved one model
// capacity slot. Reference decoding also happens under that reservation.
func (s *Server) generateImageAdmitted(ctx context.Context, w http.ResponseWriter, prompt string, plan imageGenerationPlan, reference *imagegen.ReferenceImage) (generatedImage, bool) {
	if reference != nil && plan.requestStyle == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "reference_requires_parameterized_bridge", "image references require a parameterized image bridge")
		return generatedImage{}, false
	}

	runCtx, cancel := context.WithTimeout(ctx, imagegen.MaxTimeout)
	defer cancel()
	result, err := s.ImageGenerator.Generate(runCtx, imagegen.Request{
		Prompt: prompt, BridgeRef: plan.bridge, Style: plan.requestStyle, Reference: reference, Timeout: imagegen.MaxTimeout,
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
	processed, err := imagegen.TransformOutput(runCtx, result, plan.outputOptions)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeAPIError(w, http.StatusGatewayTimeout, "server_error", "image_generation_timeout", "image generation did not complete before its deadline")
		} else {
			writeAPIError(w, http.StatusBadGateway, "server_error", "image_processing_failed", "generated image output processing failed")
		}
		return generatedImage{}, false
	}
	result = processed
	image, err := readGeneratedImage(result, plan.style)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "server_error", "image_generation_failed", "image generation returned an invalid result")
		return generatedImage{}, false
	}
	image.NativeWidth = nativeWidth
	image.NativeHeight = nativeHeight
	image.OutputProcessing = plan.outputOptions
	image.ReferenceImageSent = reference != nil
	if reference != nil {
		image.ReferenceImageSHA256 = reference.SHA256
	}
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
		case imagegen.KindSessionLocked:
			writeAPIError(w, http.StatusConflict, "invalid_request_error", "image_session_locked", "Shortcuts requires an unlocked Mac session; unlock or check the active session and retry manually")
		default:
			writeAPIError(w, http.StatusBadGateway, "server_error", "image_generation_failed", "image generation failed")
		}
		return
	}
	writeAPIError(w, http.StatusBadGateway, "server_error", "image_generation_failed", "image generation failed")
}

func generationMarker(image generatedImage, request string) string {
	return generationMarkerWithReference(image, request, false)
}

func generationMarkerWithReference(image generatedImage, request string, referenceSent bool) string {
	processing := "no output transform"
	if image.OutputProcessing.AspectRatio != "" {
		processing = fmt.Sprintf("local %s fit to aspect ratio %s", image.OutputProcessing.Fit, image.OutputProcessing.AspectRatio)
	} else if image.OutputProcessing.Size != "" {
		processing = fmt.Sprintf("local %s fit to size %s", image.OutputProcessing.Fit, image.OutputProcessing.Size)
	}
	if referenceSent {
		return fmt.Sprintf("[Hollis generated an image for %q with style %s (native %dx%d, final %dx%d, %s, SHA-256 %s). A supplied reference image was sent to the configured Shortcut; Hollis does not guarantee that a backend reused or edited its pixels.]", request, image.Style, image.NativeWidth, image.NativeHeight, image.Width, image.Height, processing, image.SHA256)
	}
	return fmt.Sprintf("[Hollis generated an image for %q with style %s (native %dx%d, final %dx%d, %s, SHA-256 %s). The server does not retain image pixels; replay the complete assistant image message to supply a later reference. Pixel editing is not guaranteed.]", request, image.Style, image.NativeWidth, image.NativeHeight, image.Width, image.Height, processing, image.SHA256)
}

func addReferenceMetadata(target map[string]any, image generatedImage) {
	if !image.ReferenceImageSent {
		return
	}
	target["reference_image_sent"] = true
	if image.ReferenceImageSHA256 != "" {
		target["reference_image_sha256"] = image.ReferenceImageSHA256
	}
}

func renderImageGenerationConversation(messages []reqMessage, hasReference bool) string {
	conversation := make([]imagegen.ConversationMessage, 0, len(messages))
	for _, message := range messages {
		conversation = append(conversation, imagegen.ConversationMessage{
			Role: message.Role, Content: message.Content,
		})
	}
	return imagegen.RenderConversationPromptWithReference(conversation, hasReference)
}

// parseReferenceDataURL accepts only the inline image representation exposed
// by the two public conversation APIs. In particular, it never treats a URL
// as a fetch target: image-generation references are client-supplied bytes.
func parseReferenceDataURL(dataURL string) (*imagegen.ReferenceImage, error) {
	payload, format, _, err := splitImageDataURL(dataURL)
	if err != nil {
		return nil, invalidImage("reference image must be an inline PNG or JPEG data URL")
	}
	return parseReferenceBase64(payload, "image/"+format)
}

// parseReferenceBase64 decodes one bounded inline reference and delegates
// format, dimensions, complete pixel decoding, and checksum validation to the
// imagegen package. The MIME argument is checked against the decoded bytes so
// replayed Responses output cannot relabel one image as another format.
func parseReferenceBase64(payload, mimeType string) (*imagegen.ReferenceImage, error) {
	decodedLen, err := exactBase64DecodedLen(payload)
	if err != nil {
		return nil, invalidImage("reference image contains invalid base64 data")
	}
	if int64(decodedLen) > imagegen.MaxReferenceBytes {
		return nil, imageLimitExceeded("reference image exceeds the 4 MiB limit")
	}
	decoded := make([]byte, decodedLen)
	count, err := base64.StdEncoding.Strict().Decode(decoded, []byte(payload))
	if err != nil || count != decodedLen {
		return nil, invalidImage("reference image contains invalid base64 data")
	}
	reference, err := imagegen.NewReferenceImage(decoded)
	if err != nil {
		if errors.Is(err, imagegen.ErrReferenceTooLarge) {
			return nil, imageLimitExceeded("reference image exceeds the 4 MiB or 16 megapixel limit")
		}
		return nil, invalidImage("reference image is not a valid PNG or JPEG")
	}
	if reference.MIMEType != mimeType {
		return nil, invalidImage("reference image MIME type does not match its data")
	}
	return reference, nil
}

func parseGenerationChatMessages(raw json.RawMessage) ([]reqMessage, *imagegen.ReferenceImage, error) {
	return parseGenerationMessages(raw, "messages", false)
}

func parseGenerationResponsesInput(raw json.RawMessage) ([]reqMessage, *imagegen.ReferenceImage, error) {
	trimmed := bytes.TrimSpace(raw)
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil, errors.New("input must not be empty")
		}
		return []reqMessage{{Role: "user", Content: text}}, nil, nil
	}
	return parseGenerationMessages(raw, "input", true)
}

func parseGenerationMessages(raw json.RawMessage, field string, responses bool) ([]reqMessage, *imagegen.ReferenceImage, error) {
	// decodeRequest has already applied the 8 MiB request bound. We still
	// validate every inline image in the bounded history, while selecting only
	// one image for the provider below; clients may need to trim very long
	// histories containing older image bytes before reaching that body limit.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, fmt.Errorf("%s must not be empty", field)
	}
	var incoming []json.RawMessage
	if err := strictUnmarshal(trimmed, &incoming); err != nil || len(incoming) == 0 {
		return nil, nil, fmt.Errorf("%s must be a nonempty array of messages", field)
	}
	out := make([]reqMessage, 0, len(incoming))
	var latestAssistantReference *imagegen.ReferenceImage
	var finalUserReference *imagegen.ReferenceImage
	var totalReferencePixels int64
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
				return nil, nil, unsupportedParameter(fmt.Sprintf("%s contains an unsupported parameter", field))
			}
			return nil, nil, fmt.Errorf("%s[%d] is not a valid message", field, i)
		}
		role := message.Role
		if role == "developer" {
			role = "system"
		}
		if role != "system" && role != "user" && role != "assistant" {
			return nil, nil, fmt.Errorf("%s[%d]: unsupported role %q", field, i, message.Role)
		}
		if message.Type != "" && (message.Type != "message" || role != "assistant") {
			return nil, nil, fmt.Errorf("%s[%d]: unsupported item type %q", field, i, message.Type)
		}
		if message.Status != "" && message.Status != "completed" {
			return nil, nil, fmt.Errorf("%s[%d]: unsupported status %q", field, i, message.Status)
		}
		content, contentReference, err := parseGenerationContent(message.Content, role, responses)
		if err != nil {
			return nil, nil, fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		if contentReference != nil {
			// Every inline image is decoded and checked, including historical
			// references that will not be sent to the provider. Bound their
			// combined decoded dimensions just like the general image-input
			// path; the request body limit alone does not bound decompressed
			// pixel memory.
			referencePixels := int64(contentReference.Width) * int64(contentReference.Height)
			if referencePixels > MaxAggregateImagePixels-totalReferencePixels {
				return nil, nil, imageLimitExceeded("image references exceed the 24 megapixel total limit")
			}
			totalReferencePixels += referencePixels

			// Every image is decoded and validated above, including historical
			// user images. Only the final user image or the latest prior
			// assistant image is attached to this generation request. This
			// preserves full text context while keeping one provider reference.
			if role == "system" {
				return nil, nil, invalidImage("reference image is not supported in system content")
			}
			if i == len(incoming)-1 {
				if role != "user" {
					return nil, nil, invalidImage("reference image must be in the final user message or a prior assistant image replay")
				}
				finalUserReference = contentReference
			} else if role == "assistant" {
				latestAssistantReference = contentReference
			}
		}
		out = append(out, reqMessage{Role: role, Content: content})
	}
	if out[len(out)-1].Role != "user" {
		return nil, nil, errors.New("the final message must have role \"user\"")
	}
	if finalUserReference != nil {
		return out, finalUserReference, nil
	}
	return out, latestAssistantReference, nil
}

func parseGenerationContent(raw rawContent, role string, responses bool) (string, *imagegen.ReferenceImage, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return "", nil, errors.New("content must not be empty")
		}
		return text, nil, nil
	}
	var parts []json.RawMessage
	if err := strictUnmarshal(raw, &parts); err != nil || len(parts) == 0 {
		return "", nil, errors.New("content must be text or a nonempty array")
	}
	var output strings.Builder
	var reference *imagegen.ReferenceImage
	for _, rawPart := range parts {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawPart, &header); err != nil {
			return "", nil, errors.New("content parts must be objects")
		}
		switch header.Type {
		case "text", "input_text", "output_text":
			var part struct {
				Type        string            `json:"type"`
				Text        string            `json:"text"`
				Annotations []json.RawMessage `json:"annotations,omitempty"`
			}
			if err := strictUnmarshal(rawPart, &part); err != nil || strings.TrimSpace(part.Text) == "" {
				return "", nil, errors.New("text content part is invalid")
			}
			output.WriteString(part.Text)
		case "image_url":
			if responses || (role != "assistant" && role != "user") {
				return "", nil, unsupportedParameter("image generation accepts image input only in user content or assistant image replay")
			}
			var part struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
				Style                string                 `json:"style"`
				Width                int                    `json:"width"`
				Height               int                    `json:"height"`
				SHA256               string                 `json:"sha256"`
				NativeWidth          int                    `json:"native_width"`
				NativeHeight         int                    `json:"native_height"`
				OutputProcessing     imagegen.OutputOptions `json:"output_processing"`
				ReferenceImageSent   bool                   `json:"reference_image_sent"`
				ReferenceImageSHA256 string                 `json:"reference_image_sha256"`
			}
			if err := strictUnmarshal(rawPart, &part); err != nil {
				return "", nil, errors.New("replayed generated image part is invalid")
			}
			parsed, err := parseReferenceDataURL(part.ImageURL.URL)
			if err != nil {
				return "", nil, err
			}
			if reference != nil {
				return "", nil, invalidImage("image generation accepts at most one reference image")
			}
			reference = parsed
		case "output_image":
			if !responses || role != "assistant" {
				return "", nil, unsupportedParameter("image generation accepts output image replay only in assistant content")
			}
			var part struct {
				Type                 string                 `json:"type"`
				Base64               string                 `json:"b64_json"`
				MIMEType             string                 `json:"mime_type"`
				Style                string                 `json:"style"`
				Width                int                    `json:"width"`
				Height               int                    `json:"height"`
				SHA256               string                 `json:"sha256"`
				NativeWidth          int                    `json:"native_width"`
				NativeHeight         int                    `json:"native_height"`
				OutputProcessing     imagegen.OutputOptions `json:"output_processing"`
				ReferenceImageSent   bool                   `json:"reference_image_sent"`
				ReferenceImageSHA256 string                 `json:"reference_image_sha256"`
			}
			if err := strictUnmarshal(rawPart, &part); err != nil || part.Base64 == "" {
				return "", nil, errors.New("replayed generated image part is invalid")
			}
			parsed, err := parseReferenceBase64(part.Base64, part.MIMEType)
			if err != nil {
				return "", nil, err
			}
			if reference != nil {
				return "", nil, invalidImage("image generation accepts at most one reference image")
			}
			reference = parsed
		case "input_image":
			if !responses || role != "user" {
				return "", nil, unsupportedParameter("image generation accepts input image only in the final user content")
			}
			var part struct {
				Type     string `json:"type"`
				ImageURL string `json:"image_url"`
			}
			if err := strictUnmarshal(rawPart, &part); err != nil {
				return "", nil, errors.New("input image part is invalid")
			}
			parsed, err := parseReferenceDataURL(part.ImageURL)
			if err != nil {
				return "", nil, err
			}
			if reference != nil {
				return "", nil, invalidImage("image generation accepts at most one reference image")
			}
			reference = parsed
		default:
			return "", nil, fmt.Errorf("unsupported content part type %q", header.Type)
		}
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", nil, errors.New("content requires text; generated pixels alone have no conversation memory")
	}
	return output.String(), reference, nil
}
