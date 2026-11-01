// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package docinput

import (
	"errors"
	"io"
	"io/fs"
	"strings"
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

	data, err := ReadFile(path, "prompt file", maxBytes)
	if err != nil {
		return "", err
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

// classifyFileError keeps routine diagnostics free of the caller's path.
// os.PathError embeds that path in its Error method, so it is never propagated;
// the standard sentinel is retained where it is useful to callers.
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
