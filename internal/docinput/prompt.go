// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package docinput

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

// ReadPromptFile reads one local UTF-8 instruction file with a bounded
// maxBytes read plus a one-byte overflow probe. The returned text preserves
// the file bytes exactly. Empty or whitespace-only instructions, invalid
// UTF-8, special files, and content larger than maxBytes are rejected before a
// caller can submit the prompt.
//
// The initial Stat check rejects FIFOs and other special files before opening
// them. O_NONBLOCK plus the descriptor Stat check closes the race where the
// path changes between those checks, so a FIFO cannot make this function wait
// for a writer.
func ReadPromptFile(path string, maxBytes int64) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("prompt file path must not be empty")
	}
	if maxBytes < 0 {
		return "", errors.New("prompt file byte limit must not be negative")
	}

	label := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect prompt file %q: %w", label, classifyFileError(err))
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("prompt file %q must be a regular file", label)
	}

	// O_NONBLOCK is ignored for regular files. It matters for a raced path
	// replacement: opening a FIFO without it would block before f.Stat runs.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("open prompt file %q: %w", label, classifyFileError(err))
	}
	data, readErr := readPromptBytes(file, maxBytes)
	closeErr := file.Close()
	if readErr != nil {
		return "", fmt.Errorf("read prompt file %q: %w", label, readErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close prompt file %q: %w", label, classifyFileError(closeErr))
	}

	if !utf8.Valid(data) {
		return "", errors.New("prompt file must contain valid UTF-8")
	}
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return "", errors.New("prompt file must contain a non-empty instruction")
	}
	return text, nil
}

// readPromptBytes validates the opened handle before reading it. The caller
// must close file, including when this helper returns an error.
func readPromptBytes(file *os.File, maxBytes int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("validate opened prompt file: %w", classifyFileError(err))
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("opened prompt file must be a regular file")
	}

	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return nil, classifyFileError(err)
	}
	var extra [1]byte
	n, err := file.Read(extra[:])
	if n != 0 {
		return nil, fmt.Errorf("prompt file exceeds the %d-byte limit", maxBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, classifyFileError(err)
	}
	return data, nil
}

// Avoid propagating os.PathError, whose Error method includes the caller's
// path. Retain the standard sentinel where it is useful to callers while
// keeping routine diagnostics free of absolute source paths.
func classifyFileError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fs.ErrNotExist
	case errors.Is(err, fs.ErrPermission):
		return fs.ErrPermission
	case errors.Is(err, fs.ErrClosed):
		return fs.ErrClosed
	case errors.Is(err, fs.ErrInvalid):
		return fs.ErrInvalid
	case errors.Is(err, io.ErrNoProgress):
		return io.ErrNoProgress
	case errors.Is(err, io.ErrUnexpectedEOF):
		return io.ErrUnexpectedEOF
	default:
		return errors.New("file access failed")
	}
}
