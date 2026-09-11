// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

func modelCapabilities(id string) map[string]any {
	local := id == string(runner.ModelLocal)
	backend := "shortcuts"
	if local {
		backend = "native"
	}
	return map[string]any{"backend": backend, "streaming": local, "usage": local, "streaming_usage": false, "image_input": id == "cloud" || id == "cloud-pro" || id == "chatgpt"}
}

func chatUsage(usage *runner.Usage) any {
	if usage == nil {
		return nil
	}
	return map[string]any{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.InputTokens + usage.OutputTokens, "completion_tokens_details": map[string]int{"reasoning_tokens": usage.ReasoningTokens}}
}

func responsesUsage(usage *runner.Usage) any {
	if usage == nil {
		return nil
	}
	return map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.InputTokens + usage.OutputTokens, "output_tokens_details": map[string]int{"reasoning_tokens": usage.ReasoningTokens}}
}

func (s *Server) completePrepared(ctx context.Context, w http.ResponseWriter, model runner.Model, request preparedRequest) (runner.Completion, bool) {
	if model != runner.ModelLocal {
		text, used, ok := s.runPrepared(ctx, w, model, request)
		return runner.Completion{Text: text, Model: used}, ok
	}
	complete, ok := s.Runner.(runner.CompleteRunner)
	if !ok {
		writeNativeError(w, errors.New("native runner unavailable"))
		return runner.Completion{}, false
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return runner.Completion{}, false
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, runner.MaxTimeout)
	defer cancel()
	result, err := complete.RunComplete(ctx, model, request.Prompt)
	if err == nil && (result.Model != model || strings.TrimSpace(result.Text) == "") {
		err = errors.New("invalid native completion")
	}
	if err != nil {
		writeNativeError(w, err)
		return runner.Completion{}, false
	}
	return result, true
}

// Keep backend failures classified without exposing helper stderr or prompts.
func nativeError(err error) (int, map[string]any) {
	status, kind, code, message := http.StatusBadGateway, "server_error", "native_failed", "the native local model request failed"
	var classified *runner.Error
	if errors.As(err, &classified) {
		switch classified.Kind {
		case runner.KindContextCapacity:
			status, kind, code, message = http.StatusRequestEntityTooLarge, "invalid_request_error", "context_too_large", "the full conversation exceeds the local model's capacity; no messages were shortened or dropped"
		case runner.KindLocalUnavailable:
			status, code, message = http.StatusServiceUnavailable, "model_unavailable", "the native local model or its matching helper is unavailable"
		case runner.KindRequestDeclined:
			code, message = "request_declined", "the native local model declined the request"
		case runner.KindRateLimited:
			status, kind, code, message = http.StatusTooManyRequests, "rate_limit_error", "rate_limited", "the native local model is rate limited"
		case runner.KindTimeout, runner.KindContextCanceled:
			status, code, message = http.StatusGatewayTimeout, "native_timeout", "the native local model request was canceled or exceeded its deadline"
		case runner.KindNativeProtocol:
			code, message = "native_protocol", "the native local helper returned an invalid or incomplete response"
		case runner.KindTransport:
			code, message = "transport", "the native local transport could not complete the request"
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status, code, message = http.StatusGatewayTimeout, "native_timeout", "the native local model request was canceled or exceeded its deadline"
	}
	return status, map[string]any{"type": kind, "code": code, "message": message}
}

func writeNativeError(w http.ResponseWriter, err error) {
	status, detail := nativeError(err)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, status, map[string]any{"error": detail})
}

// streamEvents owns one request's wire lifecycle. Responses uses named events
// with sequence numbers; Chat Completions uses data-only chunks and [DONE].
// No Apple text is used as an SSE header or copied into a frame without JSON encoding.
type streamEvents struct {
	w          http.ResponseWriter
	responses  bool
	started    bool
	sequence   int
	id, itemID string
	created    int64
}

