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

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

// Caller-provided tools on the native local route. A request that carries
// any tool member is handled here and nowhere else, so the plain-text path
// is unchanged. The server never executes a tool, never adds tools of its
// own, and never streams while tools are in scope: every validated call is
// returned to the caller.

// toolMembers are the tool parameters both endpoints share.
type toolMembers struct {
	Tools             json.RawMessage
	ToolChoice        json.RawMessage
	ParallelToolCalls json.RawMessage
}

func (m toolMembers) present() bool {
	return !isAbsentOrNull(m.Tools) || !isAbsentOrNull(m.ToolChoice) || !isAbsentOrNull(m.ParallelToolCalls)
}

func isAbsentOrNull(raw json.RawMessage) bool { return len(raw) == 0 || isNullJSON(raw) }

// parseToolMembers validates the tool parameters before anything is staged
// or spawned. Every rejection is a 400 unsupported_parameter.
func parseToolMembers(m toolMembers, responses ...bool) ([]runner.Tool, runner.ToolChoice, error) {
	if isAbsentOrNull(m.Tools) {
		return nil, "", unsupportedParameter("tool_choice and parallel_tool_calls require tools")
	}
	tools, err := runner.ParseTools(m.Tools, "tools")
	if err != nil {
		return nil, "", unsupportedParameter(err.Error())
	}
	choice := runner.ToolChoiceAuto
	if !isAbsentOrNull(m.ToolChoice) {
		var value string
		if err := json.Unmarshal(m.ToolChoice, &value); err != nil {
			var name string
			if len(responses) > 0 && responses[0] {
				var named struct {
					Type string `json:"type"`
					Name string `json:"name"`
				}
				if strictUnmarshal(m.ToolChoice, &named) != nil || named.Type != "function" {
					return nil, "", unsupportedParameter("tool_choice must name a function with type and name")
				}
				name = named.Name
			} else {
				var named struct {
					Type     string `json:"type"`
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				}
				if strictUnmarshal(m.ToolChoice, &named) != nil || named.Type != "function" {
					return nil, "", unsupportedParameter("tool_choice must name a function with type and function.name")
				}
				name = named.Function.Name
			}
			found := false
			for _, tool := range tools {
				found = found || tool.Name == name
			}
			if !found {
				names := make([]string, 0, len(tools))
				for _, tool := range tools {
					names = append(names, tool.Name)
				}
				return nil, "", unsupportedParameter(runner.ToolChoiceNotOffered(name, names).Error())
			}
			value = "function:" + name
		}
		switch runner.ToolChoice(value) {
		case runner.ToolChoiceAuto, runner.ToolChoiceNone, runner.ToolChoiceRequired:
			choice = runner.ToolChoice(value)
		default:
			if !strings.HasPrefix(value, "function:") || bytes.HasPrefix(bytes.TrimSpace(m.ToolChoice), []byte(`"`)) {
				return nil, "", unsupportedParameter("tool_choice accepts auto, none, required, or a named function object")
			}
			choice = runner.ToolChoice(value)
		}
	}
	if !isAbsentOrNull(m.ParallelToolCalls) {
		var parallel bool
		if err := json.Unmarshal(m.ParallelToolCalls, &parallel); err != nil {
			return nil, "", unsupportedParameter("parallel_tool_calls must be a boolean")
		}
		if !parallel {
			return nil, "", unsupportedParameter("parallel_tool_calls:false is not supported: the model may return several calls in one round")
		}
	}
	return tools, choice, nil
}

// toolCombination refuses the members tools cannot be combined with. Each
// message names both parameters.
func toolCombination(stream bool, imageGeneration json.RawMessage, guided bool, guidedMember string) error {
	switch {
	case stream:
		return unsupportedParameter("tools cannot be combined with stream:true; tool requests are complete-only, retry with stream:false")
	case len(imageGeneration) != 0:
		return unsupportedParameter("tools cannot be combined with image_generation")
	case guided:
		return unsupportedParameter("tools cannot be combined with " + guidedMember + " json_schema: a reply cannot be both a schema object and a tool call")
	}
	return nil
}

