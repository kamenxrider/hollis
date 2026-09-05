// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package docinput

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

const chatMaxPromptBytesForTests = 4096

func TestReadDocumentReadsSupportedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	const want = "local document content"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadDocument(path, int64(len(want)))
	if err != nil {
		t.Fatalf("ReadDocument() error = %v", err)
	}
	if got.Name != "notes.md" {
		t.Fatalf("ReadDocument() Name = %q, want %q", got.Name, "notes.md")
	}
	if got.Text != want {
		t.Fatalf("ReadDocument() Text = %q, want %q", got.Text, want)
	}
}

func TestReadDocumentRejectsUnsupportedExtensions(t *testing.T) {
	for _, name := range []string{"document.pdf", "document.markdown", "document"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := ReadDocument(path, 128)
			if err == nil || !strings.Contains(err.Error(), "not supported") {
				t.Fatalf("ReadDocument() error = %v, want unsupported extension", err)
			}
		})
	}
}

func TestReadDocumentRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		maxBytes int64
		want     string
	}{
		{name: "empty path", path: " ", maxBytes: 128, want: "path must not be empty"},
		{name: "negative limit", path: "notes.txt", maxBytes: -1, want: "negative"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ReadDocument(test.path, test.maxBytes)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadDocument() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadDocumentRejectsMissingFileWithoutLeakingAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")

	_, err := ReadDocument(path, 128)
	if err == nil {
		t.Fatal("ReadDocument() accepted a missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadDocument() error = %v, want os.ErrNotExist", err)
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("ReadDocument() error leaked absolute source path: %v", err)
	}
}

func TestReadDocumentRejectsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := ReadDocument(path, 128)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("ReadDocument() error = %v, want regular-file error", err)
	}
}

func TestReadDocumentRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := ReadDocument(path, 128)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("ReadDocument() error = %v, want regular-file error", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ReadDocument() blocked while inspecting a FIFO")
	}
}

func TestReadDocumentAllowsSymlinkToRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "notes.txt")
	const want = "content behind the symlink"
	if err := os.WriteFile(target, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := ReadDocument(link, int64(len(want)))
	if err != nil {
		t.Fatalf("ReadDocument() error = %v", err)
	}
	if got.Name != "notes.txt" {
		t.Fatalf("ReadDocument() Name = %q, want %q", got.Name, "notes.txt")
	}
	if got.Text != want {
		t.Fatalf("ReadDocument() Text = %q, want %q", got.Text, want)
	}
}

func TestReadDocumentRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	content := []byte{'o', 'k', 0xff}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadDocument(path, int64(len(content)))
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("ReadDocument() error = %v, want invalid UTF-8", err)
	}
}

func TestReadDocumentRejectsEmptyAndWhitespaceContent(t *testing.T) {
	for name, content := range map[string]string{
		"empty":      "",
		"whitespace": " \t\n\r",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notes.txt")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := ReadDocument(path, int64(len(content)))
			if err == nil || !strings.Contains(err.Error(), "non-empty content") {
				t.Fatalf("ReadDocument() error = %v, want non-empty content", err)
			}
		})
	}
}

func TestReadDocumentRejectsOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("content!"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadDocument(path, int64(len("content")))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadDocument() error = %v, want a bounded-read overflow", err)
	}
}

func TestPreparePreservesOrderAndQuotesNames(t *testing.T) {
	documents := []Document{
		{Name: "first.md", Text: "alpha"},
		{Name: `quoted"name.txt`, Text: "beta"},
	}
	const want = "Summarize the differences\n\n" +
		"--- Document \"first.md\" ---\n" +
		"alpha\n" +
		"--- End document ---\n" +
		"--- Document \"quoted\\\"name.txt\" ---\n" +
		"beta\n" +
		"--- End document ---\n"

	got, err := Prepare("Summarize the differences", documents, len(want))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got != want {
		t.Fatalf("Prepare() =\n%q\nwant\n%q", got, want)
	}
	if documents[0].Name != "first.md" || documents[0].Text != "alpha" {
		t.Fatalf("Prepare() mutated first document: %#v", documents[0])
	}
	if documents[1].Name != `quoted"name.txt` || documents[1].Text != "beta" {
		t.Fatalf("Prepare() mutated second document: %#v", documents[1])
	}
}

