// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/cli"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
)

const (
	documentInputPrompt     = "Summarize the differences."
	documentInputFirstBody  = "ALPHA-DOCUMENT-MARKER first body\n"
	documentInputSecondBody = "BETA-DOCUMENT-MARKER second body\n"
)

type documentRecordingRunner struct {
	calls      int
	model      runner.Model
	prompt     string
	imagePaths []string
}

func (r *documentRecordingRunner) Run(ctx context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	if ctx == nil {
		return "", model, errors.New("runner received nil context")
	}
	r.model = model
	r.prompt = prompt
	return "recorded response", model, nil
}

func (r *documentRecordingRunner) RunWithImages(ctx context.Context, model runner.Model, prompt string, imagePaths []string) (string, runner.Model, error) {
	r.calls++
	if ctx == nil {
		return "", model, errors.New("runner received nil context")
	}
	r.model = model
	r.prompt = prompt
	r.imagePaths = append([]string(nil), imagePaths...)
	return "recorded image response", model, nil
}

func writeDocumentInputFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func executeDocumentInput(t *testing.T, r *documentRecordingRunner, args []string, stdin string) error {
	t.Helper()
	t.Setenv("HOLLIS_STATE_DIR", t.TempDir())
	cmd := cli.NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(stdin))
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	return cmd.Execute()
}

func readDocumentInputFixture(t *testing.T, paths ...string) []docinput.Document {
	t.Helper()
	documents := make([]docinput.Document, 0, len(paths))
	for _, path := range paths {
		doc, err := docinput.ReadDocument(path, chat.MaxRenderedPromptBytes)
		if err != nil {
			t.Fatalf("ReadDocument(%q): %v", filepath.Base(path), err)
		}
		documents = append(documents, doc)
	}
	return documents
}

func TestDocumentInputSharedPreparationContract(t *testing.T) {
	first := writeDocumentInputFile(t, "first note.md", documentInputFirstBody)
	second := writeDocumentInputFile(t, "second note.txt", documentInputSecondBody)
	promptPath := writeDocumentInputFile(t, "instructions.txt", documentInputPrompt)
	symlinkTarget := writeDocumentInputFile(t, "target.txt", documentInputSecondBody)
	symlink := filepath.Join(t.TempDir(), "linked document.md")
	if err := os.Symlink(symlinkTarget, symlink); err != nil {
		t.Fatal(err)
	}

	prompt, err := docinput.ReadPromptFile(promptPath, chat.MaxRenderedPromptBytes)
	if err != nil {
		t.Fatalf("ReadPromptFile: %v", err)
	}
	if prompt != documentInputPrompt {
		t.Fatalf("prompt = %q, want %q", prompt, documentInputPrompt)
	}

	doc, err := docinput.ReadDocument(first, chat.MaxRenderedPromptBytes)
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	if doc.Name != "first note.md" || doc.Text != documentInputFirstBody {
		t.Fatalf("document = %#v", doc)
	}

	documents := readDocumentInputFixture(t, first, symlink, second)
	prepared, err := docinput.Prepare(documentInputPrompt, documents, chat.MaxRenderedPromptBytes)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, marker := range []string{documentInputPrompt, "first note.md", documentInputFirstBody, "linked document.md", "second note.txt", documentInputSecondBody} {
		if !strings.Contains(prepared, marker) {
			t.Fatalf("prepared prompt missing %q:\n%s", marker, prepared)
		}
	}
	firstIndex := strings.Index(prepared, documentInputFirstBody)
	secondIndex := strings.Index(prepared, documentInputSecondBody)
	if firstIndex < 0 || secondIndex < firstIndex {
		t.Fatalf("documents were not assembled in argument order:\n%s", prepared)
	}
	if len(prepared) > chat.MaxRenderedPromptBytes {
		t.Fatalf("prepared prompt = %d bytes, want <= %d", len(prepared), chat.MaxRenderedPromptBytes)
	}
}

func TestDocumentInputRejectsUnsafeOrOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		path string
	}{
		{name: "directory", path: dir},
		{name: "empty document", path: writeDocumentInputFile(t, "empty.md", "")},
		{name: "invalid UTF-8 document", path: writeDocumentInputFile(t, "invalid.md", "\xff\xfe")},
		{name: "overflowing document", path: writeDocumentInputFile(t, "overflow.md", strings.Repeat("x", chat.MaxRenderedPromptBytes+1))},
		{name: "unsupported extension", path: writeDocumentInputFile(t, "unsupported.pdf", "not guaranteed")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := docinput.ReadDocument(tc.path, chat.MaxRenderedPromptBytes); err == nil {
				t.Fatal("ReadDocument accepted invalid input")
			}
			if _, err := docinput.ReadPromptFile(tc.path, chat.MaxRenderedPromptBytes); tc.name != "unsupported extension" && err == nil {
				t.Fatal("ReadPromptFile accepted invalid input")
			}
		})
	}
}

func TestDocumentInputRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := docinput.ReadDocument(path, chat.MaxRenderedPromptBytes)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("ReadDocument accepted a FIFO")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ReadDocument blocked while inspecting a FIFO")
	}
}

func TestDocumentInputRejectsFinalOverflow(t *testing.T) {
	half := writeDocumentInputFile(t, "first half.md", strings.Repeat("x", chat.MaxRenderedPromptBytes/2+1024))
	otherHalf := writeDocumentInputFile(t, "second half.txt", strings.Repeat("y", chat.MaxRenderedPromptBytes/2+1024))
	documents := readDocumentInputFixture(t, half, otherHalf)
	if _, err := docinput.Prepare(documentInputPrompt, documents, chat.MaxRenderedPromptBytes); err == nil {
		t.Fatal("Prepare accepted an overflowing final prompt")
	}
}

