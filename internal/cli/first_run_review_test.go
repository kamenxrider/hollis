// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
)

const wantInstallNext = "Repair hollis and its sibling hollis-native through the same installation source: the same formula, source revision, or matched release bundle. Do not mix versions or switch installers."

func modelRow(t *testing.T, out, model string) map[string]any {
	t.Helper()
	for _, row := range modelRows(t, out, "--json") {
		if row["model"] == model {
			return row
		}
	}
	t.Fatalf("route %s omitted: %s", model, out)
	return nil
}

func TestDoctorNextNeverSuggestsStandaloneSetup(t *testing.T) {
	for _, tc := range []struct {
		name, route, next string
		localErr          error
	}{
		{"selected missing bridge", "cloud", "hollis config set bridge cloud <name-or-uuid>", nil},
		{"selected missing image", "image", "hollis config set image-bridge <name-or-uuid>", nil},
		{"helper missing overview", "", wantInstallNext, errors.New("helper missing")},
		{"selected helper missing", "local", wantInstallNext, errors.New("helper missing")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, nil, true, 27)
			args := []string{"doctor"}
			if tc.route != "" {
				args = append(args, "--model", tc.route)
			}
			out, _ := runFirstRunCommand(t, &setupStatusRunner{err: tc.localErr}, args...)
			if strings.Contains(out, "hollis setup") || !strings.HasSuffix(out, "Next: "+tc.next+"\n") {
				t.Fatalf("next step: %s", out)
			}
		})
	}
}

func TestDoctorNextForPendingAndImageConfiguration(t *testing.T) {
	for reason, want := range map[string]string{
		"import_pending":               "open -a Shortcuts",
		"image_configuration_required": "hollis config set image-bridge 'Hollis Image - Reference Input v2'",
	} {
		p := setup.Plan{Route: "image", Status: "approval_required", ReasonCode: reason,
			Bridge: setup.BridgeFacts{Reference: "Hollis Image - Reference Input v2"}}
		got := doctorNextCommand(p)
		if got != want {
			t.Fatalf("%s: next=%q, want %q", reason, got, want)
		}
	}
}

func TestModelsAutoAgreesWithFailedDiscovery(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, false, 27)
	for _, mode := range []string{"text", "--json"} {
		args := []string{"models"}
		if mode != "text" {
			args = append(args, mode)
		}
		out, err := runFirstRunCommand(t, &setupStatusRunner{status: runner.LocalStatus{Available: true}}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "neither cloud nor on-device bridge is available") {
			t.Fatalf("auto claims bridges are absent after failed discovery: %s", out)
		}
		if mode == "text" {
			if !strings.Contains(out, "cloud and on-device bridge presence unknown") {
				t.Fatalf("auto reason: %s", out)
			}
			continue
		}
		auto, cloud := modelRow(t, out, "auto"), modelRow(t, out, "cloud")
		if auto["reason_code"] != "discovery_unknown" || cloud["reason_code"] != "discovery_unknown" {
			t.Fatalf("auto=%v cloud=%v", auto, cloud)
		}
	}
}

func TestModelsLocalLeavesConfigAndPlatformUntouched(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		wantDefault any
	}{
		{"valid 0644 config", `{"default_model":"local"}`, true},
		{"invalid config", `{"unknown_key":`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, nil, true, 27)
			macosMajorVersion = func() int { t.Error("models --model local ran sw_vers"); return 27 }
			macosVersion = func() string { t.Error("models --model local ran sw_vers"); return "27.0" }
			path, _ := configPath()
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := runFirstRunCommand(t, &setupStatusRunner{status: runner.LocalStatus{Available: true}}, "models", "--model", "local", "--json")
			if err != nil {
				t.Fatalf("native status must not depend on config: %v %s", err, out)
			}
			rows := modelRows(t, out, "--json")
			if len(rows) != 1 || rows[0]["model"] != "local" || rows[0]["available"] != true || rows[0]["default"] != tc.wantDefault {
				t.Fatalf("local row: %s", out)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o644 {
				t.Fatalf("config mode changed: %v %v", info.Mode(), err)
			}
			if raw, _ := os.ReadFile(path); string(raw) != tc.body {
				t.Fatalf("config rewritten: %q", raw)
			}
		})
	}
}

func TestTransportMissingIsReportedAndRemedied(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, false, 27)
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	out, _ := runFirstRunCommand(t, &setupStatusRunner{}, "doctor", "--model", "cloud")
	if strings.Contains(out, "Next: shortcuts list") || !strings.HasSuffix(out, "Next: "+shortcutsRepairAction+"\n") {
		t.Fatalf("selected doctor: %s", out)
	}
	out, err := runFirstRunCommand(t, &setupStatusRunner{status: runner.LocalStatus{Available: true}}, "models", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"auto", "cloud", "on-device"} {
		if row := modelRow(t, out, model); row["reason_code"] != "transport_missing" {
			t.Fatalf("%s: %v", model, row)
		}
	}
}

func TestDoctorNextOnUnsupportedLocalPlatform(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, true, 26)
	out, _ := runFirstRunCommand(t, &setupStatusRunner{}, "doctor")
	if strings.Contains(out, "Next: sw_vers") || !strings.HasSuffix(out, "Next: hollis config set bridge cloud <name-or-uuid>\n") {
		t.Fatalf("macOS 26 next step: %s", out)
	}
}

func TestModelsHelperMismatchReason(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, true, 27)
	mismatch := runner.GuidedOutputRefusal()
	for _, tc := range []struct {
		err          error
		code, reason string
	}{
		{mismatch, "helper_mismatch", "present but does not match"},
		{errors.New("native helper missing"), "native_status_unknown", "native helper missing"},
	} {
		for _, args := range [][]string{{"models", "--json"}, {"models", "--model", "local", "--json"}} {
			out, err := runFirstRunCommand(t, &setupStatusRunner{err: tc.err}, args...)
			if err != nil {
				t.Fatal(err)
			}
			row := modelRow(t, out, "local")
			if reason, _ := row["reason"].(string); row["reason_code"] != tc.code || !strings.Contains(reason, tc.reason) {
				t.Fatalf("%v: %v", args, row)
			}
		}
	}
}

func TestModelsVerifiedBridgeWithUnknownPlatformIsNotUnverified(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 0)
	out, err := runFirstRunCommand(t, &setupStatusRunner{}, "models")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "cloud ") && strings.Contains(line, "unverified:") {
			t.Fatalf("verified bridge labeled unverified: %q", line)
		}
	}
	if !strings.Contains(out, "unchecked: macOS version could not be checked") {
		t.Fatalf("platform reason missing: %s", out)
	}
}
