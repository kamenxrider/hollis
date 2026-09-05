// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package docinput

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	documentMarkerPrefix = "--- Document "
	documentMarkerSuffix = " ---"
	documentEndMarker    = "--- End document ---"
)

// ReadDocument reads one local UTF-8 text or Markdown document with a bounded
// maxBytes read plus a one-byte overflow probe. The returned Name is the
// source basename; Text preserves the file bytes exactly. Empty,
// whitespace-only, invalid UTF-8, unsupported, special, and oversized files
// are rejected.
func ReadDocument(path string, maxBytes int64) (Document, error) {
	if strings.TrimSpace(path) == "" {
		return Document{}, errors.New("document path must not be empty")
	}
	if maxBytes < 0 {
		return Document{}, errors.New("document byte limit must not be negative")
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md":
	default:
		return Document{}, errors.New("document extension is not supported; use .txt or .md")
	}

	name := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		return Document{}, fmt.Errorf("inspect document %q: %w", name, classifyFileError(err))
	}
	if !info.Mode().IsRegular() {
		return Document{}, fmt.Errorf("document %q must be a regular file", name)
	}

	// O_NONBLOCK prevents a raced path replacement from blocking on a FIFO
	// before readPromptBytes can validate the opened descriptor.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Document{}, fmt.Errorf("open document %q: %w", name, classifyFileError(err))
	}
	data, readErr := readPromptBytes(file, maxBytes)
	closeErr := file.Close()
	if readErr != nil {
		return Document{}, fmt.Errorf("read document %q: %w", name, readErr)
	}
	if closeErr != nil {
		return Document{}, fmt.Errorf("close document %q: %w", name, classifyFileError(closeErr))
	}

	if !utf8.Valid(data) {
		return Document{}, errors.New("document must contain valid UTF-8")
	}
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return Document{}, errors.New("document must contain non-empty content")
	}
	return Document{Name: name, Text: text}, nil
}

// Prepare renders an instruction followed by each document in input order.
// It is pure and does not truncate: the exact final byte size is checked
// against maxBytes before any rendering is performed.
func Prepare(prompt string, documents []Document, maxBytes int) (string, error) {
	if maxBytes < 0 {
		return "", errors.New("prepared prompt byte limit must not be negative")
	}
	if !utf8.ValidString(prompt) {
		return "", errors.New("prompt must contain valid UTF-8")
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt must be non-empty")
	}
	if len(prompt) > maxBytes {
		return "", fmt.Errorf("prepared prompt exceeds the %d-byte limit", maxBytes)
	}
	if len(documents) == 0 {
		return prompt, nil
	}

	for _, document := range documents {
		if !utf8.ValidString(document.Name) {
			return "", errors.New("document name must contain valid UTF-8")
		}
		if !utf8.ValidString(document.Text) {
			return "", errors.New("document content must contain valid UTF-8")
		}
		if strings.TrimSpace(document.Name) == "" {
			return "", errors.New("document name must be non-empty")
		}
		if filepath.Base(document.Name) != document.Name ||
			document.Name == "." || document.Name == ".." {
			return "", errors.New("document name must be a basename")
		}
		if strings.TrimSpace(document.Text) == "" {
			return "", errors.New("document content must be non-empty")
		}
	}

	size := len(prompt) + 2
	for _, document := range documents {
		size += len(documentMarkerPrefix)
		size += len(strconv.Quote(document.Name))
		size += len(documentMarkerSuffix) + 1
		size += len(document.Text) + 1
		size += len(documentEndMarker) + 1
	}
	if size > maxBytes {
		return "", fmt.Errorf("prepared prompt is %d bytes; maximum is %d", size, maxBytes)
	}

	var rendered strings.Builder
	rendered.Grow(size)
	rendered.WriteString(prompt)
	rendered.WriteString("\n\n")
	for _, document := range documents {
		rendered.WriteString(documentMarkerPrefix)
		rendered.WriteString(strconv.Quote(document.Name))
		rendered.WriteString(documentMarkerSuffix)
		rendered.WriteByte('\n')
		rendered.WriteString(document.Text)
		rendered.WriteByte('\n')
		rendered.WriteString(documentEndMarker)
		rendered.WriteByte('\n')
	}
	return rendered.String(), nil
}
