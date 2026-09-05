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
)

func TestReadPromptFileExactLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.txt")
	const want = "hello"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadPromptFile(path, int64(len(want)))
	if err != nil {
		t.Fatalf("ReadPromptFile() error = %v", err)
	}
	if got != want {
		t.Fatalf("ReadPromptFile() = %q, want %q", got, want)
	}
}

func TestReadPromptFileRejectsOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.txt")
	if err := os.WriteFile(path, []byte("hello!"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadPromptFile(path, int64(len("hello")))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadPromptFile() error = %v, want a bounded-read overflow", err)
	}
}

func TestReadPromptFileRejectsEmptyAndWhitespaceInstructions(t *testing.T) {
	for name, content := range map[string]string{
		"empty":      "",
		"whitespace": " \t\n\r",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "instructions.txt")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := ReadPromptFile(path, int64(len(content)))
			if err == nil || !strings.Contains(err.Error(), "non-empty") {
				t.Fatalf("ReadPromptFile() error = %v, want an empty-instruction error", err)
			}
		})
	}
}

func TestReadPromptFileRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.txt")
	content := []byte{'o', 'k', 0xff}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadPromptFile(path, int64(len(content)))
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("ReadPromptFile() error = %v, want invalid UTF-8", err)
	}
}

func TestReadPromptFileRejectsNegativeLimit(t *testing.T) {
	_, err := ReadPromptFile(filepath.Join(t.TempDir(), "instructions.txt"), -1)
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("ReadPromptFile() error = %v, want negative-limit error", err)
	}
}

func TestReadPromptFileRejectsMissingFileWithoutLeakingAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")

	_, err := ReadPromptFile(path, 128)
	if err == nil {
		t.Fatal("ReadPromptFile() accepted a missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadPromptFile() error = %v, want os.ErrNotExist", err)
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("ReadPromptFile() error leaked absolute source path: %v", err)
	}
}

func TestReadPromptFileRejectsDirectory(t *testing.T) {
	_, err := ReadPromptFile(t.TempDir(), 128)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("ReadPromptFile() error = %v, want regular-file error", err)
	}
}

func TestReadPromptBytesClosedHandleDoesNotLeakAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = readPromptBytes(file, 128)
	if err == nil {
		t.Fatal("readPromptBytes() accepted a closed handle")
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("readPromptBytes() error leaked absolute source path: %v", err)
	}
}

func TestReadPromptFileRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := ReadPromptFile(path, 128)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("ReadPromptFile() error = %v, want regular-file error", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ReadPromptFile() blocked while inspecting a FIFO")
	}
}

func TestReadPromptFileAllowsSymlinkToRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "instructions.txt")
	const want = "follow the linked file"
	if err := os.WriteFile(target, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := ReadPromptFile(link, int64(len(want)))
	if err != nil {
		t.Fatalf("ReadPromptFile() error = %v", err)
	}
	if got != want {
		t.Fatalf("ReadPromptFile() = %q, want %q", got, want)
	}
}