// validateToolRoute refuses every route but native local before the tool
// definitions are parsed, so a Shortcuts caller learns that tools are
// unavailable rather than how to fix a definition no route would accept.
func validateToolRoute(w http.ResponseWriter, name string) bool {
	if name == "" {
		name = string(runner.ModelAuto)
	}
	model := runner.Model(name)
	if !model.Valid() {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter", fmt.Sprintf("unsupported model %q", name))
		return false
	}
	if model != runner.ModelLocal {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_parameter",
			"tools is supported only for model \"local\"")
		return false
	}
	return true
}

// validateToolModel checks the native local runner once the request is valid.
func (s *Server) validateToolModel(w http.ResponseWriter, name string) (runner.ToolRoundRunner, bool) {
	if !validateToolRoute(w, name) {
		return nil, false
	}
	if s.writeLocalProtocolFailure(w) {
		return nil, false
	}
	rounds, ok := s.Runner.(runner.ToolRoundRunner)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "server_error", "model_unavailable",
			"the configured runner does not support tool calling")
		return nil, false
	}
	if s.Available != nil {
		if available, known := s.Available[name]; !known || !available {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "model_unavailable", "the selected model is unavailable")
			return nil, false
		}
	}
	return rounds, true
}

// runTools runs the whole loop in one capacity slot under one deadline.
func (s *Server) runTools(ctx context.Context, w http.ResponseWriter, rounds runner.ToolRoundRunner, conversation runner.ToolConversation) (runner.ToolOutcome, bool) {
	release, ok := s.acquireCapacity(w)
	if !ok {
		return runner.ToolOutcome{}, false
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, runner.MaxTimeout)
	defer cancel()
	outcome, err := runner.RunToolLoop(ctx, rounds, runner.ModelLocal, conversation, runner.ReturnToCaller{})
	if err == nil && outcome.Model != runner.ModelLocal {
		err = errors.New("invalid native completion")
	}
	if err == nil && len(outcome.Calls) == 0 && strings.TrimSpace(outcome.Text) == "" {
		err = errors.New("invalid native completion")
	}
	if err != nil {
		writeNativeError(w, err)
		return runner.ToolOutcome{}, false
	}
	return outcome, true
}

// toolHistory accumulates a conversation and checks that every tool result
// answers a call made earlier in it.
type toolHistory struct {
	messages []store.Message
	callIDs  map[string]bool
}

func (h *toolHistory) add(role, content string) {
	h.messages = append(h.messages, store.Message{Role: role, Content: content})
}

func (h *toolHistory) addCalls(calls []chat.ToolCallRecord) {
	if h.callIDs == nil {
		h.callIDs = map[string]bool{}
	}
	for _, call := range calls {
		h.callIDs[call.ID] = true
	}
	h.add("assistant", chat.EncodeToolCalls(calls))
}

func (h *toolHistory) addResult(callID, output, subject string) error {
	if !h.callIDs[callID] {
		return fmt.Errorf("%s references a call id that no earlier tool call carries", subject)
	}
	h.add(chat.RoleTool, chat.EncodeToolResult(callID, output))
	return nil
}

// finish applies the final-message rule and requires every replayed call to be
// answered by a tool result before the next message and before the end.
func (h *toolHistory) finish() ([]store.Message, error) {
	if len(h.messages) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	outstanding := map[string]bool{}
	for _, message := range h.messages {
		if message.Role == chat.RoleTool {
			if callID, _, ok := chat.DecodeToolResult(message.Content); ok {
				delete(outstanding, callID)
			}
			continue
		}
		if len(outstanding) > 0 {
			return nil, errors.New("every tool call must be answered by a tool result before the next message")
		}
		if message.Role == "assistant" {
			if calls, ok := chat.DecodeToolCalls(message.Content); ok {
				for _, call := range calls {
					outstanding[call.ID] = true
				}
			}
		}
	}
	if len(outstanding) > 0 {
		return nil, errors.New("every tool call must be answered by a tool result; the history ends with an unanswered call")
	}
	last := h.messages[len(h.messages)-1]
	if last.Role == "user" || last.Role == chat.RoleTool {
		return h.messages, nil
	}
	return nil, errors.New("the final message must have role \"user\" or be a tool result")
}