func TestRespondPromptFileContract(t *testing.T) {
	promptPath := writeDocumentInputFile(t, "instructions.txt", documentInputPrompt)
	r := &documentRecordingRunner{}

	err := executeDocumentInput(t, r, []string{"respond", "--prompt-file", promptPath, "--json", "--model", "cloud"}, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.model != runner.ModelCloud || r.prompt != documentInputPrompt {
		t.Fatalf("runner call calls=%d model=%q prompt=%q", r.calls, r.model, r.prompt)
	}
}

func TestRespondDocumentContract(t *testing.T) {
	first := writeDocumentInputFile(t, "first note.md", documentInputFirstBody)
	second := writeDocumentInputFile(t, "second note.txt", documentInputSecondBody)
	r := &documentRecordingRunner{}

	err := executeDocumentInput(t, r, []string{"respond", documentInputPrompt, "--file", first, "--file", second}, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", r.calls)
	}
	for _, marker := range []string{documentInputPrompt, "first note.md", documentInputFirstBody, "second note.txt", documentInputSecondBody} {
		if !strings.Contains(r.prompt, marker) {
			t.Fatalf("prompt missing %q:\n%s", marker, r.prompt)
		}
	}
	firstIndex := strings.Index(r.prompt, documentInputFirstBody)
	secondIndex := strings.Index(r.prompt, documentInputSecondBody)
	if firstIndex < 0 || secondIndex < firstIndex {
		t.Fatalf("documents out of order:\n%s", r.prompt)
	}
	if len(r.prompt) > chat.MaxRenderedPromptBytes {
		t.Fatalf("prompt = %d bytes, want <= %d", len(r.prompt), chat.MaxRenderedPromptBytes)
	}
}

func TestRespondPromptFileWithImageContract(t *testing.T) {
	promptPath := writeDocumentInputFile(t, "instructions.txt", documentInputPrompt)
	image := filepath.Join(t.TempDir(), "photo image.png")
	if err := os.WriteFile(image, []byte("fixture image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &documentRecordingRunner{}

	err := executeDocumentInput(t, r, []string{"respond", "--prompt-file", promptPath, "--image", image, "--model", "cloud"}, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.model != runner.ModelCloud || r.prompt != documentInputPrompt {
		t.Fatalf("runner call calls=%d model=%q prompt=%q", r.calls, r.model, r.prompt)
	}
	if len(r.imagePaths) != 1 || r.imagePaths[0] != image {
		t.Fatalf("image paths = %#v", r.imagePaths)
	}
}

func TestRespondDocumentInputValidationPreventsRunnerCalls(t *testing.T) {
	promptPath := writeDocumentInputFile(t, "instructions.txt", documentInputPrompt)
	document := writeDocumentInputFile(t, "document.txt", "body\n")
	missing := filepath.Join(t.TempDir(), "missing.txt")
	dir := t.TempDir()

	tests := []struct {
		name string
		args []string
		in   string
	}{
		{name: "positional with prompt file", args: []string{"respond", "--prompt-file", promptPath, "extra"}},
		{name: "stdin with prompt file", args: []string{"respond", "--prompt-file", promptPath}, in: "stdin instruction\n"},
		{name: "documents with stdin", args: []string{"respond", "--file", document}, in: "stdin instruction\n"},
		{name: "documents and images", args: []string{"respond", "summarize", "--file", document, "--image", document}},
		{name: "missing prompt file", args: []string{"respond", "--prompt-file", missing}},
		{name: "directory prompt file", args: []string{"respond", "--prompt-file", dir}},
		{name: "unsupported document", args: []string{"respond", "summarize", "--file", writeDocumentInputFile(t, "unsupported.pdf", "not guaranteed")}},
		{name: "missing document", args: []string{"respond", "summarize", "--file", missing}},
		{name: "directory document", args: []string{"respond", "summarize", "--file", dir}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &documentRecordingRunner{}
			err := executeDocumentInput(t, r, tc.args, tc.in)
			if err == nil {
				t.Fatal("Execute accepted invalid document input")
			}
			if got := cli.ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2; err=%v", got, err)
			}
			if r.calls != 0 {
				t.Fatalf("runner calls = %d, want 0", r.calls)
			}
		})
	}
}

func TestRespondDocumentInputDoesNotLeakLocalPathsOrContents(t *testing.T) {
	invalid := writeDocumentInputFile(t, "private marker.md", "\xff secret body")
	r := &documentRecordingRunner{}
	err := executeDocumentInput(t, r, []string{"respond", "summarize", "--file", invalid}, "")
	if err == nil || cli.ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want usage 2", err, cli.ExitCode(err))
	}
	if strings.Contains(err.Error(), invalid) || strings.Contains(err.Error(), "secret body") {
		t.Fatalf("routine diagnostic leaked local details: %v", err)
	}
	if r.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", r.calls)
	}
}

func TestDocumentInputProcessLevelJSONContract(t *testing.T) {
	promptPath := writeDocumentInputFile(t, "instructions.txt", documentInputPrompt)
	result := runHelper(t, t.TempDir(), "respond", "--json", "--model", "cloud", "--prompt-file", promptPath)
	if result.exit != 0 || result.stderr != "" {
		t.Fatalf("respond: exit=%d stderr=%s stdout=%s", result.exit, result.stderr, result.stdout)
	}
	var response map[string]any
	decodeObject(t, result.stdout, &response)
	if response["model_requested"] != "cloud" || response["model_used"] != "cloud" {
		t.Fatalf("model metadata: %#v", response)
	}
	want := fmt.Sprintf("fixture response for %s", documentInputPrompt)
	if response["response"] != want {
		t.Fatalf("response = %#v, want %#v", response["response"], want)
	}
}
