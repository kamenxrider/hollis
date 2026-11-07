// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package server exposes Hollis's deliberately small OpenAI-compatible HTTP
// surface. Native local supports genuine streaming and measured complete-response
// usage; Shortcuts routes return complete text without simulated usage.
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

const (
	MaxRequestBytes = 8 << 20
	MaxPromptBytes  = 128 << 10
)

// Server serves the local API. Available is fail-closed when non-nil: only
// explicitly true tiers are offered. Auto is a strategy and remains selectable.
type Server struct {
	Runner runner.Runner
	Token  string
	// AllowUnauthenticated must be set explicitly for a loopback-only server.
	// New fails closed when Token is empty so embedders cannot accidentally
	// expose model execution without authentication.
	AllowUnauthenticated bool
	Available            map[string]bool
	// LocalStatus is the non-generative startup probe used by model capabilities.
	LocalStatus runner.LocalStatus
	// LocalStatusError is the classified failure of the startup native status
	// probe, nil when it succeeded or never ran. A protocol/version mismatch is
	// kept so the local routes can report the mismatch itself instead of the
	// generic unavailability the Available gate would give; every other error
	// stays behind that gate, where a missing helper is an availability fact.
	LocalStatusError error
	MaxConcurrency   int
	ImageGenerator   imagegen.Generator
	// ImageBridge is one parameterized JSON Shortcut supporting every style.
	ImageBridge string
	// ImageBridges is an explicit style-to-Shortcut allowlist. HTTP callers
	// select a style ID and can never provide a bridge name or filesystem path.
	ImageBridges map[string]string

	capacityOnce sync.Once
	capacity     chan struct{}
	stageImages  imageStagingFunc
	cleanupImage func(imagegen.Result) error
}

func New(r runner.Runner, token string) *Server {
	return &Server{Runner: r, Token: token, MaxConcurrency: 1}
}

// NewUnauthenticated returns an explicitly unauthenticated server. Callers are
// responsible for binding it only to a resolved loopback address.
func NewUnauthenticated(r runner.Runner) *Server {
	server := New(r, "")
	server.AllowUnauthenticated = true
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.method(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/v1/models", s.method(http.MethodGet, s.guard(s.handleModels)))
	mux.HandleFunc("/v1/chat/completions", s.method(http.MethodPost, s.guard(s.handleChatCompletions)))
	mux.HandleFunc("/v1/responses", s.method(http.MethodPost, s.guard(s.handleResponses)))
	mux.HandleFunc("/v1/images/generations", s.method(http.MethodPost, s.guard(s.handleImageGenerations)))
	return mux
}

func (s *Server) method(allowed string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != allowed {
			w.Header().Set("Allow", allowed)
			writeAPIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "invalid_method", "method not allowed")
			return
		}
		next(w, r)
	}
}

func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" && !s.AllowUnauthenticated {
			writeAPIError(w, http.StatusUnauthorized, "authentication_error", "authentication_required", "bearer token authentication is required")
			return
		}
		if s.Token == "" {
			if !loopbackAuthority(r.Host) {
				writeAPIError(w, http.StatusForbidden, "permission_error", "invalid_host", "unauthenticated requests require a loopback Host")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !loopbackAuthority(parsed.Host) {
					writeAPIError(w, http.StatusForbidden, "permission_error", "invalid_origin", "unauthenticated requests require a loopback Origin")
					return
				}
			}
		}
		if s.Token != "" {
			got := r.Header.Get("Authorization")
			want := "Bearer " + s.Token
			gotHash := sha256.Sum256([]byte(got))
			wantHash := sha256.Sum256([]byte(want))
			if subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) != 1 {
				writeAPIError(w, http.StatusUnauthorized, "authentication_error", "invalid_token", "invalid or missing bearer token")
				return
			}
		}
		if r.Method == http.MethodPost {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				writeAPIError(w, http.StatusUnsupportedMediaType, "invalid_request_error", "unsupported_media_type", "Content-Type must be application/json")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
		}
		next(w, r)
	}
}

