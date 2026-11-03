// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package chat renders stored conversations into the replay transcript that
// Apple Intelligence sees each turn. Apple's runs are stateless; continuity
// is created by replaying this deterministic transcript (proven by results
// Test B/C/E). The format is plan §13; alternatives must be tested against
// both models before replacing it.
package chat

import (
	"fmt"
	"strings"

	"github.com/kamenxrider/hollis/internal/store"
)

// replayPreamble and replayClosing bracket every replayed transcript.
const (
	replayPreamble = `You are continuing an existing conversation.`
	replayClosing  = "Respond to the final USER message while preserving the conversation context."

	// MaxHistoryMessages prevents an accidentally unbounded replay from
	// consuming the transport's prompt budget. Stored history is never
	// trimmed; callers fail before invoking a model when this is exceeded.
	MaxHistoryMessages = 256
	// MaxRenderedPromptBytes is a byte, rather than rune, limit because the
	// transport receives UTF-8 bytes and Apple rejects oversized prompts.
	MaxRenderedPromptBytes = 128 << 10
)

// ValidatePrompt checks the byte limit for any prompt before a caller invokes
// Apple Intelligence.
func ValidatePrompt(prompt string) error {
	if len(prompt) > MaxRenderedPromptBytes {
		return fmt.Errorf("rendered prompt is %d bytes; maximum is %d", len(prompt), MaxRenderedPromptBytes)
	}
	return nil
}

// ValidateTranscript checks the immutable history and rendered prompt limits
// before a caller invokes Apple Intelligence. A completed turn adds one user
// and one assistant message, so the projected stored total must also fit. It
// intentionally does not trim, summarize, or otherwise alter user content.
func ValidateTranscript(history []store.Message, rendered string) error {
	if len(history)+2 > MaxHistoryMessages {
		return fmt.Errorf("completed turn would store %d messages; maximum is %d", len(history)+2, MaxHistoryMessages)
	}
	return ValidatePrompt(rendered)
}

// RenderTranscript builds the deterministic replay prompt from stored
// messages plus the new user message (not yet stored). Format per plan §13:
//
//	You are continuing an existing conversation.
//
//	SYSTEM:
//	...
//
//	USER:
//	...
//
//	ASSISTANT:
//	...
//
//	USER:
//	<new message>
//
//	Respond to the final USER message while preserving the conversation context.
//
// Tool turns stored as a hollis_tool envelope render as the tool block the
// loop renders; every other body renders verbatim.
func RenderTranscript(history []store.Message, newUserContent string) string {
	var b strings.Builder
	b.WriteString(replayPreamble)
	b.WriteString("\n")
	writeTurns(&b, history)
	b.WriteString("\nUSER:\n")
	b.WriteString(newUserContent)
	b.WriteString("\n\n")
	b.WriteString(replayClosing)
	b.WriteString("\n")
	return b.String()
}

// toolRoundClosing replaces replayClosing when the conversation ends in tool
// results rather than a user message.
const toolRoundClosing = "Answer the most recent USER message using the TOOL results above, or call another tool if one is still needed."

// RenderToolTranscript builds the prompt for one tool round: the fixed tool
// instructions block (omitted when empty), the whole conversation (whose final turn is a user
// message or a tool result), an optional fixed notice, then the closing line.
// It is a pure function of its inputs, so every round re-renders the full
// transcript rather than appending to a previous prompt.
func RenderToolTranscript(instructions string, messages []store.Message, notice string) string {
	var b strings.Builder
	if instructions != "" {
		b.WriteString(instructions)
		b.WriteString("\n\n")
	}
	b.WriteString(replayPreamble)
	b.WriteString("\n")
	writeTurns(&b, messages)
	if notice != "" {
		b.WriteString("\n")
		b.WriteString(notice)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if len(messages) > 0 && messages[len(messages)-1].Role == RoleTool {
		b.WriteString(toolRoundClosing)
	} else {
		b.WriteString(replayClosing)
	}
	b.WriteString("\n")
	return b.String()
}

func writeTurns(b *strings.Builder, messages []store.Message) {
	names := map[string]string{}
	for _, m := range messages {
		content := m.Content
		if rendered, ok := renderToolTurn(m, names); ok {
			content = rendered
		}
		b.WriteString("\n")
		b.WriteString(roleLabel(m.Role))
		b.WriteString(":\n")
		b.WriteString(content)
		b.WriteString("\n")
	}
}

// roleLabel maps stored roles to transcript labels. Unknown roles render as
// uppercase role tags to stay deterministic.
func roleLabel(role string) string {
	switch role {
	case "system":
		return "SYSTEM"
	case "user":
		return "USER"
	case "assistant":
		return "ASSISTANT"
	default:
		return strings.ToUpper(role)
	}
}