// replayedCall validates one prior call a caller replays in its history.
func replayedCall(id, name, arguments, subject string) (chat.ToolCallRecord, error) {
	if strings.TrimSpace(id) == "" {
		return chat.ToolCallRecord{}, fmt.Errorf("%s: id must be a non-empty string", subject)
	}
	if strings.TrimSpace(name) == "" {
		return chat.ToolCallRecord{}, fmt.Errorf("%s: name must be a non-empty string", subject)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &object); err != nil || object == nil {
		return chat.ToolCallRecord{}, fmt.Errorf("%s: arguments must be a JSON object encoded as a string", subject)
	}
	return chat.ToolCallRecord{ID: id, Name: name, Arguments: arguments}, nil
}

// toolOutput reads a tool result's content: a string, possibly empty, or an
// array of text parts.
func toolOutput(raw json.RawMessage, subject string) (string, error) {
	if isAbsentOrNull(raw) {
		return "", fmt.Errorf("%s must be a string", subject)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	content, err := rawContent(raw).text()
	if err != nil {
		return "", fmt.Errorf("%s: %w", subject, err)
	}
	return content, nil
}

// --- Chat Completions ---

type chatToolMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  json.RawMessage `json:"tool_calls"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func parseChatToolMessages(raw json.RawMessage) ([]store.Message, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("messages must not be empty")
	}
	var incoming []json.RawMessage
	if err := strictUnmarshal(trimmed, &incoming); err != nil {
		return nil, errors.New("messages must be an array of messages")
	}
	var history toolHistory
	for index, data := range incoming {
		var message chatToolMessage
		if err := strictUnmarshal(data, &message); err != nil {
			if strings.Contains(err.Error(), "unknown field") {
				return nil, unsupportedParameter("messages contains an unsupported parameter")
			}
			return nil, fmt.Errorf("messages[%d] has an invalid shape", index)
		}
		subject := fmt.Sprintf("messages[%d]", index)
		if chatContentHasImage(rawContent(message.Content)) {
			return nil, unsupportedParameter("tools cannot be combined with image input")
		}
		if message.Role != chat.RoleTool && message.ToolCallID != "" {
			return nil, unsupportedParameter(subject + ": tool_call_id is only supported on role \"tool\"")
		}
		if message.Role != "assistant" && !isAbsentOrNull(message.ToolCalls) {
			return nil, unsupportedParameter(subject + ": tool_calls is only supported on role \"assistant\"")
		}
		switch message.Role {
		case "system", "developer", "user":
			content, err := rawContent(message.Content).text()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", subject, err)
			}
			role := message.Role
			if role == "developer" {
				role = "system"
			}
			history.add(role, content)
		case "assistant":
			if isAbsentOrNull(message.ToolCalls) {
				content, err := rawContent(message.Content).text()
				if err != nil {
					return nil, fmt.Errorf("%s: %w", subject, err)
				}
				history.add("assistant", content)
				continue
			}
			// Content beside tool_calls would be dropped from the replay, so
			// only an empty content is accepted rather than silently lost.
			if !isAbsentOrNull(message.Content) {
				var text string
				if json.Unmarshal(message.Content, &text) != nil || text != "" {
					return nil, unsupportedParameter(subject + ": content beside tool_calls must be null or empty")
				}
			}
			var calls []chatToolCall
			if err := strictUnmarshal(message.ToolCalls, &calls); err != nil || len(calls) == 0 {
				return nil, fmt.Errorf("%s: tool_calls must be a non-empty array of function calls", subject)
			}
			records := make([]chat.ToolCallRecord, 0, len(calls))
			for callIndex, call := range calls {
				callSubject := fmt.Sprintf("%s.tool_calls[%d]", subject, callIndex)
				if call.Type != "function" {
					return nil, fmt.Errorf("%s: type must be \"function\"", callSubject)
				}
				record, err := replayedCall(call.ID, call.Function.Name, call.Function.Arguments, callSubject)
				if err != nil {
					return nil, err
				}
				records = append(records, record)
			}
			history.addCalls(records)
		case chat.RoleTool:
			if message.ToolCallID == "" {
				return nil, fmt.Errorf("%s: tool_call_id is required on role \"tool\"", subject)
			}
			output, err := toolOutput(message.Content, subject+".content")
			if err != nil {
				return nil, err
			}
			if err := history.addResult(message.ToolCallID, output, subject); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%s: unsupported role %q", subject, message.Role)
		}
	}
	return history.finish()
}

func (s *Server) handleChatTools(w http.ResponseWriter, r *http.Request, request chatCompletionsRequest) {
	if !validateToolRoute(w, request.Model) {
		return
	}
	tools, choice, err := parseToolMembers(toolMembers{request.Tools, request.ToolChoice, request.ParallelToolCalls})
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	schema, err := parseChatGuided(request.ResponseFormat)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if err := toolCombination(request.Stream, request.ImageGeneration, schema != nil, "response_format"); err != nil {
		writeRequestValidationError(w, err)
		return
	}
	messages, err := parseChatToolMessages(request.Messages)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	rounds, ok := s.validateToolModel(w, request.Model)
	if !ok {
		return
	}
	outcome, ok := s.runTools(r.Context(), w, rounds, runner.ToolConversation{Tools: tools, Choice: choice, Messages: messages, Options: request.Options})
	if !ok {
		return
	}
	message := map[string]any{"role": "assistant", "content": outcome.Text}
	finish := "stop"
	if len(outcome.Calls) != 0 {
		calls := make([]map[string]any, len(outcome.Calls))
		for index, call := range outcome.Calls {
			calls[index] = map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}}
		}
		message = map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}
		finish = "tool_calls"
	}
	result := map[string]any{
		"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": string(outcome.Model),
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
	}
	if outcome.Usage != nil {
		result["usage"] = chatUsage(outcome.Usage)
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Responses ---

// Responses flattens the function fields; preserve their raw values so the
// shared parser still enforces every field type and the same schema subset.
func normalizeResponsesTools(raw json.RawMessage) (json.RawMessage, error) {
	if isAbsentOrNull(raw) {
		return raw, nil
	}
	var definitions []map[string]json.RawMessage
	if err := strictUnmarshal(raw, &definitions); err != nil {
		return nil, unsupportedParameter("tools must be a JSON array of tools")
	}
	normalized := make([]map[string]any, 0, len(definitions))
	for index, definition := range definitions {
		for key := range definition {
			switch key {
			case "type", "name", "description", "parameters", "strict":
			default:
				return nil, unsupportedParameter(fmt.Sprintf("tools[%d] contains an unsupported member", index))
			}
		}
		kind := definition["type"]
		delete(definition, "type")
		normalized = append(normalized, map[string]any{"type": kind, "function": definition})
	}
	return json.Marshal(normalized)
}

type responsesItemHeader struct {
	Type string `json:"type"`
	Role string `json:"role"`
}

type responsesMessageItem struct {
	Type    string     `json:"type"`
	ID      string     `json:"id"`
	Status  string     `json:"status"`
	Role    string     `json:"role"`
	Content rawContent `json:"content"`
}

type responsesFunctionCall struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Status    string `json:"status"`
}

type responsesFunctionOutput struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	CallID string          `json:"call_id"`
	Output json.RawMessage `json:"output"`
	Status string          `json:"status"`
}

func parseResponsesToolInput(input json.RawMessage, instructions string) ([]store.Message, error) {
	var history toolHistory
	if strings.TrimSpace(instructions) != "" {
		history.add("system", instructions)
	}
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("input is required and must not be null")
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("input must not be empty")
		}
		history.add("user", text)
		return history.finish()
	}
	var items []json.RawMessage
	if err := strictUnmarshal(trimmed, &items); err != nil {
		return nil, errors.New("input must be a string or an array of items")
	}
	var pending []chat.ToolCallRecord
	flush := func() {
		if len(pending) != 0 {
			history.addCalls(pending)
			pending = nil
		}
	}
	for index, data := range items {
		subject := fmt.Sprintf("input[%d]", index)
		var header responsesItemHeader
		if err := json.Unmarshal(data, &header); err != nil {
			return nil, fmt.Errorf("%s must be an object", subject)
		}
		switch header.Type {
		case "function_call":
			var item responsesFunctionCall
			if err := strictUnmarshal(data, &item); err != nil {
				return nil, itemDecodeError(err, subject)
			}
			record, err := replayedCall(item.CallID, item.Name, item.Arguments, subject)
			if err != nil {
				return nil, err
			}
			// Consecutive function_call items are one assistant turn.
			pending = append(pending, record)
			continue
		case "function_call_output":
			flush()
			var item responsesFunctionOutput
			if err := strictUnmarshal(data, &item); err != nil {
				return nil, itemDecodeError(err, subject)
			}
			if item.CallID == "" {
				return nil, fmt.Errorf("%s: call_id is required", subject)
			}
			var output string
			if isAbsentOrNull(item.Output) || json.Unmarshal(item.Output, &output) != nil {
				return nil, fmt.Errorf("%s: output must be a string", subject)
			}
			if err := history.addResult(item.CallID, output, subject); err != nil {
				return nil, err
			}
			continue
		case "", "message":
		default:
			return nil, unsupportedParameter(fmt.Sprintf("%s: unsupported item type %q", subject, header.Type))
		}
		flush()
		var item responsesMessageItem
		if err := strictUnmarshal(data, &item); err != nil {
			return nil, itemDecodeError(err, subject)
		}
		if responsesContentHasInputImage(item.Content) {
			return nil, unsupportedParameter("tools cannot be combined with image input")
		}
		role, err := responsesMessageRole(item.Role, index)
		if err != nil {
			return nil, err
		}
		content, err := item.Content.text()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", subject, err)
		}
		history.add(role, content)
	}
	flush()
	return history.finish()
}

func itemDecodeError(err error, subject string) error {
	if strings.Contains(err.Error(), "unknown field") {
		return unsupportedParameter(subject + " contains an unsupported parameter")
	}
	return fmt.Errorf("%s has an invalid shape", subject)
}

func (s *Server) handleResponsesTools(w http.ResponseWriter, r *http.Request, request responsesRequest) {
	if !validateToolRoute(w, request.Model) {
		return
	}
	normalized, err := normalizeResponsesTools(request.Tools)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	request.Tools = normalized
	tools, choice, err := parseToolMembers(toolMembers{request.Tools, request.ToolChoice, request.ParallelToolCalls}, true)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	schema, err := parseResponsesGuided(request.Text)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	if err := toolCombination(request.Stream, request.ImageGeneration, schema != nil, "text.format"); err != nil {
		writeRequestValidationError(w, err)
		return
	}
	messages, err := parseResponsesToolInput(request.Input, request.Instructions)
	if err != nil {
		writeRequestValidationError(w, err)
		return
	}
	rounds, ok := s.validateToolModel(w, request.Model)
	if !ok {
		return
	}
	outcome, ok := s.runTools(r.Context(), w, rounds, runner.ToolConversation{Tools: tools, Choice: choice, Messages: messages, Options: request.Options})
	if !ok {
		return
	}
	var output []map[string]any
	if len(outcome.Calls) == 0 {
		output = []map[string]any{{
			"type": "message", "id": "msg_" + randomID(), "role": "assistant", "status": "completed",
			"content": []map[string]any{{"type": "output_text", "text": outcome.Text, "annotations": []any{}}},
		}}
	} else {
		for _, call := range outcome.Calls {
			output = append(output, map[string]any{
				"type": "function_call", "id": "fc_" + randomID(), "call_id": call.ID,
				"name": call.Name, "arguments": call.Arguments, "status": "completed",
			})
		}
	}
	result := map[string]any{
		"id": "resp_" + randomID(), "object": "response", "created_at": time.Now().Unix(), "model": string(outcome.Model), "status": "completed",
		"output": output,
	}
	if outcome.Usage != nil {
		result["usage"] = responsesUsage(outcome.Usage)
	}
	writeJSON(w, http.StatusOK, result)
}
