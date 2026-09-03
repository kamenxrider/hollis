// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package server exposes Hollis's deliberately small OpenAI-compatible HTTP
// surface. The Shortcuts transport returns complete text, so streaming and
// token usage are never simulated.
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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
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
	Runner         runner.Runner
	Token          string
	Available      map[string]bool
	MaxConcurrency int

	capacityOnce sync.Once
	capacity     chan struct{}
}

func New(r runner.Runner, token string) *Server {
	return &Server{Runner: r, Token: token, MaxConcurrency: 1}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.method(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/v1/models", s.method(http.MethodGet, s.guard(s.handleModels)))
	mux.HandleFunc("/v1/chat/completions", s.method(http.MethodPost, s.guard(s.handleChatCompletions)))
	mux.HandleFunc("/v1/responses", s.method(http.MethodPost, s.guard(s.handleResponses)))
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

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	data := []map[string]any{}
	if s.modelAvailable(string(runner.ModelAuto)) {
		data = append(data, map[string]any{"id": "auto", "object": "model", "owned_by": "hollis"})
	}
	owned := map[string]string{
		"cloud": "Apple", "cloud-pro": "Apple", "on-device": "Apple", "chatgpt": "OpenAI",
	}
	for _, id := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		if s.modelAvailable(id) {
			data = append(data, map[string]any{"id": id, "object": "model", "owned_by": owned[id]})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) modelAvailable(id string) bool {
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
	code    string
	message string
}

func (e *requestValidationError) Error() string { return e.message }

func unsupportedParameter(message string) error {
	return &requestValidationError{code: "unsupported_parameter", message: message}
}

func (c *rawContent) UnmarshalJSON(b []byte) error {
	*c = rawContent(append([]byte(nil), b...))
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
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
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
			return "", fmt.Errorf("unsupported content part type %q", part.Type)
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
			return nil, fmt.Errorf("messages[%d]: unsupported role %q", i, message.Role)
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

func (s *Server) runModel(ctx context.Context, w http.ResponseWriter, model runner.Model, prompt string) (string, runner.Model, bool) {
	select {
	case s.slots() <- struct{}{}:
		defer func() { <-s.slots() }()
	default:
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusTooManyRequests, "rate_limit_error", "server_busy", "model capacity is busy; retry later")
		return "", model, false
	}

	ctx, cancel := context.WithTimeout(ctx, runner.MaxTimeout)
	defer cancel()
	text, used, err := s.Runner.Run(ctx, model, prompt)
	if err == nil {
		return text, used, true
	}
	var runErr *runner.Error
	if errors.As(err, &runErr) {
		switch runErr.Kind {
		case runner.KindRateLimited:
			w.Header().Set("Retry-After", "1")
			writeAPIError(w, http.StatusTooManyRequests, "rate_limit_error", "rate_limited", "Apple Intelligence is rate limited")
		case runner.KindTimeout, runner.KindContextCanceled:
			writeAPIError(w, http.StatusGatewayTimeout, "server_error", "shortcut_timeout", "the Shortcut model run did not complete before its deadline")
		case runner.KindShortcutMissing:
			writeAPIError(w, http.StatusBadGateway, "server_error", "model_unavailable", "the selected model bridge is unavailable")
		default:
			writeAPIError(w, http.StatusBadGateway, "server_error", "shortcut_failed", "the Shortcut model run failed")
		}
		return "", model, false
	}
	writeAPIError(w, http.StatusBadGateway, "server_error", "shortcut_failed", "the Shortcut model run failed")
	return "", model, false
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model    string      `json:"model"`
		Messages []inMessage `json:"messages"`
		Stream   bool        `json:"stream"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Stream {
		unsupportedStreaming(w)
		return
	}
	model, ok := s.validateModel(w, request.Model)
	if !ok {
		return
	}
	messages, err := parseMessages(request.Messages)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	prompt := transcriptFrom(messages)
	if !validatePrompt(w, prompt) {
		return
	}
	text, used, ok := s.runModel(r.Context(), w, model, prompt)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": string(used),
		"choices": []map[string]any{{
			"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop",
		}},
	})
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model        string     `json:"model"`
		Instructions string     `json:"instructions"`
		Input        rawContent `json:"input"`
		Stream       bool       `json:"stream"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Stream {
		unsupportedStreaming(w)
		return
	}
	model, ok := s.validateModel(w, request.Model)
	if !ok {
		return
	}
	if len(request.Input) == 0 || bytes.Equal(bytes.TrimSpace(request.Input), []byte("null")) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_input", "input is required and must not be null")
		return
	}
	var messages []reqMessage
	var input string
	if err := json.Unmarshal(request.Input, &input); err == nil {
		if strings.TrimSpace(input) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_input", "input must not be empty")
			return
		}
		messages = []reqMessage{{Role: "user", Content: input}}
	} else {
		var incoming []inMessage
		if err := strictUnmarshal([]byte(request.Input), &incoming); err != nil {
			if strings.Contains(err.Error(), "unknown field") {
				writeRequestValidationError(w, unsupportedParameter("input contains an unsupported parameter"))
			} else {
				writeRequestValidationError(w, errors.New("input must be a string or an array of messages"))
			}
			return
		}
		var parseErr error
		messages, parseErr = parseMessages(incoming)
		if parseErr != nil {
			writeRequestValidationError(w, parseErr)
			return
		}
	}
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append([]reqMessage{{Role: "system", Content: request.Instructions}}, messages...)
	}
	prompt := transcriptFrom(messages)
	if !validatePrompt(w, prompt) {
		return
	}
	text, used, ok := s.runModel(r.Context(), w, model, prompt)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "resp_" + randomID(), "object": "response", "created_at": time.Now().Unix(), "model": string(used), "status": "completed",
		"output": []map[string]any{{
			"type": "message", "id": "msg_" + randomID(), "role": "assistant", "status": "completed",
			"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
	})
}

func (s *Server) validateModel(w http.ResponseWriter, name string) (runner.Model, bool) {
	if name == "" {
		name = string(runner.ModelAuto)
	}
	model := runner.Model(name)
	if !model.Valid() {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter", fmt.Sprintf("unsupported model %q", name))
		return "", false
	}
	if !s.modelAvailable(name) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "model_unavailable", "the selected model is unavailable")
		return "", false
	}
	return model, true
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
	code := "invalid_input"
	message := err.Error()
	var validationErr *requestValidationError
	if errors.As(err, &validationErr) {
		code = validationErr.code
		message = validationErr.message
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_request_error", code, message)
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

func randomID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(data)
}
