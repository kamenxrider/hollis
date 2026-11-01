// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package docinput

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ReadFile reads one local file under the same safe-input rules used for
// prompt and document content: basename-only diagnostics, errors classified so
// that the caller's path never appears in a message, a regular-file check
// before reading, and a non-blocking open so a raced FIFO replacement cannot
// block. label names the input in messages ("prompt file", "schema file").
//
// Content larger than maxBytes fails on a one-byte overflow probe instead of
// being read and then discarded.
func ReadFile(path, label string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errors.New("file byte limit must not be negative")
	}

	name := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s %q: %w", label, name, classifyFileError(err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %q must be a regular file", label, name)
	}

	// O_NONBLOCK is ignored for regular files. It matters for a raced path
	// replacement: opening a FIFO without it would block before the descriptor
	// check below runs.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s %q: %w", label, name, classifyFileError(err))
	}
	data, readErr := readBoundedFile(file, label, maxBytes)
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read %s %q: %w", label, name, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %s %q: %w", label, name, classifyFileError(closeErr))
	}
	return data, nil
}

// readBoundedFile validates the opened handle before reading it. The caller
// must close file, including when this helper returns an error.
func readBoundedFile(file *os.File, label string, maxBytes int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("validate opened %s: %w", label, classifyFileError(err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("opened %s must be a regular file", label)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return nil, classifyFileError(err)
	}
	var extra [1]byte
	n, err := file.Read(extra[:])
	if n != 0 {
		return nil, fmt.Errorf("%s exceeds the %d-byte limit", label, maxBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, classifyFileError(err)
	}
	return data, nil
}
