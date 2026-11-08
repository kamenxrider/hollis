// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package chat

import (
	"strings"

	"github.com/kamenxrider/hollis/internal/store"
)

// ShortcutInstructions puts instructions last in Shortcuts' single text field.
// Local callers must instead use the native instructions channel.
func ShortcutInstructions(prompt, instructions string) string {
	if instructions == "" {
		return prompt
	}
	return "Prompt:\n" + prompt + "\n\nInstructions:\n" + instructions
}

// ShortcutTranscript preserves conversation order and moves system/developer
// messages, in their original order, after the replay closing directive.
// Explicit CLI instructions precede conversation instructions in that block.
func ShortcutTranscript(history []store.Message, prompt, instructions string) string {
	filtered := make([]store.Message, 0, len(history))
	parts := []string{}
	if instructions != "" {
		parts = append(parts, instructions)
	}
	for _, message := range history {
		if message.Role == "system" || message.Role == "developer" {
			parts = append(parts, message.Content)
		} else {
			filtered = append(filtered, message)
		}
	}
	return ShortcutInstructions(RenderTranscript(filtered, prompt), strings.Join(parts, "\n\n"))
}
