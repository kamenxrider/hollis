// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

// Route output goldens lock the output shape that the RouteSpec
// table must preserve (roadmap §13.2, issues #18/#20/#30): `hollis models`
// human/JSON/agent rows, doctor bridge rows, and `config show` bridge
// ordering. The original fixtures were generated from unmodified main (5ccde23)
// before any route-table code existed; regenerating a fixture after a
// refactor proves nothing, so treat regeneration as a deliberate contract
// change that must be reviewed, never a way to fix drift. Models JSON fixtures
// remain unchanged baselines: new output must strictly contain their rows and
// fields. Human fixtures include the default marker and next-command hints.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

var updateRouteGoldens = flag.Bool("update-route-goldens", false, "rewrite route output golden fixtures from current behavior")

// goldenEnv fixes every environment input route output depends on: the
// installed bridge list, the macOS version, the transport binary lookup,
// the version string, and the config location. No transport is invoked.
type goldenEnv struct {
	installed []string // bridge names seen in `shortcuts list`
	listOK    bool
	major     int    // macOS major version; 27 = MeasuredOSMajor
	build     string // macOS build string for doctor
}

var fullEnv = goldenEnv{
	installed: []string{
		"AFM Bridge - Cloud.signed",
		"AFM Bridge - Cloud Pro.signed",
		"AFM Bridge - On-Device.signed",
		"AFM Bridge - ChatGPT.signed",
	},
	listOK: true,
	major:  27,
	build:  "27F99",
}

// partialEnv omits cloud-pro: models must explain the tier, doctor must report
// a MISSING row with the remedy line, and /v1/models (server side) must
// drop it from the literal list.
var partialEnv = goldenEnv{
	installed: []string{
		"AFM Bridge - Cloud.signed",
		"AFM Bridge - On-Device.signed",
		"AFM Bridge - ChatGPT.signed",
	},
	listOK: true,
	major:  27,
	build:  "27F99",
}

func stubGoldenEnv(t *testing.T, env goldenEnv) (configDir string) {
	t.Helper()
	stubConfigPath(t)
	stubResolution(t, env.installed, env.listOK, env.major)
	oldBuild := macosBuild
	macosBuild = func() string { return env.build }
	t.Cleanup(func() { macosBuild = oldBuild })
	oldVersion := version
	version = "dev"
	t.Cleanup(func() { version = oldVersion })
	dir, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(dir)
}

// runGoldenCommand executes args against the full root command with a fake
// runner and returns everything printed on stdout.
func runGoldenCommand(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{}, args...))
	err := executeCommand(cmd, flags)
	return out.Bytes(), err
}

// compareGolden compares got against the fixture at testdata/goldens/name,
// rewriting it only under -update-route-goldens. token replacements apply a
// single documented normalization: the machine-specific config directory
// becomes $CONFIG_DIR so fixtures stay byte-stable across machines.
func compareGolden(t *testing.T, name string, got []byte, tokens ...[2]string) {
	t.Helper()
	for _, token := range tokens {
		got = bytes.ReplaceAll(got, []byte(token[0]), []byte(token[1]))
	}
	path := filepath.Join("testdata", "goldens", name)
	if *updateRouteGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden fixture missing for %s (regenerate deliberately with -update-route-goldens): %v", name, err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("route output drift in %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func TestRouteOutputGoldens(t *testing.T) {
	for _, env := range []goldenEnv{fullEnv, partialEnv} {
		env := env
		t.Run(envName(env), func(t *testing.T) {
			configDir := stubGoldenEnv(t, env)
			tokens := [][2]string{{configDir, "$CONFIG_DIR"}}

			// models: human, JSON, and agent must agree on rows and order.
			for _, tc := range []struct {
				name string
				args []string
			}{
				{"models_human", []string{"models"}},
				{"models_json", []string{"models", "--json"}},
				{"models_agent", []string{"models", "--agent"}},
			} {
				out, err := runGoldenCommand(t, tc.args...)
				if err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				name := envName(env) + "_" + tc.name + ".txt"
				if tc.name == "models_human" {
					compareGolden(t, name, out, tokens...)
				} else {
					compareModelsJSONBaseline(t, name, out)
				}
			}

			// doctor: full output including bridge rows. The exit code is
			// part of the contract: 0 when every bridge resolves, 3 when a
			// bridge is missing.
			for _, tc := range []struct {
				name string
				args []string
			}{
				{"doctor_human", []string{"doctor"}},
				{"doctor_json", []string{"doctor", "--json"}},
			} {
				out, err := runGoldenCommand(t, tc.args...)
				wantCode := 0
				if !env.listOK || len(env.installed) < 4 {
					wantCode = 3
				}
				if got := ExitCode(err); got != wantCode {
					t.Fatalf("%s exit = %d, want %d (err=%v)", tc.name, got, wantCode, err)
				}
				compareGolden(t, envName(env)+"_"+tc.name+".txt", out, tokens...)
			}
		})
	}
}

func compareModelsJSONBaseline(t *testing.T, name string, got []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "goldens", name))
	if err != nil {
		t.Fatal(err)
	}
	var oldValue, newValue any
	if err := json.Unmarshal(raw, &oldValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &newValue); err != nil {
		t.Fatal(err)
	}
	if !assertJSONSubset(t, oldValue, newValue) {
		t.Fatal("models JSON must add fields without removing or changing existing data")
	}
}

