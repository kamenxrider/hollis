// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

// Two offline guards for the canonical route table.
//
//  1. No production package may dispatch on a model value. Every route fact
//     belongs in the table, and consumers read it through Spec/Specs. This is
//     the regression guard issue #30 asks for: delete the table's switch, and
//     the switch must not come back.
//  2. The surfaces that render routes — `hollis models` in JSON and agent
//     mode, doctor's bridge rows and GET /v1/models — must agree with the
//     table and with each other, checked against the committed golden
//     fixtures rather than against freshly generated output.
//
// The guard is syntactic on purpose: go/ast sees a switch over a route
// constant wherever it appears, with no type information to load.

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// routeConstants are the identifiers a dispatch on a model would name.
var routeConstants = map[string]bool{
	"ModelAuto":     true,
	"ModelCloud":    true,
	"ModelCloudPro": true,
	"ModelOnDevice": true,
	"ModelChatGPT":  true,
	"ModelLocal":    true,
}

// modelSwitchViolations reports the position of every switch whose case
// expressions name a route constant. Tagless switches count: `switch { case
// m == ModelAuto: }` is the same dispatch wearing a disguise.
func modelSwitchViolations(fset *token.FileSet, files []*ast.File) []string {
	var found []string
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			sw, ok := node.(*ast.SwitchStmt)
			if ok && switchDispatchesOnModel(sw) {
				found = append(found, fset.Position(sw.Pos()).String())
			}
			return true
		})
	}
	return found
}

func switchDispatchesOnModel(sw *ast.SwitchStmt) bool {
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, expr := range clause.List {
			if namesRouteConstant(expr) {
				return true
			}
		}
	}
	return false
}

func namesRouteConstant(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			found = found || routeConstants[value.Name]
		case *ast.SelectorExpr:
			found = found || routeConstants[value.Sel.Name]
		}
		return !found
	})
	return found
}

// TestNoModelSwitchInProduction fails if any non-test package dispatches on a
// model value. Adding a route is a table row, never a new case arm.
func TestNoModelSwitchInProduction(t *testing.T) {
	files, fset, scanned := parseProductionGo(t)
	if violations := modelSwitchViolations(fset, files); len(violations) != 0 {
		t.Fatalf("model dispatch reintroduced outside the route table:\n  %s\n\nRead the fact from RouteSpec instead of switching on the model.",
			strings.Join(violations, "\n  "))
	}
	if scanned == 0 {
		t.Fatal("no production files scanned; the guard is not looking at the tree")
	}
	t.Logf("scanned %d production files", scanned)
}

// TestModelSwitchGuardDetectsReintroducedSwitch proves the guard fires on the
// two historical shapes and stays quiet on an unrelated switch. A guard that
// cannot fail is not a guard.
func TestModelSwitchGuardDetectsReintroducedSwitch(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "tagged switch over a model",
			src: `package sample

import "github.com/kamenxrider/hollis/internal/runner"

func spacing(model runner.Model) int {
	switch model {
	case runner.ModelCloudPro:
		return 45
	case runner.ModelCloud:
		return 15
	default:
		return 0
	}
}`,
			want: 1,
		},
		{
			name: "tagless switch comparing a model",
			src: `package sample

import "github.com/kamenxrider/hollis/internal/runner"

func shown(model runner.Model) bool {
	switch {
	case model == runner.ModelAuto:
		return false
	default:
		return true
	}
}`,
			want: 1,
		},
		{
			name: "switch on an unrelated value",
			src: `package sample

func label(kind string) int {
	switch kind {
	case "image":
		return 1
	default:
		return 0
	}
}`,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "sample.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse sample: %v", err)
			}
			got := modelSwitchViolations(fset, []*ast.File{file})
			if len(got) != tc.want {
				t.Fatalf("violations = %v, want %d", got, tc.want)
			}
		})
	}
}

// parseProductionGo parses every non-test Go file under cmd/ and internal/.
func parseProductionGo(t *testing.T) ([]*ast.File, *token.FileSet, int) {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	scanned := 0
	for _, top := range []string{"cmd", "internal"} {
		root := filepath.Join("..", "..", top)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				return err
			}
			files = append(files, file)
			scanned++
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	return files, fset, scanned
}

// The fixtures below are rendered by the CLI and the server, not by the
// table, so comparing the table against them catches a consumer that drifts
// away from the canonical order, selection, names, owner or image policy.

type modelsRow struct {
	Model      string `json:"model"`
	WFLLMModel string `json:"wfllm_model"`
	AppleModel string `json:"apple_model"`
}

type doctorGolden struct {
	Bridges []struct {
		Model string `json:"model"`
		UUID  string `json:"uuid"`
		Name  string `json:"name"`
	} `json:"bridges"`
}

type serverGolden struct {
	Data []struct {
		ID           string         `json:"id"`
		OwnedBy      string         `json:"owned_by"`
		Capabilities map[string]any `json:"capabilities"`
	} `json:"data"`
}

