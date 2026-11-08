// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package jsonoutput

import "testing"

func TestObject(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		ok          bool
	}{
		{`{"n":9007199254740993}`, `{"n":9007199254740993}`, true},
		{" \n{} \t", "{}", true},
		{"```json\n{\"ok\":true}\n```", `{"ok":true}`, true},
		{"```\r\n{}\r\n```", "{}", true},
		{"Here is JSON:\n{}", "", false},
		{"```json\n{}\n```\nThanks", "", false},
		{"```json\n{}\n```\n```json\n{}\n```", "", false},
		{"```python\n{}\n```", "", false},
		{"[]", "", false}, {"null", "", false}, {`"text"`, "", false},
		{`{"broken":`, "", false}, {"", "", false}, {"{} {}", "", false},
	} {
		got, ok := Object(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("input=%q got=%q ok=%t", tc.input, got, ok)
		}
	}
}
