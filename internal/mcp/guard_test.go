// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

// Source guards: surfaces that promise never to run MCP servers cannot
// reach this package, the client is built in one place, and no shell is
// ever involved in starting a server.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/kamenxrider/hollis"

type sourceFile struct {
	pkg  string
	path string
	file *ast.File
}

// productionSources parses every non-test Go file under internal/ and cmd/.
func productionSources(t *testing.T) ([]sourceFile, *token.FileSet) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []sourceFile
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "testdata" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			files = append(files, sourceFile{pkg: modulePath + "/" + filepath.ToSlash(rel), path: path, file: f})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files, fset
}

func TestNoMCPImportFromServerOrBatch(t *testing.T) {
	files, _ := productionSources(t)
	imports := map[string]map[string]bool{}
	for _, f := range files {
		if imports[f.pkg] == nil {
			imports[f.pkg] = map[string]bool{}
		}
		for _, spec := range f.file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if strings.HasPrefix(path, modulePath+"/") {
				imports[f.pkg][path] = true
			}
		}
	}
	target := modulePath + "/internal/mcp"
	for _, root := range []string{"internal/server", "internal/batch"} {
		start := modulePath + "/" + root
		if imports[start] == nil {
			t.Fatalf("package %s not found; update the guard", start)
		}
		seen := map[string]string{start: ""}
		queue := []string{start}
		for len(queue) > 0 {
			pkg := queue[0]
			queue = queue[1:]
			for dep := range imports[pkg] {
				if _, ok := seen[dep]; ok {
					continue
				}
				seen[dep] = pkg
				if dep == target {
					chain := []string{dep}
					for p := pkg; p != ""; p = seen[p] {
						chain = append(chain, p)
					}
					t.Errorf("%s reaches internal/mcp: %s", root, strings.Join(chain, " <- "))
				}
				queue = append(queue, dep)
			}
		}
	}
}

// callSites lists positions of calls to pkgAlias.name, matching by the
// imported path rather than the local alias.
func callSites(files []sourceFile, fset *token.FileSet, importPath, name string) []string {
	var sites []string
	for _, f := range files {
		alias := ""
		for _, spec := range f.file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if path != importPath {
				continue
			}
			alias = filepath.Base(path)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
		}
		inPkg := f.pkg == importPath
		if alias == "" && !inPkg {
			continue
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := fn.X.(*ast.Ident); ok && id.Name == alias && fn.Sel.Name == name {
					sites = append(sites, fset.Position(call.Pos()).String())
				}
			case *ast.Ident:
				if inPkg && fn.Name == name {
					sites = append(sites, fset.Position(call.Pos()).String())
				}
			}
			return true
		})
	}
	sort.Strings(sites)
	return sites
}

func TestSingleConstructionSite(t *testing.T) {
	files, fset := productionSources(t)
	if sites := callSites(files, fset, "github.com/modelcontextprotocol/go-sdk/mcp", "NewClient"); len(sites) != 1 {
		t.Errorf("sdk NewClient called at %v; want exactly one site", sites)
	}
	var outside []string
	for _, site := range callSites(files, fset, modulePath+"/internal/mcp", "Attach") {
		if !strings.Contains(site, filepath.Join("internal", "mcp")+string(filepath.Separator)) {
			outside = append(outside, site)
		}
	}
	if len(outside) > 1 {
		t.Errorf("mcp.Attach called at %v; want at most one site", outside)
	}
}

func TestNoShellIsUsed(t *testing.T) {
	files, fset := productionSources(t)
	shells := map[string]bool{"sh": true, "bash": true, "zsh": true, "/bin/sh": true, "/bin/bash": true, "/bin/zsh": true, "/usr/bin/env": true, "-c": true, "-lc": true}
	for _, f := range files {
		if f.pkg != modulePath+"/internal/mcp" && !(f.pkg == modulePath+"/internal/subproc" && filepath.Base(f.path) == "session.go") {
			continue
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && shells[s] {
					t.Errorf("%s: shell-like literal %q", fset.Position(lit.Pos()), s)
				}
			}
			return true
		})
	}
	sites := callSites(files, fset, "os/exec", "Command")
	sites = append(sites, callSites(files, fset, "os/exec", "CommandContext")...)
	var inScope []string
	for _, site := range sites {
		if strings.Contains(site, filepath.Join("internal", "mcp")) || strings.Contains(site, filepath.Join("internal", "subproc", "session.go")) {
			inScope = append(inScope, site)
		}
	}
	if len(inScope) != 1 || !strings.Contains(inScope[0], "session.go") {
		t.Errorf("exec.Command sites for MCP servers = %v; want only the session start", inScope)
	}
	src, err := os.ReadFile(filepath.Join("..", "subproc", "session.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "exec.Command(spec.Name, spec.Args...)") {
		t.Error("the session must build exec.Command(name, args...) directly")
	}
}
