// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

const shortcutReplay = "You are continuing an existing conversation.\n\nUSER:\nSay hello.\n\nRespond to the final USER message while preserving the conversation context.\n"

func TestShortcutRenderedPrompts(t *testing.T) {
	stubConfigPath(t)
	file := filepath.Join(t.TempDir(), "instructions.txt")
	if err := os.WriteFile(file, []byte("Reply READY."), 0600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, input, want string
		args              []string
	}{
		{"system", "", "Prompt:\nSay hello.\n\nInstructions:\nReply READY.", []string{"-s", "Reply READY.", "Say hello."}},
		{"system-file", "", "Prompt:\nSay hello.\n\nInstructions:\nReply READY.", []string{"--system-file", file, "Say hello."}},
		{"messages-system", `[{"role":"system","content":"Reply READY."},{"role":"user","content":"Say hello."}]`, "Prompt:\n" + shortcutReplay + "\n\nInstructions:\nReply READY.", []string{"--messages", "-"}},
		{"messages-developer", `[{"role":"system","content":"Reply READY."},{"role":"developer","content":"No punctuation."},{"role":"user","content":"Say hello."}]`, "Prompt:\n" + shortcutReplay + "\n\nInstructions:\nExplicit first.\n\nReply READY.\n\nNo punctuation.", []string{"--messages", "-", "-s", "Explicit first."}},
	}
	for _, model := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		for _, tc := range tests {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				r := &recordingRunner{response: "READY"}
				args := append([]string{"respond", "-m", model}, tc.args...)
				_, _, err := runContextCommand(t, r, tc.input, args...)
				if err != nil || r.calls != 1 || r.prompts[0] != tc.want {
					t.Fatalf("err=%v prompts=%q want=%q", err, r.prompts, tc.want)
				}
				t.Logf("rendered-prompt %s-%s %q", model, tc.name, r.prompts[0])
			})
		}
	}
}

func TestShortcutPersistentAndContextPrompts(t *testing.T) {
	history := []store.Message{{Role: "system", Content: "Reply READY."}, {Role: "developer", Content: "No punctuation."}}
	want := "Prompt:\n" + shortcutReplay + "\n\nInstructions:\nReply READY.\n\nNo punctuation."
	for _, model := range []runner.Model{runner.ModelCloud, runner.ModelCloudPro, runner.ModelOnDevice, runner.ModelChatGPT} {
		for _, strategy := range []string{"strict", "drop-oldest", "sliding-window"} {
			t.Run(string(model)+"/"+strategy, func(t *testing.T) {
				r := &recordingRunner{response: "READY"}
				ctx := policyContext(strategy, 1, 1024, nil)
				_, _, err := executeTurn(ctx, history, model, "Say hello.", func() runner.Runner { return r }, time.Second)
				if err != nil || r.calls != 1 || r.prompts[0] != want {
					t.Fatalf("err=%v prompts=%q", err, r.prompts)
				}
				t.Logf("rendered-prompt persistent-%s-%s %q", model, strategy, r.prompts[0])
			})
		}
	}
}

func TestShortcutImageInstructionsLast(t *testing.T) {
	history := []store.Message{{Role: "system", Content: "Use flat colors."}, {Role: "developer", Content: "No text."}, {Role: "user", Content: "Draw a tree."}}
	for _, reference := range []bool{false, true} {
		got, err := renderChatImagePromptWithReference(history, "Make it autumn.", reference)
		want := "Prompt:\nUSER:\nDraw a tree.\n\nUSER:\nMake it autumn.\n\nInstructions:\nUse flat colors.\n\nNo text."
		if reference {
			want = "Prompt:\nRequested revision:\nMake it autumn.\n\nPrevious image context:\nDraw a tree.\n\nInstructions:\nUse flat colors.\n\nNo text."
		}
		if err != nil || got != want {
			t.Fatalf("reference=%t got=%q err=%v", reference, got, err)
		}
		t.Logf("rendered-prompt image-reference-%t %q", reference, got)
	}
}
