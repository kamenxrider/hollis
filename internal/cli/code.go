// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"strings"
)

// firstFencedBlock returns the contents of the first complete fenced code block
// in text. A fence is a line whose first non-space characters are at least
// three backticks or tildes, optionally followed by an info string; it closes at
// the next line that carries the same character, at least as many of them, and
// nothing else. A fence that is never closed is not a block, so the caller can
// fall back to the whole response.
func firstFencedBlock(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		char, length, ok := fenceOpening(line)
		if !ok {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if isFenceClosing(lines[j], char, length) {
				return strings.Join(lines[i+1:j], "\n"), true
			}
		}
		return "", false
	}
	return "", false
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

// codeAbsentErr is the loud, coded failure for --code with nothing to print.
// It shares exit 7 with timeout, matching the documented output contract; the
// message distinguishes the cause because --code rejects JSON output.
func codeAbsentErr() error {
	return &cliError{
		code:        7,
		machineCode: "code_absent",
		err:         errors.New("no code to print: the response contained no code block or text"),
	}
}
