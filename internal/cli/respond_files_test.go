// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
)

type documentRecordingRunner struct {
	calls  int
	model  runner.Model
	prompt string
}

func (r *documentRecordingRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	r.model = model
	r.prompt = prompt
	return "document fixture response", model, nil
}

func writeRespondFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func withNonInteractiveStdin(t *testing.T) {
	t.Helper()
	old := interactiveStdin
	interactiveStdin = func() bool { return false }
	t.Cleanup(func() { interactiveStdin = old })
}

func TestRespondPromptFilePreservesTextAndModelPrecedence(t *testing.T) {
	withNonInteractiveStdin(t)
	promptPath := writeRespondFile(t, "instructions.txt", "First line\nsecond line\n")
	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--model", "chatgpt", "--prompt-file", promptPath, "model", "on-device"})
	cmd.SetIn(&bytes.Buffer{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.model != runner.ModelOnDevice || r.prompt != "First line\nsecond line\n" {
		t.Fatalf("runner call = %+v", r)
	}
	if out.String() != "document fixture response\n" {
		t.Fatalf("stdout = %q, want document response with trailing newline", out.String())
	}
}

func TestRespondFilesArePreparedInFlagOrder(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	firstPath := writeRespondFile(t, "first source.md", "alpha\n")
	secondPath := writeRespondFile(t, "second.txt", "beta\n")
	first, err := docinput.ReadDocument(firstPath, int64(chat.MaxRenderedPromptBytes))
	if err != nil {
		t.Fatal(err)
	}
	second, err := docinput.ReadDocument(secondPath, int64(chat.MaxRenderedPromptBytes))
	if err != nil {
		t.Fatal(err)
	}
	want, err := docinput.Prepare("Compare them", []docinput.Document{first, second}, chat.MaxRenderedPromptBytes)
	if err != nil {
		t.Fatal(err)
	}

	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--file", firstPath, "--file", secondPath, "Compare", "them"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.prompt != want {
		t.Fatalf("runner calls=%d prompt=%q, want %q", r.calls, r.prompt, want)
	}
}

func TestRespondPromptFileCanSupplyImageInstruction(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	promptPath := writeRespondFile(t, "instructions.txt", "Describe the image")
	imagePath := writeCLIImage(t, "fixture image.png")
	r := &imageRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--prompt-file", promptPath, "--image", imagePath})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.model != runner.ModelCloud || r.prompt != "Describe the image" || len(r.imagePaths) != 1 || r.imagePaths[0] != imagePath {
		t.Fatalf("image runner call = %+v", r)
	}
}

func TestRespondNewInputConflictsExit2BeforeRunner(t *testing.T) {
	withNonInteractiveStdin(t)
	promptPath := writeRespondFile(t, "instructions.txt", "Use the sources")
	documentPath := writeRespondFile(t, "source.md", "source material")
	imagePath := writeCLIImage(t, "image.png")

	tests := []struct {
		name string
		args []string
		in   string
	}{
		{name: "positional and prompt file", args: []string{"respond", "--prompt-file", promptPath, "also positional"}},
		{name: "document and image", args: []string{"respond", "--file", documentPath, "--image", imagePath, "Explain"}},
		{name: "document without any instruction", args: []string{"respond", "--file", documentPath}},
		{name: "image without any instruction", args: []string{"respond", "--image", imagePath}},
		{name: "prompt file and piped stdin", args: []string{"respond", "--prompt-file", promptPath}, in: "second instruction"},
		{name: "positional and piped stdin with a document", args: []string{"respond", "--file", documentPath, "Explain"}, in: "second instruction"},
		{name: "explicit empty prompt file", args: []string{"respond", "--prompt-file="}},
		{name: "explicit empty document", args: []string{"respond", "--file=", "Explain"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &documentRecordingRunner{}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs(tc.args)
			cmd.SetIn(strings.NewReader(tc.in))
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage exit 2", err, ExitCode(err))
			}
			if r.calls != 0 {
				t.Fatalf("runner calls=%d, want 0", r.calls)
			}
		})
	}
}

func TestRespondInvalidDocumentFailsBeforeRunner(t *testing.T) {
	withNonInteractiveStdin(t)
	tests := []struct {
		name string
		path string
	}{
		{name: "unsupported extension", path: writeRespondFile(t, "source.rtf", "source")},
		{name: "missing file", path: filepath.Join(t.TempDir(), "missing.md")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &documentRecordingRunner{}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs([]string{"respond", "--file", tc.path, "Summarize"})
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil || ExitCode(err) != 2 || r.calls != 0 {
				t.Fatalf("err=%v exit=%d calls=%d, want pre-run usage error", err, ExitCode(err), r.calls)
			}
		})
	}
}

func TestRespondPreparedDocumentOverflowFailsBeforeRunner(t *testing.T) {
	withNonInteractiveStdin(t)
	documentPath := writeRespondFile(t, "large.txt", strings.Repeat("x", chat.MaxRenderedPromptBytes))
	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--file", documentPath, "Summarize"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 2 || r.calls != 0 {
		t.Fatalf("err=%v exit=%d calls=%d, want pre-run usage error", err, ExitCode(err), r.calls)
	}
}

