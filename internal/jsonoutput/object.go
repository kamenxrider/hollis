// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

// Package jsonoutput validates complete text without repairing model output.
package jsonoutput

import (
	"encoding/json"
	"strings"
)

const Instruction = "Return exactly one valid JSON object, with no markdown fences or other text."

const InvalidMessage = "The model did not return a valid JSON object. Use -s to clarify the required object, or use --model local --schema for guided output. No retry was attempted."

// Object accepts an object, or one whole fenced object with no surrounding
// prose. It strips only outer whitespace and the optional fence; fields,
// numeric precision and interior formatting remain unchanged.
func Object(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if isObject(text) {
		return text, true
	}
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || (strings.TrimSuffix(first, "\r") != "```" && strings.TrimSuffix(first, "\r") != "```json") {
		return "", false
	}
	if !strings.HasSuffix(rest, "\n```") {
		return "", false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(rest, "\n```"))
	if !isObject(inner) {
		return "", false
	}
	return inner, true
}

func isObject(text string) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal([]byte(text), &object) == nil && object != nil
}