func loopbackAuthority(authority string) bool {
	host := authority
	if parsed, port, err := net.SplitHostPort(authority); err == nil {
		if port == "" {
			return false
		}
		host = parsed
	} else if authority == "[::1]" {
		host = "::1"
	}
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	data := []map[string]any{}
	if s.imageRouteAvailable() {
		data = append(data, map[string]any{"id": "hollis-image", "object": "model", "owned_by": "hollis"})
	}
	if s.modelAvailable(string(runner.ModelAuto)) {
		data = append(data, map[string]any{"id": "auto", "object": "model", "owned_by": "hollis"})
	}
	local, _ := runner.Spec(runner.ModelLocal)
	for _, spec := range append(runner.Specs(), local) {
		id := string(spec.ID)
		if s.modelAvailable(id) {
			data = append(data, map[string]any{"id": id, "object": "model", "owned_by": spec.OwnedBy, "capabilities": modelCapabilities(id, s.LocalStatus, s.LocalStatusError)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) imageRouteAvailable() bool {
	if s.ImageGenerator == nil {
		return false
	}
	if strings.TrimSpace(s.ImageBridge) != "" {
		return true
	}
	for style, bridge := range s.ImageBridges {
		if validImageStyle(style) && strings.TrimSpace(bridge) != "" {
			return true
		}
	}
	return false
}

func (s *Server) modelAvailable(id string) bool {
	if id == string(runner.ModelLocal) {
		if _, ok := s.Runner.(runner.CompleteRunner); !ok {
			return false
		}
	}
	if id == string(runner.ModelAuto) {
		if s.Available == nil {
			return true
		}
		return s.Available[string(runner.ModelCloud)] || s.Available[string(runner.ModelOnDevice)]
	}
	if s.Available == nil {
		return true
	}
	available, ok := s.Available[id]
	return ok && available
}

type rawContent json.RawMessage

type requestValidationError struct {
	status  int
	code    string
	message string
}

func (e *requestValidationError) Error() string { return e.message }

func unsupportedParameter(message string) error {
	return &requestValidationError{status: http.StatusBadRequest, code: "unsupported_parameter", message: message}
}

func invalidImage(message string) error {
	return &requestValidationError{status: http.StatusBadRequest, code: "invalid_image", message: message}
}

func imageLimitExceeded(message string) error {
	return &requestValidationError{status: http.StatusRequestEntityTooLarge, code: "context_too_large", message: message}
}

func (c *rawContent) UnmarshalJSON(b []byte) error {
	*c = rawContent(append([]byte(nil), b...))
	return nil
}

// textPart is one text content part. Responses output parts replayed as input
// may carry empty annotations and logprobs; they hold no text and are ignored.
type textPart struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Annotations json.RawMessage `json:"annotations"`
	Logprobs    json.RawMessage `json:"logprobs"`
}

func (p textPart) checkReplayMembers() error {
	for _, raw := range []json.RawMessage{p.Annotations, p.Logprobs} {
		if !isAbsentOrNull(raw) && !bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
			return unsupportedParameter("content part annotations and logprobs must be empty")
		}
	}
	return nil
}

func (c rawContent) text() (string, error) {
	if len(c) == 0 || bytes.Equal(bytes.TrimSpace(c), []byte("null")) {
		return "", errors.New("content must not be null")
	}
	var value string
	if err := json.Unmarshal(c, &value); err == nil {
		if strings.TrimSpace(value) == "" {
			return "", errors.New("content must not be empty")
		}
		return value, nil
	}
	var parts []textPart
	if err := strictUnmarshal([]byte(c), &parts); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return "", unsupportedParameter("content contains an unsupported parameter")
		}
		return "", errors.New("content must be a string or an array of text parts")
	}
	if len(parts) == 0 {
		return "", errors.New("content parts must not be empty")
	}
	var b strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text", "output_text":
		default:
			return "", errors.New("unsupported content part type")
		}
		if err := part.checkReplayMembers(); err != nil {
			return "", err
		}
		if strings.TrimSpace(part.Text) == "" {
			return "", errors.New("content part text must not be empty")
		}
		b.WriteString(part.Text)
	}
	return b.String(), nil
}

type reqMessage struct {
	Role    string
	Content string
}

type inMessage struct {
	Role    string     `json:"role"`
	Content rawContent `json:"content"`
}

func (m *inMessage) UnmarshalJSON(data []byte) error {
	type plainMessage inMessage
	var decoded plainMessage
	if err := strictUnmarshal(data, &decoded); err != nil {
		return err
	}
	*m = inMessage(decoded)
	return nil
}