func TestPrepareRejectsInvalidPromptOrDocument(t *testing.T) {
	invalidUTF8 := "ok\xff"
	tests := []struct {
		name      string
		prompt    string
		documents []Document
		want      string
	}{
		{
			name:      "empty prompt",
			prompt:    " \n",
			documents: nil,
			want:      "prompt must be non-empty",
		},
		{
			name:      "invalid prompt UTF-8",
			prompt:    invalidUTF8,
			documents: nil,
			want:      "prompt must contain valid UTF-8",
		},
		{
			name:      "empty document name",
			prompt:    "prompt",
			documents: []Document{{Name: " ", Text: "content"}},
			want:      "document name must be non-empty",
		},
		{
			name:      "non-basename document name",
			prompt:    "prompt",
			documents: []Document{{Name: filepath.Join("tmp", "notes.md"), Text: "content"}},
			want:      "must be a basename",
		},
		{
			name:      "invalid document name UTF-8",
			prompt:    "prompt",
			documents: []Document{{Name: invalidUTF8, Text: "content"}},
			want:      "document name must contain valid UTF-8",
		},
		{
			name:      "invalid document text UTF-8",
			prompt:    "prompt",
			documents: []Document{{Name: "notes.md", Text: invalidUTF8}},
			want:      "document content must contain valid UTF-8",
		},
		{
			name:      "empty document content",
			prompt:    "prompt",
			documents: []Document{{Name: "notes.md", Text: " \n"}},
			want:      "document content must be non-empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Prepare(test.prompt, test.documents, chatMaxPromptBytesForTests)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Prepare() error = %v, want %q", err, test.want)
			}
			if got != "" {
				t.Fatalf("Prepare() returned %q with an error", got)
			}
		})
	}
}

func TestPrepareRejectsNegativeLimit(t *testing.T) {
	_, err := Prepare("prompt", nil, -1)
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("Prepare() error = %v, want negative-limit error", err)
	}
}

func TestPrepareExactLimit(t *testing.T) {
	want := "Summarize the differences\n\n" +
		"--- Document \"notes.md\" ---\n" +
		"local document content\n" +
		"--- End document ---\n"

	got, err := Prepare("Summarize the differences", []Document{
		{Name: "notes.md", Text: "local document content"},
	}, len(want))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got != want {
		t.Fatalf("Prepare() =\n%q\nwant\n%q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatal("Prepare() returned invalid UTF-8")
	}
}

func TestPrepareRejectsOverflowWithoutSilentTruncation(t *testing.T) {
	want := "Summarize the differences\n\n" +
		"--- Document \"notes.md\" ---\n" +
		"local document content\n" +
		"--- End document ---\n"

	got, err := Prepare("Summarize the differences", []Document{
		{Name: "notes.md", Text: "local document content"},
	}, len(want)-1)
	if err == nil || !strings.Contains(err.Error(), "maximum is") {
		t.Fatalf("Prepare() error = %v, want byte-limit overflow", err)
	}
	if got != "" {
		t.Fatalf("Prepare() returned %q with an error", got)
	}
}

func TestPrepareWithoutDocumentsPreservesExactPrompt(t *testing.T) {
	const prompt = "Exact prompt\n"
	got, err := Prepare(prompt, nil, len(prompt))
	if err != nil || got != prompt {
		t.Fatalf("Prepare = %q, %v; want original prompt", got, err)
	}
	if _, err := Prepare(prompt, nil, len(prompt)-1); err == nil {
		t.Fatal("accepted prompt beyond byte limit")
	}
}

func TestPrepareRejectsPathNameWithoutEchoingIt(t *testing.T) {
	name := filepath.Join(t.TempDir(), "private.md")
	_, err := Prepare("Explain", []Document{{Name: name, Text: "source"}}, 4096)
	if err == nil || strings.Contains(err.Error(), name) {
		t.Fatalf("unexpected or leaking error: %v", err)
	}
}
