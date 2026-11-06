// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
// Package promptimage applies only explicit, deterministic one-image wording.
package promptimage

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var words = regexp.MustCompile(`(?i)\b(?:(these|those|both|two|all)[ \t]+)?(images|pictures|photos)\b`)

// Adjust never infers pronoun referents or changes zero/multiple-image input.
func Adjust(prompt string, images int, keep bool) (string, bool) {
	if images != 1 || keep {
		return prompt, false
	}
	var adjusted strings.Builder
	offset := 0
	for _, match := range words.FindAllStringSubmatchIndex(prompt, -1) {
		start, end := match[0], match[1]
		before, _ := utf8.DecodeLastRuneInString(prompt[:start])
		after, _ := utf8.DecodeRuneInString(prompt[end:])
		if unicode.IsLetter(before) || unicode.IsNumber(before) || unicode.IsMark(before) ||
			unicode.IsLetter(after) || unicode.IsNumber(after) || unicode.IsMark(after) {
			continue
		}
		adjusted.WriteString(prompt[offset:start])
		noun := prompt[match[4] : match[5]-1] // drop only the original final s/S
		if match[2] >= 0 {
			original := prompt[match[2]:match[3]]
			determiner := "the"
			switch strings.ToLower(original) {
			case "these":
				determiner = "this"
			case "those":
				determiner = "that"
			}
			if original == strings.ToUpper(original) {
				determiner = strings.ToUpper(determiner)
			} else if unicode.IsUpper([]rune(original)[0]) {
				determiner = strings.ToUpper(determiner[:1]) + determiner[1:]
			}
			adjusted.WriteString(determiner)
			adjusted.WriteString(prompt[match[3]:match[4]])
		}
		adjusted.WriteString(noun)
		offset = end
	}
	adjusted.WriteString(prompt[offset:])
	result := adjusted.String()
	return result, result != prompt
}
