// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package chat renders stored conversations into the replay transcript that
// Apple Intelligence sees each turn. Apple's runs are stateless; continuity
// is created by replaying this deterministic transcript (proven by results
// Test B/C/E). The format is plan §13; alternatives must be tested against
// both models before replacing it.
package chat

import (
	"strings"

	"github.com/kamenxrider/hollis/internal/store"
)

// replayPreamble and replayClosing bracket every replayed transcript.
const (
	replayPreamble = `You are continuing an existing conversation.`
	replayClosing  = "Respond to the final USER message while preserving the conversation context."
)

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

// ApproxSize returns the transcript size in characters — an approximate
// local metric only. Never call this "tokens" (plan §14).
func SizeChars(s string) int { return len([]rune(s)) }
