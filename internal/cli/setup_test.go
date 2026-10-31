// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type setupStatusRunner struct {
	status runner.LocalStatus
	err    error
	probes int
}

func (r *setupStatusRunner) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	panic("setup must never request inference")
}
func (r *setupStatusRunner) NativeStatus(context.Context) (runner.LocalStatus, error) {
	r.probes++
	return r.status, r.err
}

func runSetupFixture(t *testing.T, r runner.Runner, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	var got map[string]any
	if decodeErr := json.Unmarshal(out.Bytes(), &got); decodeErr != nil {
		t.Fatalf("invalid JSON: %v output=%q commandErr=%v", decodeErr, out.String(), err)
	}
	return got, err
}

func TestSelectedDoctorLocalNeverTouchesShortcutsOrConfig(t *testing.T) {
	stubResolution(t, nil, false, 27)
	oldConfig, oldList, oldLook := configPath, listInstalledShortcuts, lookPath
	configPath = func() (string, error) { t.Fatal("local read bridge config"); return "", nil }
	listInstalledShortcuts = func(context.Context) ([]string, error) { t.Fatal("local listed Shortcuts"); return nil, nil }
	lookPath = func(string) (string, error) { t.Fatal("local probed Shortcuts"); return "", nil }
	t.Cleanup(func() { configPath, listInstalledShortcuts, lookPath = oldConfig, oldList, oldLook })
	r := &setupStatusRunner{status: runner.LocalStatus{Available: true, Version: runner.NativeVersion, Protocol: runner.NativeProtocol}}
	got, err := runSetupFixture(t, r, "doctor", "--model", "local", "--json")
	if err != nil || got["status"] != "ready" || got["artifact_verification"] != "unknown" || got["inference_tested"] != false || r.probes != 1 {
		t.Fatalf("got=%v err=%v probes=%d", got, err, r.probes)
	}
}

func TestSelectedDoctorCloudIgnoresUnrelatedRoutes(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, []string{"AFM Bridge - Cloud Pro"}, true, 27)
	r := &setupStatusRunner{err: errors.New("unrelated helper missing")}
	got, err := runSetupFixture(t, r, "doctor", "--model", "cloud-pro", "--json")
	if err != nil || got["status"] != "ready" || r.probes != 0 {
		t.Fatalf("got=%v err=%v native probes=%d", got, err, r.probes)
	}
	bridge := got["bridge"].(map[string]any)
	if bridge["reference"] != "AFM Bridge - Cloud Pro" || bridge["discovered"] != true {
		t.Fatalf("bridge=%v", bridge)
	}
}

func TestSelectedDoctorFailuresAndCustomMappings(t *testing.T) {
	for _, tc := range []struct {
		name, route, reason, override string
		names                         []string
		major, exit                   int
		listOK                        bool
	}{
		{"missing", "cloud", "bridge_missing", "", nil, 27, 3, true},
		{"custom found", "cloud", "prerequisites_ready", "Renamed", []string{"Renamed"}, 27, 0, true},
		{"custom missing", "cloud", "configured_reference_unverified", "Renamed", nil, 27, 10, true},
		{"uuid unverified", "cloud", "configured_reference_unverified", runner.BridgeUUIDCloud, nil, 27, 10, true},
		{"discovery unknown", "cloud", "discovery_unknown", "", nil, 27, 5, false},
		{"discovery unknown custom", "cloud", "discovery_unknown", "Renamed", nil, 27, 5, false},
		{"discovery unknown uuid", "cloud", "discovery_unknown", runner.BridgeUUIDCloud, nil, 27, 5, false},
		{"unsupported pro", "cloud-pro", "platform_unsupported", "", nil, 26, 10, true},
		{"unknown OS", "cloud", "platform_unknown", "", nil, 0, 10, true},
		{"image not connected", "image", "image_configuration_required", "", []string{imageSetupBridge}, 27, 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, tc.names, tc.listOK, tc.major)
			if tc.override != "" {
				if err := saveConfig(config{Bridges: map[string]string{tc.route: tc.override}}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := runSetupFixture(t, &setupStatusRunner{}, "doctor", "--model", tc.route, "--json")
			if ExitCode(err) != tc.exit || got["reason_code"] != tc.reason {
				t.Fatalf("got=%v err=%v exit=%d", got, err, ExitCode(err))
			}
			if tc.exit != 0 && (got["error"] == nil || !ErrorReported(err)) {
				t.Fatal("nonzero diagnostic lost structured error")
			}
			if tc.override != "" {
				bridge := got["bridge"].(map[string]any)
				if bridge["reference"] != tc.override || bridge["configured"] != true {
					t.Fatalf("lost configured reference: %v", bridge)
				}
			}
		})
	}
}