func parseMessages(messages []inMessage) ([]reqMessage, error) {
	out := make([]reqMessage, 0, len(messages))
	for i, message := range messages {
		role := message.Role
		switch role {
		case "system", "user", "assistant":
		case "developer":
			role = "system"
		default:
			return nil, fmt.Errorf("messages[%d]: unsupported role", i)
		}
		content, err := message.Content.text()
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		out = append(out, reqMessage{Role: role, Content: content})
	}
	if len(out) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	if out[len(out)-1].Role != "user" {
		return nil, errors.New("the final message must have role \"user\"")
	}
	return out, nil
}

func transcriptFrom(messages []reqMessage) string {
	history := make([]store.Message, 0, len(messages)-1)
	for _, message := range messages[:len(messages)-1] {
		history = append(history, store.Message{Role: message.Role, Content: message.Content})
	}
	return chat.RenderTranscript(history, messages[len(messages)-1].Content)
}

func validatePrompt(w http.ResponseWriter, prompt string) bool {
	if len(prompt) > MaxPromptBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "context_too_large", "rendered prompt exceeds 128 KiB")
		return false
	}
	return true
}

func (s *Server) slots() chan struct{} {
	s.capacityOnce.Do(func() {
		limit := s.MaxConcurrency
		if limit < 1 || limit > 4 {
			limit = 1
		}
		s.capacity = make(chan struct{}, limit)
	})
	return s.capacity
}

func (s *Server) acquireCapacity(w http.ResponseWriter) (func(), bool) {
	select {
	case s.slots() <- struct{}{}:
		return func() { <-s.slots() }, true
	default:
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusTooManyRequests, "rate_limit_error", "server_busy", "model capacity is busy; retry later")
		return nil, false
	}
}

func (s *Server) runModel(ctx context.Context, w http.ResponseWriter, model runner.Model, prompt string) (string, runner.Model, bool) {
	release, ok := s.acquireCapacity(w)
	if !ok {
		return "", model, false
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, runner.MaxTimeout)
	defer cancel()
	text, used, err := s.Runner.Run(ctx, model, prompt)
	return writeRunResult(w, model, text, used, err)
}

