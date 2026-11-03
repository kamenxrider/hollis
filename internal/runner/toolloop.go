// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/store"
)

// ToolRoundRunner runs one complete, non-streaming tool round with the given
// helper tool-calling mode ("allowed" or "disallowed"). Tool rounds never
// stream: a call must be validated before any of it reaches a caller.
type ToolRoundRunner interface {
	RunToolRound(ctx context.Context, model Model, request ToolRoundRequest) (Completion, error)
}

// ToolRoundRequest keeps native metadata separate from the conversation.
type ToolRoundRequest struct {
	Prompt         string
	Mode           string
	Instructions   string
	Definitions    []ToolDefinition
	RawDefinitions []RawToolDefinition
	Hybrid         bool
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Source      string          `json:"source,omitempty"`
}

func (r ToolRoundRequest) inputBytes() int {
	total := len(r.Prompt) + len(r.Instructions)
	for _, tool := range r.Definitions {
		total += len(tool.Name) + len(tool.Description) + len(tool.Parameters)
	}
	for _, tool := range r.RawDefinitions {
		total += len(rawToolInstruction(tool))
	}
	return total
}

func nativeToolDefinitions(tools []Tool) []ToolDefinition {
	definitions := make([]ToolDefinition, len(tools))
	for i, tool := range tools {
		definitions[i] = ToolDefinition{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters.Bytes()}
	}
	return definitions
}

// ToolResult is one tool's output, matched to its call by id.
type ToolResult struct {
	CallID string
	Output string
}

// ToolExecution accounts for each original call exactly once on success.
// Pending calls must be unchanged originals. On error, Results may contain
// completed calls only; missing calls are not retried or continued.
type ToolExecution struct {
	Results []ToolResult
	Pending []ToolCall
}

// ToolExecutor dispatches externally; runner never imports an MCP transport.
// After appending each completed result, executors must call admit and stop on
// error before any later approval or dispatch. Keep that result in the partial
// execution returned on error; admission does not undo a completed operation.
type ToolExecutor interface {
	ExecuteTools(ctx context.Context, calls []ToolCall, admit func(ToolResult) error) (ToolExecution, error)
}

// ReturnToCaller hands every validated call back to the caller.
type ReturnToCaller struct{}

func (ReturnToCaller) ExecuteTools(_ context.Context, calls []ToolCall, _ func(ToolResult) error) (ToolExecution, error) {
	return ToolExecution{Pending: append([]ToolCall(nil), calls...)}, nil
}

// ToolConversation is one tool-enabled request. Messages is the whole
// conversation; its final message is a user message, an assistant tool-call
// turn being continued, or a tool result. Tool turns use the chat package's
// hollis_tool envelope.
type ToolConversation struct {
	Tools    []Tool
	Offers   []ToolOffer
	Choice   ToolChoice
	Messages []store.Message
}

// ToolOutcome is the loop's answer: either final text or validated calls.
type ToolOutcome struct {
	Text  string
	Calls []ToolCall
	Model Model
	// Messages contains the input transcript plus appended call/result messages,
	// excluding correction notices and the final answer (Text). Available
	// completed messages are preserved on error, not as a durable execution log.
	Messages []store.Message
	// Usage sums every round's measured counts, and is nil unless every
	// round reported them.
	Usage  *Usage
	Rounds int
}

// toolRepairNotice is the fixed repair template. reason is a fixed sentence
// whose only variable parts are numbers, an offered tool name and a pointer
// built from schema-authored property names; it never carries an argument
// value.
func toolRepairNotice(round int, reason string) string {
	return fmt.Sprintf("Your previous reply in round %d was not a valid tool call: %s. Reply again with either exactly one JSON object of the form {\"tool_calls\":[...]} that follows the format and parameters above, or a plain-text answer if no tool is needed.", round, reason)
}

