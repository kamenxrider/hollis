// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

// Guided output over HTTP uses the same subset parser, the same schema runner
// and the same payload re-validation as the CLI. Only the native local route
// implements guided output, so the request is refused for every other route
// before any capacity is taken or process spawned.

// chatResponseFormat is Chat Completions' response_format member. The nested
// json_schema object is decoded separately so its unknown fields can be
// rejected without weakening the top-level strict decode.
type chatResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema"`
}

// chatJSONSchema is response_format.json_schema. `name` is required like the
// upstream shape, `description` is optional, and both are labels only: Hollis
// never uses them for validation, caching or generation.
type chatJSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Strict      *bool           `json:"strict"`
}

// responsesText is the Responses endpoint's text member. Only `format` is
// supported; other upstream text parameters (such as verbosity) are refused
// rather than ignored.
type responsesText struct {
	Format json.RawMessage `json:"format"`
}

// responsesFormat is text.format, which repeats the type discriminator inside
// the object, unlike Chat Completions.
type responsesFormat struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Strict      *bool           `json:"strict"`
}

func invalidResponseFormat(message string) error {
	return &requestValidationError{status: http.StatusBadRequest, code: "invalid_response_format", message: message}
}

func invalidSchema(message string) error {
	return &requestValidationError{status: http.StatusBadRequest, code: "invalid_schema", message: message}
}

// parseChatGuided returns the parsed schema, or nil when the request asks for
// the existing plain-text behavior. A malformed or unsupported shape is an
// error, never a silent fallback to text.
func parseChatGuided(raw json.RawMessage) (*runner.Schema, error) {
	if isAbsentJSON(raw) {
		return nil, nil
	}
	if isNullJSON(raw) {
		return nil, invalidResponseFormat("response_format must be an object")
	}
	var format chatResponseFormat
	if err := strictUnmarshal(raw, &format); err != nil {
		return nil, responseFormatDecodeError(err, "response_format")
	}
	if format.Type == "" {
		return nil, invalidResponseFormat("response_format.type is required")
	}
	switch format.Type {
	case "text":
		if len(format.JSONSchema) != 0 {
			return nil, unsupportedParameter("response_format.json_schema is only supported with type \"json_schema\"")
		}
		return nil, nil
	case "json_schema":
		if len(format.JSONSchema) == 0 || isNullJSON(format.JSONSchema) {
			return nil, invalidResponseFormat("response_format.json_schema is required with type \"json_schema\"")
		}
	default:
		return nil, unsupportedParameter(fmt.Sprintf(
			"unsupported response_format type %q; only \"json_schema\" is supported", format.Type))
	}
	var inner chatJSONSchema
	if err := strictUnmarshal(format.JSONSchema, &inner); err != nil {
		return nil, responseFormatDecodeError(err, "response_format.json_schema")
	}
	if strings.TrimSpace(inner.Name) == "" {
		return nil, invalidResponseFormat("response_format.json_schema.name must be a non-empty string")
	}
	return parseGuidedSchema(inner.Schema, "response_format.json_schema.schema")
}

// parseResponsesGuided is the Responses counterpart. text.format.type is the
// discriminator, and type "text" keeps today's plain-text behavior.
func parseResponsesGuided(raw json.RawMessage) (*runner.Schema, error) {
	if isAbsentJSON(raw) {
		return nil, nil
	}
	if isNullJSON(raw) {
		return nil, invalidResponseFormat("text must be an object")
	}
	var text responsesText
	if err := strictUnmarshal(raw, &text); err != nil {
		return nil, responseFormatDecodeError(err, "text")
	}
	if len(text.Format) == 0 || isNullJSON(text.Format) {
		return nil, nil
	}
	var format responsesFormat
	if err := strictUnmarshal(text.Format, &format); err != nil {
		return nil, responseFormatDecodeError(err, "text.format")
	}
	if format.Type == "" {
		return nil, invalidResponseFormat("text.format.type is required")
	}
	switch format.Type {
	case "text":
		if len(format.Schema) != 0 {
			return nil, unsupportedParameter("text.format.schema is only supported with type \"json_schema\"")
		}
		return nil, nil
	case "json_schema":
	default:
		return nil, unsupportedParameter(fmt.Sprintf(
			"unsupported text.format type %q; only \"json_schema\" is supported", format.Type))
	}
	if strings.TrimSpace(format.Name) == "" {
		return nil, invalidResponseFormat("text.format.name must be a non-empty string")
	}
	return parseGuidedSchema(format.Schema, "text.format.schema")
}

func parseGuidedSchema(raw json.RawMessage, subject string) (*runner.Schema, error) {
	if len(raw) == 0 || isNullJSON(raw) {
		return nil, invalidResponseFormat(subject + " is required")
	}
	schema, err := runner.ParseSchema(raw)
	if err != nil {
		var failure *runner.SchemaError
		if errors.As(err, &failure) {
			// The same sentence the CLI reports, pointer included. It carries
			// caller strings (property names) with control characters
			// escaped, never raw schema content beyond the pointer.
			return nil, invalidSchema(failure.Error())
		}
		return nil, invalidSchema("schema is not valid JSON")
	}
	return schema, nil
}

// responseFormatDecodeError maps a strict-decoding failure to the same codes
// the rest of the HTTP surface uses: an unknown member is an unsupported
// parameter, and a wrong JSON type is an invalid format shape.
func responseFormatDecodeError(err error, subject string) error {
	if strings.Contains(err.Error(), "unknown field") {
		return unsupportedParameter(subject + " contains an unsupported parameter")
	}
	return invalidResponseFormat(subject + " has an invalid shape")
}

