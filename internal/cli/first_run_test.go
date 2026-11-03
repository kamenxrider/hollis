// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
	"github.com/kamenxrider/hollis/internal/store"
)

func runFirstRunCommand(t *testing.T, r runner.Runner, args ...string) (string, error) {
	t.Helper()
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := executeCommand(cmd, flags)
	return out.String(), err
}

func modelRows(t *testing.T, output, mode string) []map[string]any {
	t.Helper()
	if mode == "--agent" {
		var envelope struct {
			Meta    map[string]any  `json:"meta"`
			Results json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal([]byte(output), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Meta["schema_version"] != agentContextSchemaVersion {
			t.Fatalf("schema version changed: %v", envelope.Meta)
		}
		output = string(envelope.Results)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("invalid array: %v: %s", err, output)
	}
	return rows
}

func TestModelsDefaultMarker(t *testing.T) {
	for _, configured := range []string{"", "auto", "cloud", "cloud-pro", "on-device", "local"} {
		for _, mode := range []string{"text", "--json", "--agent"} {
			t.Run(configured+"/"+mode, func(t *testing.T) {
				stubConfigPath(t)
				stubResolution(t, nil, true, 27)
				if err := saveConfig(config{DefaultModel: configured}); err != nil {
					t.Fatal(err)
				}
				args := []string{"models"}
				if mode != "text" {
					args = append(args, mode)
				}
				r := &setupStatusRunner{}
				out, err := runFirstRunCommand(t, r, args...)
				if err != nil || r.probes != 1 {
					t.Fatalf("err=%v probes=%d", err, r.probes)
				}
				want := configured
				if want == "" {
					want = "auto"
				}
				if mode == "text" {
					if strings.Count(out, "(default)") != 1 || !strings.Contains(out, want+" (default)") {
						t.Fatalf("missing unique default marker: %s", out)
					}
				} else {
					rows := modelRows(t, out, mode)
					if len(rows) != len(runner.Specs())+2 {
						t.Fatalf("unavailable routes disappeared: %v", rows)
					}
					for _, row := range rows {
						if row["default"] != (row["model"] == want) {
							t.Fatalf("incorrect default: %v, want %s", row, want)
						}
					}
				}
			})
		}
	}
}

func TestModelsUnavailableReasons(t *testing.T) {
	for _, tc := range []struct {
		name, model, override, code, reason string
		major                               int
		listOK, available                   bool
		names                               []string
		local                               runner.LocalStatus
		localErr                            error
	}{
		{name: "old OS", model: "cloud-pro", major: 26, listOK: true, names: allImportedNames(), code: "platform_unsupported", reason: "requires macOS 27"},
		{name: "unknown OS", model: "cloud-pro", listOK: true, code: "platform_unknown", reason: "macOS version could not be checked"},
		{name: "missing Shortcut", model: "cloud", major: 27, listOK: true, code: "bridge_missing", reason: "Shortcut not imported"},
		{name: "missing helper", model: "local", major: 27, listOK: true, localErr: errors.New("native helper missing"), code: "native_status_unknown", reason: "native helper missing"},
		{name: "settings", model: "local", major: 27, listOK: true, local: runner.LocalStatus{Reason: "Apple Intelligence is off"}, code: "model_unavailable", reason: "Apple Intelligence"},
		{name: "stale name", model: "cloud", major: 27, listOK: true, override: "Renamed", code: "configured_reference_unverified", reason: "configured Shortcut reference is unverified"},
		{name: "configured UUID", model: "cloud", major: 27, listOK: true, override: runner.BridgeUUIDCloud, available: true, code: "configured_reference_unverified", reason: "configured Shortcut reference is unverified"},
		{name: "discovery failed", model: "cloud", major: 27, local: runner.LocalStatus{Available: true}, code: "discovery_unknown", reason: "bridge presence unknown"},
	} {
		for _, mode := range []string{"text", "--json", "--agent"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				stubConfigPath(t)
				stubResolution(t, tc.names, tc.listOK, tc.major)
				if tc.override != "" {
					if err := saveConfig(config{Bridges: map[string]string{tc.model: tc.override}}); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"models"}
				if mode != "text" {
					args = append(args, mode)
				}
				out, err := runFirstRunCommand(t, &setupStatusRunner{status: tc.local, err: tc.localErr}, args...)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "text" {
					if !strings.Contains(out, tc.model) || !strings.Contains(out, tc.reason) {
						t.Fatalf("missing unavailable explanation: %s", out)
					}
					return
				}
				for _, row := range modelRows(t, out, mode) {
					if row["model"] == tc.model {
						reason, _ := row["reason"].(string)
						if row["available"] != tc.available || row["reason_code"] != tc.code || !strings.Contains(reason, tc.reason) {
							t.Fatalf("unexpected diagnostic: %v", row)
						}
						return
					}
				}
				t.Fatalf("route %s omitted", tc.model)
			})
		}
	}
}

func TestModelsLocalDefaultAndProjection(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, false, 27)
	if err := saveConfig(config{DefaultModel: "local"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"--json", "--agent"} {
		out, err := runFirstRunCommand(t, &setupStatusRunner{}, "models", "--model", "local", mode,
			"--select", "model,default,available,readiness,reason_code,reason")
		if err != nil {
			t.Fatal(err)
		}
		rows := modelRows(t, out, mode)
		if len(rows) != 1 || rows[0]["model"] != "local" || rows[0]["default"] != true || len(rows[0]) != 6 {
			t.Fatalf("local projection: %s", out)
		}
	}
}