// RunToolLoop drives the out-of-band tool loop. Every round re-renders the
// full transcript, is checked against the prompt budget before it spawns,
// and runs as one complete helper request. One deadline, ctx's, covers every
// round. The loop never retries after a refusal or rate limit; a round whose
// call fails validation gets exactly one repair round.
func RunToolLoop(ctx context.Context, r ToolRoundRunner, model Model, conversation ToolConversation, executor ToolExecutor) (ToolOutcome, error) {
	outcome := ToolOutcome{Model: model}
	if executor == nil {
		executor = ReturnToCaller{}
	}
	if len(conversation.Messages) == 0 {
		return outcome, nativeError(KindUsage, "tool conversation must not be empty")
	}
	messages := append([]store.Message(nil), conversation.Messages...)
	outcome.Messages = append([]store.Message(nil), conversation.Messages...)
	appendMessage := func(message store.Message) {
		messages = append(messages, message)
		outcome.Messages = append(outcome.Messages, message)
	}
	used := map[string]bool{}
	for _, message := range messages {
		if calls, ok := chat.DecodeToolCalls(message.Content); ok && message.Role == "assistant" {
			for _, call := range calls {
				used[call.ID] = true
			}
		}
	}
	usage, measured := Usage{}, true
	prior := priorToolRounds(messages)
	consumed := prior
	capFailure := func() *Error {
		return loopFault(KindToolRoundCap, fmt.Sprintf("tool loop reached its cap of %d model calls; %d visible prior tool turns and %d new calls used the budget", ToolRoundCap, prior, outcome.Rounds))
	}
	run := func(round int, request ToolRoundRequest) (Completion, error) {
		if consumed >= ToolRoundCap {
			return Completion{}, capFailure()
		}
		if err := nativeContextError(ctx); err != nil {
			return Completion{}, err
		}
		if size := request.inputBytes(); size > NativeMaxPromptBytes {
			err := nativeError(KindContextCapacity, fmt.Sprintf("tool round %d renders to %d bytes; the limit is %d bytes; nothing was truncated", round, size, NativeMaxPromptBytes))
			err.PromptBytes = size
			return Completion{}, err
		}
		consumed++
		outcome.Rounds++
		completion, err := r.RunToolRound(ctx, model, request)
		if err != nil {
			return completion, err
		}
		if err := nativeContextError(ctx); err != nil {
			return completion, err
		}
		if completion.Usage == nil {
			measured = false
		} else {
			usage.InputTokens += completion.Usage.InputTokens
			usage.OutputTokens += completion.Usage.OutputTokens
			usage.ReasoningTokens += completion.Usage.ReasoningTokens
		}
		return completion, nil
	}
	finish := func() {
		if measured && outcome.Rounds > 0 {
			total := usage
			outcome.Usage = &total
		}
	}

	if conversation.Choice == ToolChoiceNone {
		// No tools are in scope, so nothing is rendered and nothing is
		// detected: a call written anyway is delivered as plain text.
		completion, err := run(1, ToolRoundRequest{Prompt: chat.RenderToolTranscript("", messages, ""), Mode: "disallowed"})
		if err != nil {
			return outcome, err
		}
		outcome.Text = completion.Text
		finish()
		return outcome, nil
	}
	if conversation.Choice != ToolChoiceAuto {
		return outcome, nativeError(KindUsage, "tool_choice accepts only auto or none")
	}
	tools, request, err := prepareToolOffers(conversation)
	if err != nil {
		return outcome, err
	}
	if len(tools) == 0 {
		return outcome, nativeError(KindUsage, "tool loop requires at least one tool")
	}
	round := prior + 1
	for {
		request.Prompt = chat.RenderToolTranscript("", messages, "")
		completion, err := run(round, request)
		if err != nil {
			return outcome, err
		}
		result := interpretOfferedRound(completion.Text, tools, round, used)
		if !result.plain && result.fault != "" && !result.framing {
			notice := toolRepairNotice(round, result.fault)
			request.Prompt = chat.RenderToolTranscript("", messages, notice)
			completion, err = run(round, request)
			if err != nil {
				return outcome, err
			}
			result = interpretOfferedRound(completion.Text, tools, round, used)
			if !result.plain && result.fault != "" {
				return outcome, loopFault(KindToolCallInvalid, fmt.Sprintf("round %d produced an invalid tool call after its repair round: %s", round, result.fault))
			}
		}
		if result.framing {
			return outcome, loopFault(KindToolCallInvalid, fmt.Sprintf("round %d: %s", round, result.fault))
		}
		if result.plain {
			outcome.Text = completion.Text
			finish()
			return outcome, nil
		}
		if consumed >= ToolRoundCap {
			return outcome, capFailure()
		}
		assistant := store.Message{Role: "assistant", Content: chat.EncodeToolCalls(toolCallRecords(result.calls))}
		admitted := append(append([]store.Message(nil), messages...), assistant)
		admit := func(result ToolResult) error {
			admitted = append(admitted, store.Message{Role: chat.RoleTool, Content: chat.EncodeToolResult(result.CallID, result.Output)})
			next := request
			next.Prompt = chat.RenderToolTranscript("", admitted, "")
			if err := toolResultBudget(next); err != nil {
				return err
			}
			return nativeContextError(ctx)
		}
		// An executor must not be able to mutate the originals used to verify
		// its pending dispositions or the assistant envelope.
		execution, execErr := executor.ExecuteTools(ctx, append([]ToolCall(nil), result.calls...), admit)
		results, pending, matchErr := matchExecution(result.calls, execution, execErr != nil)
		if matchErr != nil {
			return outcome, matchErr
		}
		appendMessage(assistant)
		for _, call := range result.calls {
			used[call.ID] = true
		}
		for _, toolResult := range results {
			appendMessage(store.Message{Role: chat.RoleTool, Content: chat.EncodeToolResult(toolResult.CallID, toolResult.Output)})
		}
		if execErr != nil {
			finish()
			return outcome, execErr
		}
		if err := nativeContextError(ctx); err != nil {
			finish()
			return outcome, err
		}
		if len(results) > 0 {
			request.Prompt = chat.RenderToolTranscript("", messages, "")
			if budgetErr := toolResultBudget(request); budgetErr != nil {
				finish()
				return outcome, budgetErr
			}
		}
		if len(pending) > 0 {
			outcome.Calls = pending
			finish()
			return outcome, nil
		}
		round++
	}
}