func writeRunResult(w http.ResponseWriter, requested runner.Model, text string, used runner.Model, err error) (string, runner.Model, bool) {
	if err == nil {
		return text, used, true
	}
	var runErr *runner.Error
	if errors.As(err, &runErr) {
		switch runErr.Kind {
		case runner.KindRequestDeclined:
			writeShortcutError(w, http.StatusBadGateway, "server_error", runErr)
		case runner.KindShortcutFailed:
			writeShortcutError(w, http.StatusBadGateway, "server_error", runErr)
		case runner.KindTransport:
			writeAPIError(w, http.StatusBadGateway, "server_error", "transport", "the Shortcut transport could not complete the request")
		case runner.KindRateLimited:
			writeShortcutError(w, http.StatusTooManyRequests, "rate_limit_error", runErr)
		case runner.KindContentRefused:
			writeShortcutError(w, http.StatusUnprocessableEntity, "invalid_request_error", runErr)
		case runner.KindAccessRequired:
			writeShortcutError(w, http.StatusForbidden, "permission_error", runErr)
		case runner.KindTimeout, runner.KindContextCanceled:
			writeAPIError(w, http.StatusGatewayTimeout, "server_error", "shortcut_timeout", "the Shortcut model run did not complete before its deadline")
		case runner.KindShortcutMissing:
			writeAPIError(w, http.StatusBadGateway, "server_error", "model_unavailable", "the selected model bridge is unavailable")
		default:
			writeAPIError(w, http.StatusBadGateway, "server_error", "shortcut_failed", "the Shortcut model run failed")
		}
		return "", requested, false
	}
	writeAPIError(w, http.StatusBadGateway, "server_error", "shortcut_failed", "the Shortcut model run failed")
	return "", requested, false
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var request chatCompletionsRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	includeUsage, compatibilityErr := request.validateCompatibility()
	if compatibilityErr != nil {
		writeRequestValidationError(w, compatibilityErr)
		return
	}
	options, samplingErr := parseSampling(request.Model, request.samplingFields, request.MaxTokens, request.ImageGeneration)
	if samplingErr != nil {
		writeRequestValidationError(w, samplingErr)
		return
	}
	request.Options = options
	if jsonObjectFormat(request.ResponseFormat) {
		s.handleChatJSONObject(w, r, request)
		return
	}
	if (toolMembers{request.Tools, request.ToolChoice, request.ParallelToolCalls}).present() {
		s.handleChatTools(w, r, request)
		return
	}
	// Guided output is parsed and validated before any route or capacity
	// decision, so an unsupported schema costs no discovery and no inference.
	schema, err := parseChatGuided(request.ResponseFormat)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if schema != nil {
		s.handleChatGuided(w, r, request, schema)
		return
	}
	if request.Stream && (request.Model != string(runner.ModelLocal) || len(request.ImageGeneration) != 0) {
		unsupportedStreaming(w)
		return
	}
	if len(request.ImageGeneration) != 0 {
		s.handleChatImageGeneration(w, r, request)
		return
	}
	request.KeepPrompt = r.Header.Get("X-Hollis-Keep-Prompt") == "1"
	prepared, err := prepareChatImageRequest(request)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	prepared.Options = request.Options
	prepared.IncludeUsage = includeUsage
	model, ok := s.validatePreparedModel(w, prepared)
	if !ok || !validatePrompt(w, prepared.Prompt) {
		return
	}
	if prepared.PromptAdjusted {
		w.Header().Set("X-Hollis-Prompt-Adjusted", "true")
	}
	if request.Stream {
		s.streamPrepared(w, r, model, prepared, false)
		return
	}
	completion, ok := s.completePrepared(r.Context(), w, model, prepared)
	if !ok {
		return
	}
	result := map[string]any{
		"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": string(completion.Model),
		"choices": []map[string]any{{
			"index": 0, "message": map[string]any{"role": "assistant", "content": completion.Text}, "finish_reason": finishReason(completion.Usage, request.Options),
		}},
	}
	if completion.Usage != nil {
		result["usage"] = chatUsage(completion.Usage)
	}
	if prepared.PromptAdjusted {
		result["prompt_adjusted"] = true
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	var request responsesRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	options, samplingErr := parseSampling(request.Model, request.samplingFields, request.MaxOutputTokens, request.ImageGeneration)
	if samplingErr != nil {
		writeRequestValidationError(w, samplingErr)
		return
	}
	request.Options = options
	if (toolMembers{request.Tools, request.ToolChoice, request.ParallelToolCalls}).present() {
		s.handleResponsesTools(w, r, request)
		return
	}
	schema, err := parseResponsesGuided(request.Text)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if schema != nil {
		s.handleResponsesGuided(w, r, request, schema)
		return
	}
	if request.Stream && (request.Model != string(runner.ModelLocal) || len(request.ImageGeneration) != 0) {
		unsupportedStreaming(w)
		return
	}
	if len(request.ImageGeneration) != 0 {
		s.handleResponsesImageGeneration(w, r, request)
		return
	}
	request.KeepPrompt = r.Header.Get("X-Hollis-Keep-Prompt") == "1"
	prepared, err := prepareResponsesImageRequest(request)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	prepared.Options = request.Options
	model, ok := s.validatePreparedModel(w, prepared)
	if !ok || !validatePrompt(w, prepared.Prompt) {
		return
	}
	if prepared.PromptAdjusted {
		w.Header().Set("X-Hollis-Prompt-Adjusted", "true")
	}
	if request.Stream {
		s.streamPrepared(w, r, model, prepared, true)
		return
	}
	completion, ok := s.completePrepared(r.Context(), w, model, prepared)
	if !ok {
		return
	}
	result := map[string]any{
		"id": "resp_" + randomID(), "object": "response", "created_at": time.Now().Unix(), "model": string(completion.Model), "status": "completed",
		"output": []map[string]any{{
			"type": "message", "id": "msg_" + randomID(), "role": "assistant", "status": "completed",
			"content": []map[string]any{{"type": "output_text", "text": completion.Text, "annotations": []any{}}},
		}},
	}
	if completion.Usage != nil {
		result["usage"] = responsesUsage(completion.Usage)
	}
	responseTermination(result, finishReason(completion.Usage, request.Options))
	if prepared.PromptAdjusted {
		result["prompt_adjusted"] = true
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) validatePreparedModel(w http.ResponseWriter, request preparedRequest) (runner.Model, bool) {
	name := request.Model
	if name == "" && request.hasImages() {
		name = string(runner.ModelCloud)
	}
	model, ok := s.validateModel(w, name)
	if !ok || !request.hasImages() {
		return model, ok
	}

	spec, known := runner.Spec(model)
	if !known && model != runner.ModelAuto {
		writeRequestValidationError(w, unsupportedParameter("the selected model does not support image input"))
		return "", false
	}
	if !model.SupportsImages() {
		writeRequestValidationError(w, unsupportedParameter("image input requires local, cloud, cloud-pro, or chatgpt"))
		return "", false
	}
	limit := spec.Capabilities.MaxImages
	if len(request.Images) > limit {
		writeRequestValidationError(w, unsupportedParameter(fmt.Sprintf("model %q accepts at most %d image(s)", model, limit)))
		return "", false
	}
	return model, true
}

func (s *Server) runPrepared(ctx context.Context, w http.ResponseWriter, model runner.Model, request preparedRequest) (string, runner.Model, bool) {
	if !request.hasImages() {
		return s.runModel(ctx, w, model, request.Prompt)
	}
	imageRunner, ok := s.Runner.(runner.ImageRunner)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "model_unavailable", "the configured runner does not support image input")
		return "", model, false
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return "", model, false
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, runner.MaxTimeout)
	defer cancel()
	stager := s.stageImages
	if stager == nil {
		stager = stageImages
	}
	staged, err := stager(ctx, request.Images)
	if err != nil {
		var validationErr *requestValidationError
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeAPIError(w, http.StatusGatewayTimeout, "server_error", "shortcut_timeout", "the image request did not complete before its deadline")
		case errors.As(err, &validationErr):
			writeRequestValidationError(w, err)
		default:
			writeAPIError(w, http.StatusInternalServerError, "server_error", "image_staging_failed", "image input could not be prepared")
		}
		return "", model, false
	}
	defer staged.Cleanup()

	text, used, err := imageRunner.RunWithImages(ctx, model, request.Prompt, staged.Paths)
	return writeRunResult(w, model, text, used, err)
}