func (e *streamEvents) send(name string, value map[string]any) error {
	if e.responses {
		value["type"] = name
		value["sequence_number"] = e.sequence
		e.sequence++
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if !e.started {
		e.w.Header().Set("Content-Type", "text/event-stream")
		e.w.Header().Set("Cache-Control", "no-cache")
		e.w.Header().Set("X-Accel-Buffering", "no")
		e.w.WriteHeader(http.StatusOK)
		e.started = true
	}
	if e.responses {
		if _, err = fmt.Fprintf(e.w, "event: %s\n", name); err != nil {
			return err
		}
	}
	if _, err = fmt.Fprintf(e.w, "data: %s\n\n", encoded); err != nil {
		return err
	}
	return http.NewResponseController(e.w).Flush()
}

func (e *streamEvents) response(status, text string) map[string]any {
	output := []any{}
	if status == "completed" {
		output = append(output, e.item("completed", text))
	}
	return map[string]any{"id": e.id, "object": "response", "created_at": e.created, "model": string(runner.ModelLocal), "status": status, "output": output, "error": nil, "incomplete_details": nil, "usage": nil}
}
func (e *streamEvents) part(text string) map[string]any {
	return map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
}
func (e *streamEvents) item(status, text string) map[string]any {
	content := []any{}
	if status == "completed" {
		content = append(content, e.part(text))
	}
	return map[string]any{"id": e.itemID, "type": "message", "role": "assistant", "status": status, "content": content}
}
func (e *streamEvents) chunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{"id": e.id, "object": "chat.completion.chunk", "created": e.created, "model": string(runner.ModelLocal), "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}
func (e *streamEvents) begin() error {
	if !e.responses {
		return e.send("", e.chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	}
	for _, name := range []string{"response.created", "response.in_progress"} {
		if err := e.send(name, map[string]any{"response": e.response("in_progress", "")}); err != nil {
			return err
		}
	}
	if err := e.send("response.output_item.added", map[string]any{"output_index": 0, "item": e.item("in_progress", "")}); err != nil {
		return err
	}
	return e.send("response.content_part.added", map[string]any{"item_id": e.itemID, "output_index": 0, "content_index": 0, "part": e.part("")})
}
func (e *streamEvents) delta(text string) error {
	if !e.started {
		if err := e.begin(); err != nil {
			return err
		}
	}
	if !e.responses {
		return e.send("", e.chunk(map[string]any{"content": text}, nil))
	}
	return e.send("response.output_text.delta", map[string]any{"item_id": e.itemID, "output_index": 0, "content_index": 0, "delta": text, "logprobs": []any{}})
}
func (e *streamEvents) done(text string) error {
	if !e.responses {
		if err := e.send("", e.chunk(map[string]any{}, "stop")); err != nil {
			return err
		}
		if _, err := fmt.Fprint(e.w, "data: [DONE]\n\n"); err != nil {
			return err
		}
		return http.NewResponseController(e.w).Flush()
	}
	if err := e.send("response.output_text.done", map[string]any{"item_id": e.itemID, "output_index": 0, "content_index": 0, "text": text, "logprobs": []any{}}); err != nil {
		return err
	}
	if err := e.send("response.content_part.done", map[string]any{"item_id": e.itemID, "output_index": 0, "content_index": 0, "part": e.part(text)}); err != nil {
		return err
	}
	if err := e.send("response.output_item.done", map[string]any{"output_index": 0, "item": e.item("completed", text)}); err != nil {
		return err
	}
	return e.send("response.completed", map[string]any{"response": e.response("completed", text)})
}
func (e *streamEvents) failed(err error) {
	if !e.started {
		writeNativeError(e.w, err)
		return
	}
	_, detail := nativeError(err)
	if !e.responses {
		_ = e.send("", map[string]any{"error": detail})
		return
	}
	response := e.response("failed", "")
	response["error"] = map[string]any{"code": detail["code"], "message": detail["message"]}
	_ = e.send("response.failed", map[string]any{"response": response})
}

func (s *Server) streamPrepared(w http.ResponseWriter, r *http.Request, model runner.Model, request preparedRequest, responses bool) {
	stream, ok := s.Runner.(runner.StreamRunner)
	if model != runner.ModelLocal || request.hasImages() || !ok {
		unsupportedStreaming(w)
		return
	}
	release, ok := s.acquireCapacity(w)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), runner.MaxTimeout)
	defer cancel()
	// A stalled HTTP reader must not keep a callback and model process alive past
	// the request's deadline. ResponseController reaches standard wrapped writers.
	controller := http.NewResponseController(w)
	if deadline, ok := ctx.Deadline(); ok {
		_ = controller.SetWriteDeadline(deadline)
	}
	defer controller.SetWriteDeadline(time.Time{})
	cancelWrite := context.AfterFunc(ctx, func() { _ = controller.SetWriteDeadline(time.Now()) })
	defer cancelWrite()
	prefix := "chatcmpl-"
	if responses {
		prefix = "resp_"
	}
	events := streamEvents{w: w, responses: responses, id: prefix + randomID(), itemID: "msg_" + randomID(), created: time.Now().Unix()}
	var accumulated strings.Builder
	result, err := stream.Stream(ctx, model, request.Prompt, func(delta string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if delta == "" {
			return nil
		}
		if err := events.delta(delta); err != nil {
			cancel()
			return err
		}
		accumulated.WriteString(delta)
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && (result.Model != model || strings.TrimSpace(result.Text) == "" || result.Text != accumulated.String()) {
		err = errors.New("native stream did not yield a matching complete result")
	}
	if err != nil {
		events.failed(err)
		return
	}
	// Streaming token usage is deliberately absent: only complete-call usage has
	// been qualified. Never fabricate counts or turn partial output into success.
	_ = events.done(result.Text)
}
