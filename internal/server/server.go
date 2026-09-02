// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package server exposes the local OpenAI-compatible HTTP endpoint
// (plan §19): GET /health, GET /v1/models, POST /v1/chat/completions and
// POST /v1/responses. Every request is a stateless translation from the
// OpenAI shapes into the tested replay-transcript format plus one
// Shortcuts call. Streaming is unsupported by design (plan principle 6:
// no fake streaming — the transport returns the whole response in one
// call), and token counts are never invented (principle 5).
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

// Server serves the HTTP API. Runner must be safe for concurrent use;
// Token enables Bearer auth on /v1 endpoints (required for any
// non-loopback bind, plan §30). Available gates the catalog: when set, a
// tier id missing from it (or present with false) is not offered; nil
// means every tier is available (the pre-resolution behavior).
type Server struct {
	Runner runner.Runner
	Token  string
	// Available maps tier id -> bridge resolved. nil = all available.
	Available map[string]bool
}

// New returns a Server.
func New(r runner.Runner, token string) *Server { return &Server{Runner: r, Token: token} }

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/models", s.guard(s.handleModels))
	mux.HandleFunc("/v1/chat/completions", s.guard(s.handleChatCompletions))
	mux.HandleFunc("/v1/responses", s.guard(s.handleResponses))
	return mux
}

// guard enforces Bearer auth when a token is configured.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
			writeError(w, http.StatusUnauthorized, "invalid or missing Authorization header")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	// Catalog = what resolves on this machine (results/macos-26-compat.md
	// step 2): auto plus tiers whose bridges resolve. ChatGPT is OpenAI's
	// model exposed through Apple's extension — owned_by must not claim
	// Apple.
	data := []map[string]any{
		{"id": "auto", "object": "model", "owned_by": "hollis"},
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

// modelAvailable reports whether an explicit tier may be invoked. A nil
// Available map means no gating (all tiers offered); tiers absent from
// the map (auto) are always available.
func (s *Server) modelAvailable(id string) bool {
	if s.Available == nil {
		return true
	}
	av, ok := s.Available[id]
	return !ok || av
}

// rawContent is a message content field that may be a plain string or an
// array of typed parts ({"type": "text"|"input_text"|"output_text",
// "text": "..."}). Custom unmarshaling captures the raw JSON bytes: the
// default RawMessage behavior would base64-decode string values.
type rawContent json.RawMessage

// UnmarshalJSON captures the raw JSON bytes verbatim.
func (c *rawContent) UnmarshalJSON(b []byte) error {
	*c = rawContent(append([]byte(nil), b...))
	return nil
}

// text extracts the plain text from a string or typed-parts content.
func (c rawContent) text() (string, error) {
	if len(c) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(c, &s); err == nil {
		return s, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(c, &parts); err != nil {
		return "", fmt.Errorf("content must be a string or an array of text parts")
	}
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "", "text", "input_text", "output_text":
			b.WriteString(p.Text)
		default:
			return "", fmt.Errorf("unsupported content part type %q", p.Type)
		}
	}
	return b.String(), nil
}

// reqMessage is one inbound chat message after normalization.
type reqMessage struct {
	Role    string
	Content string
}

// parseMessages normalizes an inbound messages array. "developer" is
// accepted as OpenAI's newer spelling of "system". The final message
// must be a user message — that is the turn the model is asked to
// respond to.
func parseMessages(msgs []inMessage) ([]reqMessage, error) {
	out := make([]reqMessage, 0, len(msgs))
	for i, m := range msgs {
		role := m.Role
		switch role {
		case "system", "user", "assistant":
		case "developer":
			role = "system"
		default:
			return nil, fmt.Errorf("messages[%d]: unsupported role %q", i, m.Role)
		}
		text, err := m.Content.text()
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		out = append(out, reqMessage{Role: role, Content: text})
	}
	if len(out) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	if out[len(out)-1].Role != "user" {
		return nil, errors.New("the final message must have role \"user\"")
	}
	if strings.TrimSpace(out[len(out)-1].Content) == "" {
		return nil, errors.New("input must not be empty")
	}
	return out, nil
}

// transcript converts a normalized message list into the tested replay
// transcript (plan §13): history is everything before the final user
// message, which is passed as the new turn.
func transcriptFrom(msgs []reqMessage) string {
	history := make([]store.Message, 0, len(msgs)-1)
	for _, m := range msgs[:len(msgs)-1] {
		history = append(history, store.Message{Role: m.Role, Content: m.Content})
	}
	return chat.RenderTranscript(history, msgs[len(msgs)-1].Content)
}