func (s *Server) validateModel(w http.ResponseWriter, name string) (runner.Model, bool) {
	if name == "" {
		name = string(runner.ModelAuto)
	}
	model := runner.Model(name)
	if !model.Valid() {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter", "unsupported model")
		return "", false
	}
	// Preserve a startup protocol diagnosis rather than reducing it to an
	// availability fact. Malformed output does not establish a mismatch.
	if model == runner.ModelLocal && s.writeLocalProtocolFailure(w) {
		return "", false
	}
	if !s.modelAvailable(name) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "model_unavailable", "the selected model is unavailable")
		return "", false
	}
	return model, true
}

// localMismatchMessage is the HTTP sentence for a paired helper the startup
// status probe classified as a protocol/version mismatch. The shared
// native_protocol sentence describes a malformed payload from a correctly
// paired helper, which is not this case: the helper answered validly, in
// another protocol. The remedy matches the CLI's runner error.
const localMismatchMessage = "the native local helper does not match this Hollis version; install the matching bundle"

// writeLocalProtocolFailure preserves both malformed protocol and positively
// identified helper mismatches. Other failures keep the availability contract.
func (s *Server) writeLocalProtocolFailure(w http.ResponseWriter) bool {
	var classified *runner.Error
	if !errors.As(s.LocalStatusError, &classified) || classified.Kind != runner.KindNativeProtocol {
		return false
	}
	writeNativeError(w, s.LocalStatusError)
	return true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "context_too_large", "request body exceeds 8 MiB")
		} else if strings.Contains(err.Error(), "unknown field") {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter", "request contains an unsupported parameter")
		} else {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json", "request body is not valid JSON")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "context_too_large", "request body exceeds 8 MiB")
		} else {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json", "request body must contain exactly one JSON value")
		}
		return false
	}
	return true
}

func strictUnmarshal(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func unsupportedStreaming(w http.ResponseWriter) {
	writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter", "streaming is not supported")
}

func writeRequestValidationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "invalid_input"
	message := err.Error()
	var validationErr *requestValidationError
	if errors.As(err, &validationErr) {
		if validationErr.status != 0 {
			status = validationErr.status
		}
		code = validationErr.code
		message = validationErr.message
	}
	writeAPIError(w, status, "invalid_request_error", code, message)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, errorType, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": message, "type": errorType, "code": code},
	})
}

func writeShortcutError(w http.ResponseWriter, status int, errorType string, failure *runner.Error) {
	value := map[string]any{"message": failure.PublicShortcutMessage(), "type": errorType, "code": string(failure.Kind)}
	if details := failure.ShortcutDetails(); len(details) != 0 {
		value["details"] = details
	}
	writeJSON(w, status, map[string]any{"error": value})
}

func randomID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(data)
}
