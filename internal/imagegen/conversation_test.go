// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"strings"
	"testing"
)

func TestRenderConversationPromptSingleUserIsVerbatim(t *testing.T) {
	want := "  Draw a turtle in a pond.\nKeep the light warm.  "
	got := RenderConversationPrompt([]ConversationMessage{{Role: "user", Content: want}})
	if got != want {
		t.Fatalf("prompt = %q, want verbatim %q", got, want)
	}
}

func TestRenderConversationPromptUsesProvenRevisionShapeAndDropsCLIArtifact(t *testing.T) {
	messages := []ConversationMessage{
		{Role: "user", Content: "A small turtle beside a glass greenhouse."},
		{Role: "assistant", Content: `HOLLIS_IMAGE_ARTIFACT {"type":"hollis.image_artifact.v1","path":"/private/secret/turtle.png","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`},
		{Role: "user", Content: "Move the turtle inside the greenhouse."},
	}
	want := "Original image description:\nA small turtle beside a glass greenhouse.\n\nRequested revision:\nMove the turtle inside the greenhouse."
	got := RenderConversationPrompt(messages)
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "aaaa") {
		t.Fatalf("artifact metadata leaked into prompt: %q", got)
	}
}

func TestRenderConversationPromptPreservesOrderedRevisions(t *testing.T) {
	artifact := `HOLLIS_IMAGE_ARTIFACT {"type":"hollis.image_artifact.v1"}`
	got := RenderConversationPrompt([]ConversationMessage{
		{Role: "user", Content: "Draw a red boat."},
		{Role: "assistant", Content: artifact},
		{Role: "user", Content: "Add a white sail."},
		{Role: "assistant", Content: artifact},
		{Role: "user", Content: "Make the sail blue."},
	})
	want := "Original image description:\nDraw a red boat.\n\nRequested revision 1:\nAdd a white sail.\n\nRequested revision 2:\nMake the sail blue."
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
}

func TestRenderConversationPromptPreservesMixedConversationAndUnrecognizedMetadata(t *testing.T) {
	messages := []ConversationMessage{
		{Role: "system", Content: "Use flat colors."},
		{Role: "user", Content: "Draw a tree."},
		{Role: "assistant", Content: "The tree has a round crown."},
		{Role: "assistant", Content: `HOLLIS_IMAGE_ARTIFACT {"type":"some.other.record","path":"keep-me"}`},
		{Role: "user", Content: "Make it autumn."},
	}
	got := RenderConversationPrompt(messages)
	for _, want := range []string{"SYSTEM:\nUse flat colors.", "USER:\nDraw a tree.", "ASSISTANT:\nThe tree has a round crown.", "keep-me", "USER:\nMake it autumn."} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
}

func TestRenderConversationPromptDropsOnlyExactAPIMarker(t *testing.T) {
	marker := `[Hollis generated an image for "Draw a green tree" with style animation (native 1024x1024, final 1024x1024, no output transform, SHA-256 aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa). The image pixels are included in this response but are not retained or reused; replay this text to preserve the generation record. A later request uses text context to generate a new image, not pixel editing.]`
	got := RenderConversationPrompt([]ConversationMessage{
		{Role: "user", Content: "Draw a green tree"},
		{Role: "assistant", Content: marker},
		{Role: "user", Content: "Make it autumn"},
	})
	want := "Original image description:\nDraw a green tree\n\nRequested revision:\nMake it autumn"
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}

	nearMatch := marker + " This is real assistant context."
	got = RenderConversationPrompt([]ConversationMessage{{Role: "assistant", Content: nearMatch}, {Role: "user", Content: "Continue"}})
	if !strings.Contains(got, nearMatch) {
		t.Fatalf("non-exact marker was dropped: %q", got)
	}
}

func TestRenderConversationPromptRecoversRequestFromAssistantOnlyAPIReplay(t *testing.T) {
	marker := `[Hollis generated an image for "Draw a greenhouse with a brass turtle" with style illustration (native 1024x1024, final 1024x1024, no output transform, SHA-256 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb). The image pixels are included in this response but are not retained or reused; replay this text to preserve the generation record. A later request uses text context to generate a new image, not pixel editing.]`
	got := RenderConversationPrompt([]ConversationMessage{
		{Role: "assistant", Content: marker},
		{Role: "user", Content: "Change the light to golden evening sunlight"},
	})
	want := "Original image description:\nDraw a greenhouse with a brass turtle\n\nRequested revision:\nChange the light to golden evening sunlight"
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
}

func TestRenderConversationPromptDoesNotDuplicateRequestRecoveredFromAPIMarker(t *testing.T) {
	marker := `[Hollis generated an image for "Draw a green tree" with style animation (native 1024x1024, final 1024x1024, no output transform, SHA-256 aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa). The image pixels are included in this response but are not retained or reused; replay this text to preserve the generation record. A later request uses text context to generate a new image, not pixel editing.]`
	got := RenderConversationPrompt([]ConversationMessage{
		{Role: "user", Content: "Draw a green tree"},
		{Role: "assistant", Content: marker},
		{Role: "user", Content: "Make it autumn"},
	})
	if strings.Count(got, "Draw a green tree") != 1 {
		t.Fatalf("original request duplicated: %q", got)
	}
}
