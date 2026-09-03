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
func RenderTranscript(history []store.Message, newUserContent string) string {
	var b strings.Builder
	b.WriteString(replayPreamble)
	b.WriteString("\n")
	for _, m := range history {
		b.WriteString("\n")
		b.WriteString(roleLabel(m.Role))
		b.WriteString(":\n")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	b.WriteString("\nUSER:\n")
	b.WriteString(newUserContent)
	b.WriteString("\n\n")
	b.WriteString(replayClosing)
	b.WriteString("\n")
	return b.String()
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
