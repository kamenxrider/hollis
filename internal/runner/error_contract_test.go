// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// Read the declarations, not another handwritten list, so a newly added Kind
// cannot silently inherit the fallback when its public mapping is forgotten.
func TestEveryErrorKindHasContract(t *testing.T) {
	files, err := parser.ParseDir(token.NewFileSet(), ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[Kind]bool{}
	for _, entry := range errorContracts {
		if entry.Kind == "" {
			continue
		}
		if seen[entry.Kind] || entry.ExitCode == 0 || entry.MachineCode == "" ||
			entry.HTTPStatus == 0 || entry.HTTPType == "" || entry.HTTPCode == "" {
			t.Fatalf("duplicate or incomplete error contract: %+v", entry)
		}
		seen[entry.Kind] = true
	}
	count := 0
	for _, file := range files["runner"].Files {
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok {
				return true
			}
			typ, ok := spec.Type.(*ast.Ident)
			if !ok || typ.Name != "Kind" {
				return true
			}
			for _, value := range spec.Values {
				literal, ok := value.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				name, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				count++
				if !seen[Kind(name)] {
					t.Errorf("error kind %q is missing from the contract table", name)
				}
			}
			return true
		})
	}
	if count == 0 || count != len(seen) {
		t.Fatalf("found %d declared kinds and %d table kinds", count, len(seen))
	}
}
