// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package chat

import (
	"strings"
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

func TestValidateTranscriptRejectsOversizedHistory(t *testing.T) {
	history := make([]store.Message, MaxHistoryMessages-1)
	if err := ValidateTranscript(history, "small prompt"); err == nil {
		t.Fatal("ValidateTranscript accepted a turn whose stored result exceeds the message limit")
	}
}

func TestValidateTranscriptRejectsOversizedRenderedPrompt(t *testing.T) {
	rendered := strings.Repeat("x", MaxRenderedPromptBytes+1)
	if err := ValidateTranscript(nil, rendered); err == nil {
		t.Fatal("ValidateTranscript accepted a prompt over the byte limit")
	}
}

func TestValidateTranscriptAcceptsLimits(t *testing.T) {
	history := make([]store.Message, MaxHistoryMessages-2)
	rendered := strings.Repeat("x", MaxRenderedPromptBytes)
	if err := ValidateTranscript(history, rendered); err != nil {
		t.Fatalf("ValidateTranscript rejected the exact limits: %v", err)
	}
}

func TestValidatePromptRejectsOversizedOneShot(t *testing.T) {
	if err := ValidatePrompt(strings.Repeat("x", MaxRenderedPromptBytes+1)); err == nil {
		t.Fatal("ValidatePrompt accepted a one-shot prompt over the byte limit")
	}
}