// Returns whether at least one field or row was added.
func assertJSONSubset(t *testing.T, oldValue, newValue any) bool {
	t.Helper()
	added := false
	switch old := oldValue.(type) {
	case map[string]any:
		current, ok := newValue.(map[string]any)
		if !ok {
			t.Fatalf("object changed type: %T", newValue)
		}
		added = len(current) > len(old)
		for key, value := range old {
			next, exists := current[key]
			if !exists {
				t.Fatalf("existing field %q was removed", key)
			}
			if assertJSONSubset(t, value, next) {
				added = true
			}
		}
	case []any:
		current, ok := newValue.([]any)
		if !ok || len(current) < len(old) {
			t.Fatalf("array lost rows: %v", newValue)
		}
		added = len(current) > len(old)
		for _, row := range old {
			model := row.(map[string]any)["model"]
			found := false
			for _, candidate := range current {
				if candidate.(map[string]any)["model"] == model {
					found = true
					if assertJSONSubset(t, row, candidate) {
						added = true
					}
					break
				}
			}
			if !found {
				t.Fatalf("existing model %v was removed", model)
			}
		}
	default:
		if oldValue != newValue {
			t.Fatalf("existing value changed: want %v, got %v", oldValue, newValue)
		}
	}
	return added
}

func TestRouteConfigShowGolden(t *testing.T) {
	configDir := stubGoldenEnv(t, fullEnv)
	tokens := [][2]string{{configDir, "$CONFIG_DIR"}}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	// Overrides for every concrete tier in a scrambled key order: config
	// show must render them in the canonical tier order, not map order.
	const configJSON = `{"default_model":"cloud",
		"bridges":{"chatgpt":"My ChatGPT","on-device":"My On-Device","cloud":"My Cloud","cloud-pro":"My Pro"},
		"image_bridge":"Hollis Image Unified"}`
	if err := os.WriteFile(path, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"config_show_human", []string{"config", "show"}},
		{"config_show_json", []string{"config", "show", "--json"}},
	} {
		out, err := runGoldenCommand(t, tc.args...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		compareGolden(t, tc.name+".txt", out, tokens...)
	}
}

func TestRouteGoldensRejectEnvironmentDependence(t *testing.T) {
	// Guard the harness itself: with discovery failing, bridge rows must
	// still render (fail-closed, unverified), never disappear.
	env := goldenEnv{installed: nil, listOK: false, major: 27, build: "27F99"}
	configDir := stubGoldenEnv(t, env)
	tokens := [][2]string{{configDir, "$CONFIG_DIR"}}
	out, err := runGoldenCommand(t, "models", "--json")
	var usage *cliError
	if err == nil || !errors.As(err, &usage) || ExitCode(err) != 5 {
		t.Fatalf("list failure must surface a transport-classified error, got %v", err)
	}
	if !strings.Contains(string(out), `"error"`) {
		t.Fatalf("list failure lost error payload: %s", out)
	}
	compareGolden(t, "list_failure_models_json.txt", out, tokens...)
}

func envName(env goldenEnv) string {
	if len(env.installed) == 4 {
		return "full"
	}
	if env.listOK {
		return "partial"
	}
	return "list_failure"
}