// TestRouteTableMatchesGoldenSurfaces pins the CLI and HTTP listings to the
// table: models JSON rows, models agent rows, doctor bridges and /v1/models
// must name the same tiers in the same order with the same values.
func TestRouteTableMatchesGoldenSurfaces(t *testing.T) {
	specs := Specs()
	tiers := Models()
	if len(tiers) != len(specs) {
		t.Fatalf("Models() has %d tiers, table has %d", len(tiers), len(specs))
	}

	jsonRows := readJSON[[]modelsRow](t, filepath.Join("..", "cli", "testdata", "goldens", "full_models_json.txt"))
	var agent struct {
		Results []modelsRow `json:"results"`
	}
	readJSONInto(t, filepath.Join("..", "cli", "testdata", "goldens", "full_models_agent.txt"), &agent)

	// auto, the table's tiers in order, then the native route.
	var wantModels []string
	wantModels = append(wantModels, "auto")
	for _, tier := range tiers {
		wantModels = append(wantModels, string(tier))
	}
	wantModels = append(wantModels, "local")

	if got := rowModels(jsonRows); !reflect.DeepEqual(got, wantModels) {
		t.Fatalf("models --json order = %v, want %v", got, wantModels)
	}
	if got := rowModels(agent.Results); !reflect.DeepEqual(got, wantModels) {
		t.Fatalf("models --agent order = %v, want %v", got, wantModels)
	}
	if !reflect.DeepEqual(jsonRows, agent.Results) {
		t.Fatalf("models --json rows differ from --agent results:\n json=%v\nagent=%v", jsonRows, agent.Results)
	}
	for i, spec := range specs {
		row := jsonRows[i+1] // row 0 is auto
		if row.WFLLMModel != spec.Selection {
			t.Errorf("%s wfllm_model = %q, table says %q", spec.ID, row.WFLLMModel, spec.Selection)
		}
		// The golden machine is macOS 27, so the measured name applies.
		if row.AppleModel != spec.Names.Measured {
			t.Errorf("%s apple_model = %q, table says %q", spec.ID, row.AppleModel, spec.Names.Measured)
		}
	}

	doctor := readJSON[doctorGolden](t, filepath.Join("..", "cli", "testdata", "goldens", "full_doctor_json.txt"))
	if got := bridgeModels(doctor); !reflect.DeepEqual(got, wantModels[1:len(wantModels)-1]) {
		t.Fatalf("doctor bridge order = %v, want %v", got, tiers)
	}
	for i, spec := range specs {
		bridge := doctor.Bridges[i]
		if bridge.UUID != spec.CompiledUUID {
			t.Errorf("%s doctor uuid = %q, table says %q", spec.ID, bridge.UUID, spec.CompiledUUID)
		}
		if bridge.Name != spec.BridgeNames[0] {
			t.Errorf("%s doctor name = %q, table says %q", spec.ID, bridge.Name, spec.BridgeNames[0])
		}
	}

	all := readJSON[serverGolden](t, filepath.Join("..", "server", "testdata", "goldens", "server_models_all.json"))
	var wantServer []string
	wantServer = append(wantServer, "hollis-image", "auto")
	for _, tier := range tiers {
		wantServer = append(wantServer, string(tier))
	}
	wantServer = append(wantServer, "local")
	if got := serverIDs(all); !reflect.DeepEqual(got, wantServer) {
		t.Fatalf("/v1/models order = %v, want %v", got, wantServer)
	}
	for i, spec := range specs {
		row := all.Data[i+2] // hollis-image and auto come first
		if row.OwnedBy != spec.OwnedBy {
			t.Errorf("%s owned_by = %q, table says %q", spec.ID, row.OwnedBy, spec.OwnedBy)
		}
		if got, want := row.Capabilities["backend"], string(spec.Transport); got != want {
			t.Errorf("%s backend = %v, want %q", spec.ID, got, want)
		}
		if got, want := row.Capabilities["image_input"], spec.Capabilities.Images; got != want {
			t.Errorf("%s image_input = %v, table says %v", spec.ID, got, want)
		}
	}

	// The gated listing is a fail-closed subset in the same order.
	gated := readJSON[serverGolden](t, filepath.Join("..", "server", "testdata", "goldens", "server_models_gated.json"))
	gatedIDs := serverIDs(gated)
	if !isSubsequence(gatedIDs, wantServer) {
		t.Fatalf("gated /v1/models order %v is not a subsequence of %v", gatedIDs, wantServer)
	}
	if len(gatedIDs) != 3 {
		t.Fatalf("gated /v1/models lists %d models, want 3: %v", len(gatedIDs), gatedIDs)
	}
}

func rowModels(rows []modelsRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Model)
	}
	return out
}

func bridgeModels(g doctorGolden) []string {
	out := make([]string, 0, len(g.Bridges))
	for _, bridge := range g.Bridges {
		out = append(out, bridge.Model)
	}
	return out
}

func serverIDs(g serverGolden) []string {
	out := make([]string, 0, len(g.Data))
	for _, row := range g.Data {
		out = append(out, row.ID)
	}
	return out
}

func isSubsequence(subset, full []string) bool {
	next := 0
	for _, want := range full {
		if next < len(subset) && subset[next] == want {
			next++
		}
	}
	return next == len(subset)
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	var value T
	readJSONInto(t, path, &value)
	return value
}

func readJSONInto(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("decode golden %s: %v", path, err)
	}
}