func isAbsentJSON(raw json.RawMessage) bool { return len(raw) == 0 }

func isNullJSON(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// validateGuidedModel refuses every route but native local before any schema
// work runs. auto is a policy that can fall back to a Shortcut, so it is not a
// guided route either.
func (s *Server) validateGuidedModel(w http.ResponseWriter, name string) (runner.Model, bool) {
	if name == "" {
		name = string(runner.ModelAuto)
	}
	model := runner.Model(name)
	if !model.Valid() {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter", fmt.Sprintf("unsupported model %q", name))
		return "", false
	}
	if model != runner.ModelLocal {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter",
			"guided output requires model \"local\"; other routes cannot enforce a schema and never fall back to plain text")
		return "", false
	}
	// Guided output needs the schema interface, not the complete interface a
	// plain local completion needs. A runner without it is a configuration
	// failure, not a usage error, and never a reason to answer in plain text.
	if _, ok := s.Runner.(runner.SchemaRunner); !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "server_error", "model_unavailable",
			"the configured runner does not support guided output")
		return "", false
	}
	if s.Available != nil {
		if available, ok := s.Available[name]; !ok || !available {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "model_unavailable", "the selected model is unavailable")
			return "", false
		}
	}
	spec, known := runner.Spec(model)
	if !known || !spec.Capabilities.Schema {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter", "guided output is not available on the selected model")
		return "", false
	}
	return model, true
}

// completeGuided runs one schema request through the same runner path the CLI
// uses, then re-validates the returned object at the HTTP boundary. A fake or
// future runner that returns a non-conforming object is refused here; no
// partial or repaired value is ever written to the client.
func (s *Server) completeGuided(ctx context.Context, w http.ResponseWriter, prompt string, schema *runner.Schema) (runner.Completion, bool) {
	schemaRunner, ok := s.Runner.(runner.SchemaRunner)
	if !ok {
		writeNativeError(w, &runner.Error{Kind: runner.KindNativeFailed, ExitCode: -1,
			Err: errors.New("configured runner does not support guided output")})
		return runner.Completion{}, false
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return runner.Completion{}, false
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, runner.MaxTimeout)
	defer cancel()
	result, err := schemaRunner.RunSchema(ctx, runner.ModelLocal, prompt, schema)
	if err == nil {
		if result.Model != runner.ModelLocal || len(result.Structured) == 0 {
			err = invalidGuidedOutput()
		} else if validateErr := schema.Validate(result.Structured); validateErr != nil {
			err = invalidGuidedOutput()
		}
	}
	if err != nil {
		writeNativeError(w, err)
		return runner.Completion{}, false
	}
	return result, true
}

func invalidGuidedOutput() error {
	return &runner.Error{Kind: runner.KindNativeProtocol, ExitCode: -1,
		Err: errors.New("guided response failed validation")}
}

// guidedPrompt rejects the combinations the CLI already refuses. Checking them
// here keeps the HTTP surface from accepting a request the CLI would not.
func guidedPrompt(prepared preparedRequest) error {
	if prepared.hasImages() {
		return unsupportedParameter("guided output cannot be combined with image input")
	}
	return nil
}

func (s *Server) handleChatGuided(w http.ResponseWriter, r *http.Request, request chatCompletionsRequest, schema *runner.Schema) {
	if request.Stream {
		writeRequestValidationError(w, unsupportedParameter("guided output is complete-only; stream must be false"))
		return
	}
	if len(request.ImageGeneration) != 0 {
		writeRequestValidationError(w, unsupportedParameter("guided output cannot be combined with image generation"))
		return
	}
	prepared, err := prepareChatImageRequest(request)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if err := guidedPrompt(prepared); err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if _, ok := s.validateGuidedModel(w, prepared.Model); !ok {
		return
	}
	if !validatePrompt(w, prepared.Prompt) {
		return
	}
	completion, ok := s.completeGuided(r.Context(), w, prepared.Prompt, schema)
	if !ok {
		return
	}
	result := map[string]any{
		"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": string(completion.Model),
		"choices": []map[string]any{{
			"index": 0, "message": map[string]any{"role": "assistant", "content": string(completion.Structured)}, "finish_reason": "stop",
		}},
	}
	if completion.Usage != nil {
		result["usage"] = chatUsage(completion.Usage)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleResponsesGuided(w http.ResponseWriter, r *http.Request, request responsesRequest, schema *runner.Schema) {
	if request.Stream {
		writeRequestValidationError(w, unsupportedParameter("guided output is complete-only; stream must be false"))
		return
	}
	if len(request.ImageGeneration) != 0 {
		writeRequestValidationError(w, unsupportedParameter("guided output cannot be combined with image generation"))
		return
	}
	prepared, err := prepareResponsesImageRequest(request)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if err := guidedPrompt(prepared); err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if _, ok := s.validateGuidedModel(w, prepared.Model); !ok {
		return
	}
	if !validatePrompt(w, prepared.Prompt) {
		return
	}
	completion, ok := s.completeGuided(r.Context(), w, prepared.Prompt, schema)
	if !ok {
		return
	}
	result := map[string]any{
		"id": "resp_" + randomID(), "object": "response", "created_at": time.Now().Unix(), "model": string(completion.Model), "status": "completed",
		"output": []map[string]any{{
			"type": "message", "id": "msg_" + randomID(), "role": "assistant", "status": "completed",
			"content": []map[string]any{{"type": "output_text", "text": string(completion.Structured), "annotations": []any{}}},
		}},
	}
	if completion.Usage != nil {
		result["usage"] = responsesUsage(completion.Usage)
	}
	writeJSON(w, http.StatusOK, result)
}
