// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestImagePolicyMessagesArePinned(t *testing.T) {
	for _, model := range []string{"auto", "on-device"} {
		r := &recordingImageRunner{}
		s := NewUnauthenticated(r)
		w := httptest.NewRecorder()
		_, ok := s.validatePreparedModel(w, preparedRequest{
			Model: model, Images: []encodedImage{{}},
		})
		var body struct {
			Error struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if ok || body.Error.Message != "image input requires cloud, cloud-pro, or chatgpt" {
			t.Fatalf("ok=%v body=%s", ok, w.Body.String())
		}
		if r.textCalls != 0 || r.imageCalls != 0 {
			t.Fatal("rejected input reached runner")
		}
	}
	// Unknown IDs cannot reach the defensive image-policy branch because
	// validateModel rejects them first. Pin its full literal in source,
	// without weakening production validation just to exercise dead code.
	f, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		literal, ok := n.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			value, _ := strconv.Unquote(literal.Value)
			found = found || value == "the selected model does not support image input"
		}
		return true
	})
	if !found {
		t.Fatal("defensive image-policy literal changed")
	}
	r := &recordingImageRunner{}
	w := httptest.NewRecorder()
	if _, ok := NewUnauthenticated(r).validatePreparedModel(w, preparedRequest{Model: "unknown", Images: []encodedImage{{}}}); ok {
		t.Fatal("unknown model accepted")
	}
	if r.textCalls != 0 || r.imageCalls != 0 {
		t.Fatal("unknown input reached runner")
	}
}
