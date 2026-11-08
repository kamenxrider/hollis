// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/jsonoutput"
	"github.com/kamenxrider/hollis/internal/runner"
)

func TestRespondJSONBridgeIntegration(t *testing.T) {
	for _, spec := range runner.Specs() {
		for _, installed := range []bool{false, true} {
			for _, envelope := range []bool{false, true} {
				t.Run(string(spec.ID)+"/"+map[bool]string{false: "fallback", true: "json"}[installed]+"/"+map[bool]string{false: "human", true: "envelope"}[envelope], func(t *testing.T) {
					stubConfigPath(t)
					names := []string{spec.BridgeNames[1]}
					if installed {
						names = append(names, spec.JSONBridge.Names[1])
					}
					stubResolution(t, names, true, 27)
					dir := t.TempDir()
					path := filepath.Join(dir, "shortcuts")
					t.Setenv("JSON_CAPTURE", filepath.Join(dir, "argv"))
					t.Setenv("JSON_PROMPT", filepath.Join(dir, "prompt"))
					script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$JSON_CAPTURE\"\ncp \"$6\" \"$JSON_PROMPT\"\nprintf '%s' '{\"price\":19.949999999999999}'\n"
					if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
						t.Fatal(err)
					}
					r := runner.New()
					r.ShortcutsPath = path
					messages := `[{"role":"system","content":"Keep fields."},{"role":"user","content":"Old."},{"role":"assistant","content":"Old answer."},{"role":"user","content":"Return an object."}]`
					args := []string{"respond", "--model", string(spec.ID), "--format", "json", "--messages", "-", "--context-strategy", "sliding-window", "--context-max-turns", "1"}
					if envelope {
						args = append(args, "--json")
					}
					out, notes, err := runContextCommand(t, r, messages, args...)
					if err != nil {
						t.Fatal(err)
					}
					argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
					prompt, _ := os.ReadFile(filepath.Join(dir, "prompt"))
					wire, ref := "public.plain-text", spec.BridgeNames[1]
					if installed {
						wire, ref = "public.json", spec.JSONBridge.Names[1]
					}
					if !strings.HasPrefix(string(argv), "run\n"+ref+"\n--output-type\n"+wire+"\n--input-path\n") ||
						!strings.Contains(string(prompt), "Instructions:\n"+jsonoutput.Instruction+"\n\nKeep fields.") ||
						strings.Contains(string(prompt), "Old answer.") {
						t.Fatalf("argv=%s prompt=%s", argv, prompt)
					}
					if !strings.Contains(out, "19.949999999999999") {
						t.Fatalf("number spelling lost: %s", out)
					}
					if envelope {
						var got map[string]any
						if json.Unmarshal([]byte(out), &got) != nil || (got["json_bridge_hint"] != nil) == installed {
							t.Fatalf("missing fallback hint: %s", out)
						}
					} else if strings.Contains(notes, "hollis setup --import "+string(spec.ID)) == installed {
						t.Fatalf("hint=%s", notes)
					}
				})
			}
		}
	}
}

func TestInvalidJSONHumanExcerpt(t *testing.T) {
	raw := "first\nsecond\t" + strings.Repeat("界", 200) + "\x1b[31m"
	err := invalidJSONObjectOutputWithHint(raw, "Import hint.", true)
	if strings.ContainsAny(err.Error(), "\r\n\t\x1b") || !strings.Contains(err.Error(), "first second") ||
		!strings.Contains(err.Error(), "...") || len([]rune(strings.TrimPrefix(err.Error(), jsonoutput.InvalidMessage))) > 140 {
		t.Fatalf("unsafe or unbounded excerpt: %q", err.Error())
	}
	fields := commandErrorFields(err)
	details := fields["details"].(map[string]any)
	if details["raw_response"] != raw || details["json_bridge_hint"] != "Import hint." || ExitCode(err) != 5 {
		t.Fatalf("raw evidence lost: %v", fields)
	}
}

func TestDoctorJSONBridgeWarnings(t *testing.T) {
	stubConfigPath(t)
	names := []string{"AFM Bridge - Cloud", "AFM Bridge JSON - Cloud", "AFM Bridge JSON - Cloud"}
	stubResolution(t, names, true, 27)
	got, err := runSetupFixture(t, &setupStatusRunner{}, "doctor", "--model", "cloud", "--json")
	if err != nil {
		t.Fatal(err)
	}
	warnings := got["warnings"].([]any)
	if len(warnings) != 1 || warnings[0].(map[string]any)["reason_code"] != "duplicate_bridge_name" {
		t.Fatalf("warnings=%v", warnings)
	}
	stubResolution(t, []string{"AFM Bridge - Cloud"}, true, 27)
	got, err = runSetupFixture(t, &setupStatusRunner{}, "doctor", "--model", "cloud", "--json")
	if err != nil || got["warnings"].([]any)[0].(map[string]any)["reason_code"] != "json_bridge_missing" {
		t.Fatalf("warnings affect readiness: err=%v got=%v", err, got)
	}
}

func TestMissingJSONBridgeWarningsRespectPlatformAndDiscovery(t *testing.T) {
	for _, tc := range []struct {
		major  int
		listOK bool
		count  int
	}{{27, true, 4}, {26, true, 3}, {0, true, 3}, {27, false, 0}} {
		resolved := runner.ResolveBridges(nil, tc.listOK, tc.major, nil)
		var models []runner.Model
		for _, spec := range runner.Specs() {
			models = append(models, spec.ID)
		}
		got := missingJSONBridgeWarnings(resolved, models, tc.major)
		if len(got) != tc.count {
			t.Fatalf("platform=%d discovery=%v: warnings=%v", tc.major, tc.listOK, got)
		}
	}
}
