// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import "strings"

// firstFencedBlock returns the first complete fenced code block with content,
// and whether text contains any complete block (even if all are empty).
// A fence is a line whose first non-space characters are at least
// three backticks or tildes, optionally followed by an info string; it closes at
// the next line that carries the same character, at least as many of them, and
// nothing else. A fence that is never closed is not a block, so the caller can
// fall back to the whole response.
func firstFencedBlock(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	complete := false
	for i := 0; i < len(lines); i++ {
		char, length, ok := fenceOpening(lines[i])
		if !ok {
			continue
		}
		j := i + 1
		for j < len(lines) && !isFenceClosing(lines[j], char, length) {
			j++
		}
		if j == len(lines) {
			return "", complete
		}
		complete = true
		block := strings.Join(lines[i+1:j], "\n")
		if strings.TrimSpace(block) != "" {
			return block, true
		}
		i = j
	}
	return "", complete
}

func fenceOpening(line string) (byte, int, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 3 {
		return 0, 0, false
	}
	char := trimmed[0]
	if char != '`' && char != '~' {
		return 0, 0, false
	}
	length := 0
	for length < len(trimmed) && trimmed[length] == char {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	return char, length, true
}

func isFenceClosing(line string, char byte, length int) bool {
	trimmed := strings.TrimLeft(line, " \t")
	marker := 0
	for marker < len(trimmed) && trimmed[marker] == char {
		marker++
	}
	return marker >= length && strings.TrimSpace(trimmed[marker:]) == ""
}
