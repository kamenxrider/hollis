// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// executeImageConfigCLI exercises the public root command while keeping the
// text runner fake. Image style configuration must not discover shortcuts or
// invoke a model.
func executeImageConfigCLI(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.Bytes(), err
}

func TestImageStyleBridgeConfigPersistsAndResolvesExactReferences(t *testing.T) {
	stubConfigPath(t)
	if err := saveConfig(config{
		DefaultModel: "cloud-pro",
		Bridges: map[string]string{
			"cloud":   "text-cloud-bridge",
			"chatgpt": "text-chatgpt-bridge",
		},
	}); err != nil {
		t.Fatal(err)
	}

	for _, style := range imageStyles {
		style := style
		ref := "Hollis Image " + style
		if _, err := executeImageConfigCLI(t, "config", "set", "image-bridge", style, ref); err != nil {
			t.Fatalf("set %s bridge: %v", style, err)
		}

		got, err := loadConfig()
		if err != nil {
			t.Fatalf("load after setting %s: %v", style, err)
		}
		if got.ImageBridges[style] != ref {
			t.Fatalf("persisted %s bridge = %q, want %q", style, got.ImageBridges[style], ref)
		}
		resolved, err := resolveImageBridge(style, "")
		if err != nil {
			t.Fatalf("resolve %s: %v", style, err)
		}
		if resolved != ref {
			t.Fatalf("resolved %s bridge = %q, want exact %q", style, resolved, ref)
		}
	}

	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "cloud-pro" {
		t.Fatalf("image bridge updates changed default model to %q", got.DefaultModel)
	}
	if got.Bridges["cloud"] != "text-cloud-bridge" || got.Bridges["chatgpt"] != "text-chatgpt-bridge" {
		t.Fatalf("image bridge updates changed text bridges: %+v", got.Bridges)
	}
}

func TestImageStyleBridgeRemovalPreservesExistingConfig(t *testing.T) {
	stubConfigPath(t)
	initial := config{
		DefaultModel: "cloud",
		Bridges:      map[string]string{"cloud": "text-cloud", "on-device": "text-local"},
		ImageBridges: map[string]string{"any": "old-any", "sketch": "old-sketch"},
	}
	if err := saveConfig(initial); err != nil {
		t.Fatal(err)
	}
	if _, err := executeImageConfigCLI(t, "config", "set", "image-bridge", "any", "new-any"); err != nil {
		t.Fatalf("replace image bridge: %v", err)
	}
	if _, err := executeImageConfigCLI(t, "config", "set", "image-bridge", "any", ""); err != nil {
		t.Fatalf("remove image bridge: %v", err)
	}

	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != initial.DefaultModel {
		t.Fatalf("default model changed after image bridge set/remove: %q", got.DefaultModel)
	}
	if got.Bridges["cloud"] != "text-cloud" || got.Bridges["on-device"] != "text-local" {
		t.Fatalf("text bridges changed after image bridge set/remove: %+v", got.Bridges)
	}
	if _, ok := got.ImageBridges["any"]; ok {
		t.Fatalf("removed any image bridge is still configured: %+v", got.ImageBridges)
	}
	if got.ImageBridges["sketch"] != "old-sketch" {
		t.Fatalf("removing any image bridge changed sketch mapping: %+v", got.ImageBridges)
	}
}

func TestImageStylesJSONReportsConfigurationWithoutRuntimeVerification(t *testing.T) {
	stubConfigPath(t)
	if err := saveConfig(config{ImageBridges: map[string]string{
		"any":    "bridge-any",
		"sketch": "bridge-sketch",
	}}); err != nil {
		t.Fatal(err)
	}

	out, err := executeImageConfigCLI(t, "image", "styles", "--json")
	if err != nil {
		t.Fatalf("image styles: %v", err)
	}
	var got []struct {
		Style           string `json:"style"`
		Bridge          string `json:"bridge"`
		Configured      bool   `json:"configured"`
		RuntimeVerified bool   `json:"runtime_verified"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("image styles JSON: %v (%q)", err, out)
	}
	if len(got) != len(imageStyles) {
		t.Fatalf("styles count = %d, want %d (%q)", len(got), len(imageStyles), out)
	}
	wantRefs := map[string]string{"any": "bridge-any", "sketch": "bridge-sketch"}
	for i, item := range got {
		if item.Style != imageStyles[i] {
			t.Errorf("style[%d] = %q, want %q", i, item.Style, imageStyles[i])
		}
		wantRef := wantRefs[item.Style]
		if item.Bridge != wantRef || item.Configured != (wantRef != "") {
			t.Errorf("%s status = bridge %q configured %t, want %q configured %t", item.Style, item.Bridge, item.Configured, wantRef, wantRef != "")
		}
		if item.RuntimeVerified {
			t.Errorf("%s claims runtime verification without running a Shortcut", item.Style)
		}
	}
}

func TestImageStyleResolutionRejectsUnknownUnmappedAndConflictingReferences(t *testing.T) {
	stubConfigPath(t)
	if _, err := resolveImageBridge("invented", ""); err == nil || ExitCode(err) != 2 {
		t.Fatalf("unknown style error = %v, want usage exit 2", err)
	}
	if _, err := resolveImageBridge("animation", ""); err == nil || ExitCode(err) != 2 {
		t.Fatalf("unmapped style error = %v, want usage exit 2", err)
	}
	if _, err := resolveImageBridge("animation", "explicit bridge"); err == nil || ExitCode(err) != 2 {
		t.Fatalf("style plus explicit bridge error = %v, want usage exit 2", err)
	}

	resolved, err := resolveImageBridge("", "explicit bridge")
	if err != nil || resolved != "explicit bridge" {
		t.Fatalf("explicit bridge resolution = %q, %v; want exact reference", resolved, err)
	}
	for _, ref := range []string{"   ", "-option", "  -option", "bridge\x00suffix"} {
		if _, err := resolveImageBridge("", ref); err == nil || ExitCode(err) != 2 {
			t.Fatalf("invalid explicit bridge %q error = %v, want usage exit 2", ref, err)
		}
	}

	if _, err := executeImageConfigCLI(t, "config", "set", "image-bridge", "invented", "bridge"); err == nil || ExitCode(err) != 2 {
		t.Fatalf("unknown public style error = %v, want usage exit 2", err)
	}
	if _, err := executeImageConfigCLI(t, "config", "set", "image-bridge", "any", "--", "-option"); err == nil || ExitCode(err) != 10 {
		t.Fatalf("option-like public bridge error = %v, want config exit 10", err)
	}
}

func TestImageConfigRejectsInvalidPersistedStylesAndReferences(t *testing.T) {
	for _, raw := range []string{
		`{"image_bridges":{"invented":"bridge"}}`,
		`{"image_bridges":{"any":"-option"}}`,
		`{"image_bridges":{"any":"bridge\u0000suffix"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfigAt(path); err == nil {
				t.Fatalf("invalid image config accepted: %s", raw)
			}
		})
	}
}

func TestImageStyleBridgeConfigErrorsMentionTheInvalidReference(t *testing.T) {
	stubConfigPath(t)
	_, err := executeImageConfigCLI(t, "config", "set", "image-bridge", "any", "--", "-option")
	if err == nil || !strings.Contains(err.Error(), "invalid image bridge reference") {
		t.Fatalf("error = %v, want invalid image bridge reference", err)
	}
}
