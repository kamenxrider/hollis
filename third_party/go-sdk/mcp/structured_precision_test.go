// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.
// Added by Hollis: scoped structured-content precision regressions.

package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestStructuredPrecisionNumbers(t *testing.T) {
	for _, value := range []string{
		"42", "9007199254740993", "-9007199254740993",
		"0.123456789012345678901", "1e400", "1e-400", "-0", "1.2300E+12",
	} {
		t.Run(value, func(t *testing.T) {
			for _, shape := range []string{value, `{"nested":[` + value + `]}`} {
				var result CallToolResult
				if err := json.Unmarshal([]byte(`{"content":[],"structuredContent":`+shape+`}`), &result); err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(result.StructuredContent)
				if err != nil || string(got) != shape {
					t.Fatalf("roundtrip = %s, %v; want %s", got, err, shape)
				}
				leaf := result.StructuredContent
				if object, ok := leaf.(map[string]any); ok {
					leaf = object["nested"].([]any)[0]
				}
				if number, ok := leaf.(json.Number); !ok || number.String() != value {
					t.Fatalf("numeric leaf = %#v (%T)", leaf, leaf)
				}
			}
		})
	}
}

func TestStructuredPrecisionTypesAndEnvelope(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want any
	}{
		{`{}`, nil},
		{`{"structuredContent":null}`, nil},
		{`{"structuredContent":"hello"}`, "hello"},
		{`{"structuredContent":true}`, true},
		{`{"structuredContent":[null,false,"x",42]}`, []any{nil, false, "x", json.Number("42")}},
		{`{"structuredContent":{"n":null,"s":"x","b":true}}`, map[string]any{"n": nil, "s": "x", "b": true}},
		{`{"StructuredContent":42}`, nil},
	} {
		var result CallToolResult
		if err := json.Unmarshal([]byte(tc.wire), &result); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.StructuredContent, tc.want) {
			t.Fatalf("%s: got %#v; want %#v", tc.wire, result.StructuredContent, tc.want)
		}
	}
	var result CallToolResult
	wire := `{"_meta":{"n":42},"content":[{"type":"text","text":"kept"}],"isError":true,"structuredContent":42,"requestState":"state","resultType":"input_required","inputRequests":{}}`
	if err := json.Unmarshal([]byte(wire), &result); err != nil {
		t.Fatal(err)
	}
	if result.Meta["n"] != float64(42) || !result.IsError || !result.NeedsInput() || result.RequestState != "state" || result.InputRequests == nil {
		t.Fatalf("envelope changed: %+v", result)
	}
	if len(result.Content) != 1 || result.Content[0].(*TextContent).Text != "kept" {
		t.Fatalf("content changed: %#v", result.Content)
	}
}

func TestStructuredPrecisionDepthAndErrors(t *testing.T) {
	for _, depth := range []int{999, 1000} {
		wire := `{"structuredContent":` + strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth) + "}"
		result := CallToolResult{StructuredContent: "unchanged"}
		err := json.Unmarshal([]byte(wire), &result)
		if depth == 999 && err != nil {
			t.Fatalf("valid depth rejected: %v", err)
		}
		if depth == 1000 && (err == nil || result.StructuredContent != "unchanged") {
			t.Fatalf("excess depth: error=%v, destination=%#v", err, result.StructuredContent)
		}
	}
	for _, wire := range []string{
		`{"structuredContent":01}`, `{"structuredContent":NaN}`,
		`{"structuredContent":`, `{"content":[{"type":"invalid"}],"structuredContent":42}`,
	} {
		result := CallToolResult{StructuredContent: "unchanged"}
		if err := json.Unmarshal([]byte(wire), &result); err == nil || result.StructuredContent != "unchanged" {
			t.Fatalf("invalid result %s: error=%v, destination=%#v", wire, err, result.StructuredContent)
		}
	}
}
