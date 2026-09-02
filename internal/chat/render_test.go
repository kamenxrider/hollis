// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package chat

import (
	"testing"

	"github.com/kamenxrider/hollis/internal/store"
)

func TestRenderTranscriptDeterministic(t *testing.T) {
	history := []store.Message{
		{Seq: 0, Role: "system", Content: "Be terse."},
		{Seq: 1, Role: "user", Content: "Remember codeword VANTA-ORBIT-7319"},
		{Seq: 2, Role: "assistant", Content: "ACK"},
	}
	got := RenderTranscript(history, "What was the codeword?")
	want := "You are continuing an existing conversation.\n" +
		"\n" +
		"SYSTEM:\n" +
		"Be terse.\n" +
		"\n" +
		"USER:\n" +
		"Remember codeword VANTA-ORBIT-7319\n" +
		"\n" +
		"ASSISTANT:\n" +
		"ACK\n" +
		"\n" +
		"USER:\n" +
		"What was the codeword?\n" +
		"\n" +
		"Respond to the final USER message while preserving the conversation context.\n"
	if got != want {
		t.Fatalf("transcript mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderTranscriptEmptyHistory(t *testing.T) {
	got := RenderTranscript(nil, "hello")
	want := "You are continuing an existing conversation.\n\nUSER:\nhello\n\nRespond to the final USER message while preserving the conversation context.\n"
	if got != want {
		t.Fatalf("render mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestUnknownRoleUppercase(t *testing.T) {
	if got := roleLabel("tool"); got != "TOOL" {
		t.Fatalf("roleLabel = %q, want TOOL", got)
	}
}
