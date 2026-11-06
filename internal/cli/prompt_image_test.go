// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func TestImagePromptOffer(t *testing.T) {
	for _, answer := range []string{"Y\n", "\n", "yes\n", "n\n", "", "invalid\n"} {
		var notice bytes.Buffer
		got, changed := offerImagePrompt("Describe these images.", 1, false, true, strings.NewReader(answer), &notice)
		want := answer == "Y\n" || answer == "\n" || answer == "yes\n"
		if changed != want || !strings.Contains(notice.String(), "[Y/n]") || (want && got != "Describe this image.") {
			t.Fatal(answer, got, changed, notice.String())
		}
	}
}

func TestImagePromptCLI(t *testing.T) {
	for _, keep := range []bool{false, true} {
		r := &localImageRecorder{}
		args := []string{"--model", "local", "--image", "/synthetic.png", "--agent"}
		if keep {
			args = append(args, "--keep-prompt")
		}
		args = append(args, "Describe these images.")
		out, notice, err := runRespond(t, r, args...)
		want := "Describe this image."
		if keep {
			want = "Describe these images."
		}
		if err != nil || r.prompt != want || strings.Contains(out, `"prompt_adjusted":true`) == keep || strings.Contains(notice, "adjusted plural") == keep {
			t.Fatalf("keep=%v prompt=%q out=%s notice=%s err=%v", keep, r.prompt, out, notice, err)
		}
	}
}

func TestSavedImagePromptUsesFramingAndPersistsAdjustment(t *testing.T) {
	stubConfigPath(t)
	path := filepath.Join(t.TempDir(), "hollis.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = old })
	r := &localImageRecorder{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"chat", "--model", "local", "--image", "/synthetic.png", "--json", "Describe these images."})
	cmd.SetIn(strings.NewReader(""))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ConversationID string `json:"conversation_id"`
		PromptAdjusted bool   `json:"prompt_adjusted"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil || !payload.PromptAdjusted {
		t.Fatal(out.String(), err)
	}
	if r.prompt != chat.RenderTranscript(nil, "Describe this image.") {
		t.Fatalf("framing changed: %q", r.prompt)
	}
	// The CLI closes its store. Reopen the same database, not a new fixture.
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	messages, err := reopened.Messages(payload.ConversationID)
	if err != nil || len(messages) != 2 || messages[0].Content != "Describe this image." || !messages[0].PromptAdjusted || messages[1].PromptAdjusted {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	conv, err := reopened.GetConversation(payload.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), inputImageTurnKey{}, inputImageTurn{paths: []string{"/synthetic.png"}, adjusted: true})
	if _, err := runTurnResult(ctx, reopened, conv, "Describe this image again.", func() runner.Runner { return r }, 0); err != nil {
		t.Fatal(err)
	}
	if r.prompt != chat.RenderTranscript(messages, "Describe this image again.") {
		t.Fatalf("continuation framing changed: %q", r.prompt)
	}
}

func TestBatchImageWarningKeepsManifestInstruction(t *testing.T) {
	stubConfigPath(t)
	root := t.TempDir()
	input := filepath.Join(root, "input")
	if err := os.Mkdir(input, 0o700); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(root, "prompt.txt")
	if err := os.WriteFile(prompt, []byte("Describe these images."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "a.png"), []byte("synthetic image"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := batch.Plan(batch.PlanOptions{InputDir: input, OutputDir: filepath.Join(root, "output"),
		JobPath: filepath.Join(root, "job.json"), InstructionSource: prompt, Model: runner.ModelCloud})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Warnings) != 1 {
		t.Fatal(job)
	}
	s := batch.NewLocalStore()
	if err := s.Create(t.Context(), job.JobPath, job); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Load(t.Context(), job.JobPath)
	if err != nil || batch.ValidateJob(reloaded) != nil || reloaded.ID != job.ID || reloaded.InstructionSHA256 != job.InstructionSHA256 {
		t.Fatal(reloaded, err)
	}
	r := &batchTestRunner{}
	result, err := batch.Run(context.Background(), job.JobPath, batch.InvocationOptions{MaxCalls: 1},
		&hollisBatchExecutor{newRunner: func() runner.Runner { return r }}, s, realBatchClock{})
	if err != nil || result.Attempted != 1 || r.imagePrompt != "Describe these images." {
		t.Fatal(result, err, r.imagePrompt)
	}
}