func TestRespondLegacyPositionalAndStdinBehaviorRemainsValid(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	tests := []struct {
		name       string
		args       []string
		in         string
		wantPrompt string
	}{
		{name: "positional ignores incidental stdin", args: []string{"respond", "positional", "instruction"}, in: "legacy ignored input", wantPrompt: "positional instruction"},
		{name: "stdin remains an instruction source", args: []string{"respond"}, in: "piped instruction\n", wantPrompt: "piped instruction\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &documentRecordingRunner{}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs(tc.args)
			cmd.SetIn(strings.NewReader(tc.in))
			cmd.SetOut(&bytes.Buffer{})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if r.calls != 1 || r.prompt != tc.wantPrompt {
				t.Fatalf("runner calls=%d prompt=%q, want %q", r.calls, r.prompt, tc.wantPrompt)
			}
		})
	}
}

func TestRespondPipedStdinSuppliesDocumentInstruction(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	documentPath := writeRespondFile(t, "notes.md", "alpha\n")
	document, err := docinput.ReadDocument(documentPath, int64(chat.MaxRenderedPromptBytes))
	if err != nil {
		t.Fatal(err)
	}
	want, err := docinput.Prepare("Summarize this\n", []docinput.Document{document}, chat.MaxRenderedPromptBytes)
	if err != nil {
		t.Fatal(err)
	}
	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--file", documentPath})
	cmd.SetIn(strings.NewReader("Summarize this\n"))
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.prompt != want {
		t.Fatalf("runner calls=%d prompt=%q, want %q", r.calls, r.prompt, want)
	}
}

func TestRespondPipedStdinSuppliesImageInstruction(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	imagePath := writeCLIImage(t, "fixture image.png")
	r := &imageRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--image", imagePath})
	cmd.SetIn(strings.NewReader("Describe this"))
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.prompt != "Describe this" || len(r.imagePaths) != 1 {
		t.Fatalf("image runner call = %+v", r)
	}
}

func TestRespondPromptFileDashReadsStdin(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	documentPath := writeRespondFile(t, "source.md", "source material")
	document, err := docinput.ReadDocument(documentPath, int64(chat.MaxRenderedPromptBytes))
	if err != nil {
		t.Fatal(err)
	}
	want, err := docinput.Prepare("First line\nsecond line\n", []docinput.Document{document}, chat.MaxRenderedPromptBytes)
	if err != nil {
		t.Fatal(err)
	}
	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--prompt-file", "-", "--file", documentPath})
	cmd.SetIn(strings.NewReader("First line\nsecond line\n"))
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.prompt != want {
		t.Fatalf("runner calls=%d prompt=%q, want %q", r.calls, r.prompt, want)
	}
}

// TestRespondOpenEmptyPipeDoesNotBlockFileInput is the regression test for the
// measured hang: a pipe held open with no data used to stall file input until
// the writer produced a byte or closed.
func TestRespondOpenEmptyPipeDoesNotBlockFileInput(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	documentPath := writeRespondFile(t, "source.md", "source material")
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close() // deliberately left open with no data

	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--file", documentPath})
	cmd.SetIn(readEnd)
	cmd.SetOut(&bytes.Buffer{})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case execErr := <-done:
		if execErr == nil || ExitCode(execErr) != 2 {
			t.Fatalf("err=%v exit=%d, want usage exit 2", execErr, ExitCode(execErr))
		}
		if !strings.Contains(execErr.Error(), "pipe it via stdin") {
			t.Fatalf("error %q does not name the stdin instruction path", execErr)
		}
		if r.calls != 0 {
			t.Fatalf("runner calls=%d, want 0", r.calls)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("file input blocked on an open empty pipe")
	}
}

// TestRespondOpenEmptyPipeDoesNotBlockPositionalFileInput covers the same
// regression when the instruction is positional: the probe that looks for a
// competing piped instruction must not wait for data that never arrives.
func TestRespondOpenEmptyPipeDoesNotBlockPositionalFileInput(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	documentPath := writeRespondFile(t, "source.md", "source material")
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()

	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--file", documentPath, "Summarize"})
	cmd.SetIn(readEnd)
	cmd.SetOut(&bytes.Buffer{})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case execErr := <-done:
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
		if r.calls != 1 || !strings.HasPrefix(r.prompt, "Summarize\n\n") {
			t.Fatalf("runner calls=%d prompt=%q", r.calls, r.prompt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("positional file input blocked on an open empty pipe")
	}
}

func TestRespondPipedInstructionArrivingShortlyAfterStart(t *testing.T) {
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	documentPath := writeRespondFile(t, "notes.md", "alpha\n")
	document, err := docinput.ReadDocument(documentPath, int64(chat.MaxRenderedPromptBytes))
	if err != nil {
		t.Fatal(err)
	}
	want, err := docinput.Prepare("Summarize this", []docinput.Document{document}, chat.MaxRenderedPromptBytes)
	if err != nil {
		t.Fatal(err)
	}
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	go func() {
		defer writeEnd.Close()
		time.Sleep(50 * time.Millisecond)
		_, _ = writeEnd.WriteString("Summarize this")
	}()
	r := &documentRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--file", documentPath})
	cmd.SetIn(readEnd)
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.prompt != want {
		t.Fatalf("runner calls=%d prompt=%q, want %q", r.calls, r.prompt, want)
	}
}

func TestRespondHelpDocumentsFileFlags(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &documentRecordingRunner{} })
	cmd.SetArgs([]string{"respond", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"--prompt-file", "--file", ".txt", ".md", "Documents cannot be mixed with images"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help missing %q:\n%s", want, out.String())
		}
	}
}