func toolResultBudget(request ToolRoundRequest) error {
	if size := request.inputBytes(); size > NativeMaxPromptBytes {
		err := nativeError(KindContextCapacity, fmt.Sprintf("tool results render to %d bytes; the limit is %d bytes; nothing was truncated", size, NativeMaxPromptBytes))
		err.PromptBytes = size
		return err
	}
	return nil
}

// priorToolRounds counts the assistant tool-call turns since the last user
// message. A caller that continues a conversation over several requests
// carries those visible turns in its history. Earlier hidden corrections cannot
// be reconstructed from stateless replay; this is not a global request quota.
func priorToolRounds(messages []store.Message) int {
	count := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role == "user" {
			break
		}
		if _, ok := chat.DecodeToolCalls(message.Content); ok && message.Role == "assistant" {
			count++
		}
	}
	return count
}

func toolCallRecords(calls []ToolCall) []chat.ToolCallRecord {
	records := make([]chat.ToolCallRecord, len(calls))
	for index, call := range calls {
		records[index] = chat.ToolCallRecord{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
	}
	return records
}

// ToolCallRecords converts validated calls to their stored form.
func ToolCallRecords(calls []ToolCall) []chat.ToolCallRecord { return toolCallRecords(calls) }

// matchExecution verifies before appending anything, then restores call order
// independently of executor result/pending ordering.
func matchExecution(calls []ToolCall, execution ToolExecution, partial bool) ([]ToolResult, []ToolCall, error) {
	want := make(map[string]ToolCall, len(calls))
	for _, call := range calls {
		want[call.ID] = call
	}
	results := map[string]ToolResult{}
	pending := map[string]ToolCall{}
	bad := func() ([]ToolResult, []ToolCall, error) {
		return nil, nil, nativeError(KindUsage, "tool executor returned invalid or incomplete call dispositions")
	}
	for _, result := range execution.Results {
		if _, ok := want[result.CallID]; !ok {
			return bad()
		}
		if _, ok := results[result.CallID]; ok {
			return bad()
		}
		results[result.CallID] = result
	}
	for _, call := range execution.Pending {
		original, ok := want[call.ID]
		if !ok || original != call {
			return bad()
		}
		if _, ok := results[call.ID]; ok {
			return bad()
		}
		if _, ok := pending[call.ID]; ok {
			return bad()
		}
		pending[call.ID] = call
	}
	if !partial && len(results)+len(pending) != len(calls) {
		return bad()
	}
	var orderedResults []ToolResult
	var orderedPending []ToolCall
	for _, call := range calls {
		if result, ok := results[call.ID]; ok {
			orderedResults = append(orderedResults, result)
		}
		if _, ok := pending[call.ID]; ok {
			orderedPending = append(orderedPending, call)
		}
	}
	return orderedResults, orderedPending, nil
}

func loopFault(kind Kind, message string) *Error {
	return &Error{Kind: kind, ExitCode: -1, Err: errors.New(message)}
}

// RunToolRound runs one tool round as a complete native request carrying
// the helper tool-calling mode.
func (r *NativeRunner) RunToolRound(ctx context.Context, model Model, request ToolRoundRequest) (Completion, error) {
	if request.Mode == "" {
		return Completion{Model: model}, nativeError(KindUsage, "tool round requires a tool-calling mode")
	}
	return r.RunCompleteWithOptions(ctx, model, request.Prompt, nativeOptions{ToolCalling: request.Mode, ToolInstructions: request.Instructions, ToolDefinitions: request.Definitions, RawToolDefinitions: request.RawDefinitions, HybridTools: request.Hybrid})
}

// RunToolRound is native-only: Shortcut routes have no tool surface.
func (r *RoutedRunner) RunToolRound(ctx context.Context, model Model, request ToolRoundRequest) (Completion, error) {
	if model != ModelLocal {
		return Completion{Model: model}, nativeError(KindUsage, "tools are supported only for model local")
	}
	return r.Native.RunToolRound(ctx, model, request)
}
