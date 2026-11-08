// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/kamenxrider/hollis/internal/jsonoutput"
	"github.com/kamenxrider/hollis/internal/runner"
)

// Validate the optional label without retaining its contents in the request.
type ignoredUser struct{}

func (*ignoredUser) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '"' {
		return errors.New("user must be a string")
	}
	var value string
	return json.Unmarshal(data, &value)
}

func (r chatCompletionsRequest) validateCompatibility() (bool, error) {
	if len(r.N) > 0 {
		var n int
		if json.Unmarshal(r.N, &n) != nil || n != 1 {
			return false, unsupportedParameter("n must be 1; multiple completions are not supported")
		}
	}
	if !isAbsentOrNull(r.Logprobs) {
		var enabled bool
		if json.Unmarshal(r.Logprobs, &enabled) != nil || enabled {
			return false, unsupportedParameter("logprobs must be false or null; log probabilities are not supported")
		}
	}
	if !isAbsentOrNull(r.TopLogprobs) {
		return false, unsupportedParameter("top_logprobs must be absent or null")
	}
	if len(r.StreamOptions) == 0 {
		return false, nil
	}
	if !r.Stream {
		return false, unsupportedParameter("stream_options requires stream:true")
	}
	var options struct {
		IncludeUsage bool `json:"include_usage"`
	}
	if isNullJSON(r.StreamOptions) || strictUnmarshal(r.StreamOptions, &options) != nil {
		return false, unsupportedParameter("stream_options only supports the boolean include_usage")
	}
	return options.IncludeUsage, nil
}

func jsonObjectFormat(raw json.RawMessage) bool {
	var format chatResponseFormat
	return json.Unmarshal(raw, &format) == nil && format.Type == "json_object"
}

func (s *Server) handleChatJSONObject(w http.ResponseWriter, r *http.Request, request chatCompletionsRequest) {
	var format chatResponseFormat
	if strictUnmarshal(request.ResponseFormat, &format) != nil || len(format.JSONSchema) != 0 {
		writeRequestValidationError(w, unsupportedParameter("json_object only supports response_format.type"))
		return
	}
	if request.Stream {
		writeRequestValidationError(w, unsupportedParameter("guided output is complete-only; stream must be false"))
		return
	}
	if len(request.ImageGeneration) != 0 || (toolMembers{request.Tools, request.ToolChoice, request.ParallelToolCalls}).present() {
		writeRequestValidationError(w, unsupportedParameter("json_object cannot be combined with tools or image generation"))
		return
	}
	instructions := ""
	local := request.Model == string(runner.ModelLocal)
	if !local {
		instructions = jsonoutput.Instruction
	}
	prepared, err := prepareChatImageRequestWithInstructions(request, instructions)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if err := guidedPrompt(prepared); err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if local {
		prepared.Prompt = jsonoutput.Instruction + "\n\n" + prepared.Prompt
	}
	prepared.Options = request.Options
	model, ok := s.validatePreparedModel(w, prepared)
	if !ok || !validatePrompt(w, prepared.Prompt) {
		return
	}
	var completion runner.Completion
	if local {
		completion, ok = s.completePrepared(r.Context(), w, model, prepared)
	} else {
		completion, ok = s.completeShortcutJSONObject(r, w, model, prepared.Prompt)
	}
	if !ok {
		return
	}
	if local {
		// Preserve Local's existing complete-text validation, including its
		// refusal of fences; it has no Shortcut fence-unwrapping concession.
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(completion.Text), &object) != nil || object == nil {
			writeAPIError(w, http.StatusBadGateway, "server_error", "invalid_json_output", "Local did not return a valid JSON object; no partial output was returned")
			return
		}
	} else {
		object, valid := jsonoutput.Object(completion.Text)
		if !valid {
			writeJSONObjectError(w, completion.Text)
			return
		}
		completion.Text = object
	}
	result := map[string]any{
		"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": string(model),
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": completion.Text}, "finish_reason": finishReason(completion.Usage, request.Options)}},
	}
	if completion.Usage != nil {
		result["usage"] = chatUsage(completion.Usage)
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSONObjectError(w http.ResponseWriter, raw string) {
	contract := runner.ErrorContractFor(runner.KindInvalidJSONOutput)
	writeJSON(w, contract.HTTPStatus, map[string]any{"error": map[string]any{
		"type": contract.HTTPType, "code": contract.HTTPCode, "message": contract.HTTPMessage,
		"details": map[string]any{"raw_response": raw},
	}})
}

func (s *Server) completeShortcutJSONObject(r *http.Request, w http.ResponseWriter, model runner.Model, prompt string) (runner.Completion, bool) {
	release, ok := s.acquireCapacity(w)
	if !ok {
		return runner.Completion{}, false
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), runner.MaxTimeout)
	defer cancel()
	text, used, err := s.Runner.Run(ctx, model, prompt)
	var classified *runner.Error
	if errors.As(err, &classified) && classified.Kind == runner.KindNoOutput {
		writeJSONObjectError(w, text)
		return runner.Completion{}, false
	}
	text, used, ok = writeRunResult(w, model, text, used, err)
	return runner.Completion{Text: text, Model: used}, ok
}