func TestEmptyChatHints(t *testing.T) {
	stubConfigPath(t)
	db := filepath.Join(t.TempDir(), "chats.db")
	old := openStore
	openStore = func() (*store.Store, error) { return store.Open(db) }
	t.Cleanup(func() { openStore = old })
	for _, tc := range []struct {
		command []string
		hint    string
		exit    int
	}{
		{[]string{"chats", "list"}, `No chats yet. Run: hollis chat "hello"` + "\n", 0},
		{[]string{"chats", "search", "missing"}, "No matching chats. Run: hollis chats list\n", 3},
	} {
		for _, mode := range []string{"text", "--json", "--agent"} {
			t.Run(strings.Join(tc.command, " ")+"/"+mode, func(t *testing.T) {
				args := append([]string{}, tc.command...)
				if mode != "text" {
					args = append(args, mode, "--select", "id")
				}
				out, err := runFirstRunCommand(t, &setupStatusRunner{}, args...)
				if ExitCode(err) != tc.exit {
					t.Fatalf("exit=%d, want %d", ExitCode(err), tc.exit)
				}
				if mode == "text" {
					if out != tc.hint {
						t.Fatalf("hint=%q, want %q", out, tc.hint)
					}
				} else {
					if rows := modelRows(t, out, mode); rows == nil || len(rows) != 0 {
						t.Fatalf("empty results changed: %s", out)
					}
					if strings.Contains(out, "Run:") || strings.Contains(out, `"error"`) {
						t.Fatalf("hint or error leaked into array output: %s", out)
					}
				}
			})
		}
	}
}

func TestDoctorNextCommandReadinessStates(t *testing.T) {
	for _, tc := range []struct {
		status, reason, route, want string
	}{
		{"ready", "prerequisites_ready", "local", `hollis respond --model local "hello"`},
		{"ready", "prerequisites_ready", "cloud", `hollis respond --model cloud "hello"`},
		{"ready", "prerequisites_ready", "image", `hollis image generate "A lighthouse" --output lighthouse.png`},
		{"unknown", "platform_unsupported", "local", "sw_vers -productVersion"},
		{"unknown", "platform_unknown", "cloud", "sw_vers -productVersion"},
		{"unknown", "native_status_unknown", "local", "hollis setup --model local"},
		{"model_not_ready", "model_unavailable", "local", `open -a "System Settings"`},
		{"unknown", "config_unknown", "cloud", "hollis config show"},
		{"unknown", "transport_missing", "cloud", "shortcuts list"},
		{"unknown", "discovery_unknown", "cloud", "shortcuts list"},
		{"unknown", "configured_reference_unverified", "cloud", "shortcuts list"},
		{"import_required", "bridge_missing", "cloud", "hollis setup --model cloud"},
		{"approval_required", "import_pending", "cloud", "hollis setup --model cloud"},
		{"approval_required", "image_configuration_required", "image", "hollis setup --model image"},
	} {
		t.Run(tc.route+"/"+tc.reason, func(t *testing.T) {
			p := setup.Plan{Route: tc.route, Status: tc.status, ReasonCode: tc.reason}
			if got := doctorNextCommand(p); got != tc.want {
				t.Fatalf("next=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestDoctorNextCommandOutput(t *testing.T) {
	for _, tc := range []struct {
		name, route, next string
		local             runner.LocalStatus
		localErr          error
		names             []string
		exit              int
	}{
		{"local ready overview", "", `hollis respond --model local "hello"`, runner.LocalStatus{Available: true}, nil, nil, 3},
		{"bridge ready overview", "", `hollis respond --model cloud "hello"`, runner.LocalStatus{}, nil, allImportedNames(), 0},
		{"helper missing overview", "", "hollis setup --model local", runner.LocalStatus{}, errors.New("helper missing"), nil, 3},
		{"settings overview", "", `open -a "System Settings"`, runner.LocalStatus{}, nil, nil, 3},
		{"selected ready", "local", `hollis respond --model local "hello"`, runner.LocalStatus{Available: true}, nil, nil, 0},
		{"selected settings", "local", `open -a "System Settings"`, runner.LocalStatus{}, nil, nil, 10},
		{"selected missing", "cloud", "hollis setup --model cloud", runner.LocalStatus{}, nil, nil, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, tc.names, true, 27)
			args := []string{"doctor"}
			if tc.route != "" {
				args = append(args, "--model", tc.route)
			}
			out, err := runFirstRunCommand(t, &setupStatusRunner{status: tc.local, err: tc.localErr}, args...)
			if ExitCode(err) != tc.exit || !strings.HasSuffix(out, "Next: "+tc.next+"\n") || strings.Count(out, "Next:") != 1 {
				t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, out)
			}
			if err != nil && !ErrorReported(err) {
				t.Fatal("main would print another error after the next command")
			}
			out, err = runFirstRunCommand(t, &setupStatusRunner{status: tc.local, err: tc.localErr}, append(args, "--json")...)
			if ExitCode(err) != tc.exit || strings.Contains(out, "Next:") || !json.Valid([]byte(out)) {
				t.Fatalf("JSON diagnostic changed: err=%v output=%s", err, out)
			}
		})
	}
}

func TestModelsBaselinesRemainUnchanged(t *testing.T) {
	// These are the original JSON fixtures, not regenerated expected output.
	for _, name := range []string{"full_models_json.txt", "partial_models_json.txt", "full_models_agent.txt", "partial_models_agent.txt"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "goldens", name))
		if err != nil || bytes.Contains(raw, []byte(`"default"`)) {
			t.Fatalf("baseline was regenerated: %s: %v", name, err)
		}
	}
}