func TestSelectedDoctorErrorProjectionKeepsError(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, true, 27)
	got, err := runSetupFixture(t, &setupStatusRunner{}, "doctor", "--model", "cloud", "--agent", "--select", "route")
	results := got["results"].(map[string]any)
	if ExitCode(err) != 3 || got["error"] == nil || got["meta"] == nil || len(results) != 1 || results["route"] != "cloud" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestSetupConfigObservationIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{"bridges":{"cloud":"Renamed"},"image_bridges":{"sketch":"Sketch"}}`)
	if err := os.WriteFile(path, raw, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0750); err != nil {
		t.Fatal(err)
	}
	old := configPath
	configPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { configPath = old })
	beforeFile, _ := os.Stat(path)
	beforeDir, _ := os.Stat(dir)
	for range 2 {
		c, err := readSetupConfig()
		if err != nil || c.Bridges["cloud"] != "Renamed" || c.ImageBridges["sketch"] != "Sketch" {
			t.Fatalf("config=%v err=%v", c, err)
		}
	}
	afterFile, _ := os.Stat(path)
	afterDir, _ := os.Stat(dir)
	afterRaw, _ := os.ReadFile(path)
	entries, _ := os.ReadDir(dir)
	if beforeFile.Mode() != afterFile.Mode() || beforeDir.Mode() != afterDir.Mode() ||
		!beforeFile.ModTime().Equal(afterFile.ModTime()) || !reflect.DeepEqual(raw, afterRaw) || len(entries) != 1 {
		t.Fatal("read-only observation wrote state or changed permissions")
	}
	configPath = func() (string, error) { return filepath.Join(dir, "absent", "config.json"), nil }
	if _, err := readSetupConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "absent")); !os.IsNotExist(err) {
		t.Fatal("observation created missing state directory")
	}
}

func TestSetupConfigRejectsSymlinkAndInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	old := configPath
	configPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { configPath = old })
	if _, err := readSetupConfig(); err == nil {
		t.Fatal("accepted symlinked config")
	}
	configPath = func() (string, error) { return target, nil }
	if err := os.WriteFile(target, []byte(`{} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSetupConfig(); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}

func TestSetupWithoutAuthenticatedRecordDoesNotProbe(t *testing.T) {
	r := &setupStatusRunner{}
	got, err := runSetupFixture(t, r, "setup", "--model", "local", "--agent")
	result := got["results"].(map[string]any)
	if ExitCode(err) != 10 || r.probes != 0 || result["reason_code"] != "runtime_unverified" || result["source"] != "bootstrap" {
		t.Fatalf("got=%v err=%v probes=%d", got, err, r.probes)
	}
}

func TestSetupRejectsAutoAndEmptyRoute(t *testing.T) {
	for _, command := range []string{"setup", "doctor"} {
		for _, route := range []string{"auto", "", "future"} {
			cmd := NewRootCmd(func() runner.Runner { return &setupStatusRunner{} })
			cmd.SetArgs([]string{command, "--model", route})
			cmd.SetOut(&bytes.Buffer{})
			if err := cmd.Execute(); ExitCode(err) != 2 {
				t.Fatalf("%s route=%q err=%v", command, route, err)
			}
		}
	}
}