// runModel invokes the transport with the per-call deadline policy used
// across hollis (the runner clamps to the 120s ceiling). ctx comes from the
// request: a disconnected client cancels the run instead of leaving an
// orphaned shortcut process for up to the ceiling.
func (s *Server) runModel(ctx context.Context, w http.ResponseWriter, model runner.Model, prompt string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, runner.DefaultTimeout)
	defer cancel()
	text, err := s.Runner.Run(ctx, model, prompt)
	if err == nil {
		return text, true
	}
	var re *runner.Error
	if errors.As(err, &re) && re.Kind == runner.KindTimeout {
		writeError(w, http.StatusGatewayTimeout, "the model run exceeded its deadline and was killed")
		return "", false
	}
	writeError(w, http.StatusBadGateway, "the model run failed: "+err.Error())
	return "", false
}

type inMessage struct {
	Role    string     `json:"role"`
	Content rawContent `json:"content"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Model    string      `json:"model"`
		Messages []inMessage `json:"messages"`
		Stream   bool        `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Stream {
		unsupportedStreaming(w)
		return
	}
	model := req.Model
	if model == "" {
		model = string(runner.ModelAuto)
	}
	if !runner.Model(model).Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown model %q: choose auto, cloud, cloud-pro, on-device, or chatgpt", model))
		return
	}
	if !s.modelAvailable(model) {
		writeError(w, http.StatusBadRequest, unavailableMessage(runner.Model(model)))
		return
	}
	msgs, err := parseMessages(req.Messages)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	text, ok := s.runModel(r.Context(), w, runner.Model(model), transcriptFrom(msgs))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      newID("chatcmpl-"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
	})
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Model        string     `json:"model"`
		Instructions string     `json:"instructions"`
		Input        rawContent `json:"input"`
		Stream       bool       `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Stream {
		unsupportedStreaming(w)
		return
	}
	model := req.Model
	if model == "" {
		model = string(runner.ModelAuto)
	}
	if !runner.Model(model).Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown model %q: choose auto, cloud, cloud-pro, on-device, or chatgpt", model))
		return
	}
	if !s.modelAvailable(model) {
		writeError(w, http.StatusBadRequest, unavailableMessage(runner.Model(model)))
		return
	}

	// input is a string or an array of {role, content} messages. Try the
	// string shape first, then the array explicitly — an array of message
	// objects also unmarshals as empty text parts, so error-based
	// dispatch would silently swallow it.
	var msgs []reqMessage
	if len(req.Input) == 0 {
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}
	var inputString string
	if strErr := json.Unmarshal(req.Input, &inputString); strErr == nil {
		msgs = []reqMessage{{Role: "user", Content: inputString}}
	} else {
		var arr []inMessage
		if arrErr := json.Unmarshal(req.Input, &arr); arrErr != nil {
			writeError(w, http.StatusBadRequest, "input must be a string or an array of messages")
			return
		}
		var parseErr error
		if msgs, parseErr = parseMessages(arr); parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
	}
	if req.Instructions != "" {
		msgs = append([]reqMessage{{Role: "system", Content: req.Instructions}}, msgs...)
	}

	text, ok := s.runModel(r.Context(), w, runner.Model(model), transcriptFrom(msgs))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         newID("resp_"),
		"object":     "response",
		"created_at": time.Now().Unix(),
		"model":      model,
		"status":     "completed",
		"output": []map[string]any{{
			"type":   "message",
			"id":     newID("msg_"),
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{{
				"type":        "output_text",
				"text":        text,
				"annotations": []any{},
			}},
		}},
	})
}

// unavailableMessage renders the CLI's stable wording for an explicit
// tier whose bridge did not resolve (results/macos-26-compat.md step 2).
// The canonical wording lives in runner.UnavailableErr — never copy it.
func unavailableMessage(m runner.Model) string {
	return runner.UnavailableErr(m).Error()
}

// unsupportedStreaming answers stream=true with a clear unsupported
// error. Faking streaming by splitting a complete response is forbidden
// (plan principle 6).
func unsupportedStreaming(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "streaming is not supported: the Shortcuts transport returns the complete response in a single call (use stream=false)")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": "invalid_request_error"},
	})
}

func newID(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return prefix + hex.EncodeToString(b)
}
