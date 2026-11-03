// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestOverviewReadinessSelection(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		for _, selection := range []string{"readiness", "version,readiness"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
				stubConfigPath(t)
				stubResolution(t, nil, true, 27)
				oldBuild := macosBuild
				macosBuild = func() string { return "fixture" }
				t.Cleanup(func() { macosBuild = oldBuild })
				r := &setupStatusRunner{}
				cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
				var out, stderr bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&stderr)
				cmd.SetArgs([]string{"doctor", mode, "--select", selection})
				if err := executeCommand(cmd, flags); err != nil {
					t.Fatalf("declared overview field rejected: %v", err)
				}
				if r.probes != 1 || stderr.Len() != 0 {
					t.Fatalf("probes=%d stderr=%s", r.probes, stderr.String())
				}
				var row map[string]any
				if err := json.Unmarshal(out.Bytes(), &row); err != nil {
					t.Fatal(err)
				}
				if row["error"] != nil {
					t.Fatalf("overview error envelope: %s", out.String())
				}
				if mode == "--agent" {
					row = row["results"].(map[string]any)
				}
				if row["overview"] != true || row["readiness"] != "not_assessed" || row["inference_tested"] != false {
					t.Fatalf("overview markers lost: %s", out.String())
				}
				diagnostics, ok := row["diagnostics"].([]any)
				if !ok || len(diagnostics) != 1 || diagnostics[0].(map[string]any)["code"] != "missing" {
					t.Fatalf("diagnostics lost: %s", out.String())
				}
				wantFields := 4
				if strings.Contains(selection, "version") {
					wantFields++
					if row["version"] != version {
						t.Fatal("selected version lost")
					}
				}
				if len(row) != wantFields {
					t.Fatalf("unexpected projection: %s", out.String())
				}
			})
		}
	}
}

func TestOverviewKeepsNextAndSelectedProtocolGate(t *testing.T) {
	for _, tc := range []struct{ reason, next string }{
		{"helper_mismatch", wantInstallNext},
		{"native_protocol", "Inspect the native helper's malformed or incomplete status response"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, nil, true, 27)
			oldBuild := macosBuild
			macosBuild = func() string { return "fixture" }
			t.Cleanup(func() { macosBuild = oldBuild })
			nativeReason := ""
			if tc.reason == "helper_mismatch" {
				nativeReason = tc.reason
			}
			for _, selected := range []bool{false, true} {
				for _, mode := range []string{"", "--json", "--agent"} {
					r := &setupStatusRunner{err: &runner.Error{Kind: runner.KindNativeProtocol,
						NativeReason: nativeReason, ExitCode: -1, Err: errors.New("fixture protocol failure")}}
					cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
					var out, stderr bytes.Buffer
					cmd.SetOut(&out)
					cmd.SetErr(&stderr)
					args := []string{"doctor"}
					if selected {
						args = append(args, "--model", "local")
					}
					if mode != "" {
						args = append(args, mode, "--select")
						if selected {
							args = append(args, "reason_code")
						} else {
							args = append(args, "version")
						}
					}
					cmd.SetArgs(args)
					err := executeCommand(cmd, flags)
					wantExit := 0
					if selected {
						wantExit = 5
					}
					if ExitCode(err) != wantExit || r.probes != 1 {
						t.Fatalf("%v: err=%v probes=%d output=%s", args, err, r.probes, out.String())
					}
					if mode == "" {
						if strings.Count(out.String(), "Next:") != 1 || !strings.Contains(out.String(), "Next: "+tc.next) {
							t.Fatalf("lost truthful next action: %s", out.String())
						}
						if !selected && !strings.Contains(out.String(), "Overview only;") {
							t.Fatal("missing overview label")
						}
						if selected && !ErrorReported(err) {
							t.Fatal("selected error would print twice")
						}
						if stderr.Len() == 0 {
							t.Fatal("diagnostic disappeared")
						}
						continue
					}
					var envelope map[string]any
					if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					row := envelope
					if mode == "--agent" {
						row = envelope["results"].(map[string]any)
					}
					if stderr.Len() != 0 || strings.Contains(out.String(), "Next:") {
						t.Fatal("human diagnostic leaked into machine output")
					}
					if selected {
						failure, ok := envelope["error"].(map[string]any)
						if !ok || failure["code"] != "native_protocol" || row["reason_code"] != tc.reason {
							t.Fatalf("selected error/reason lost under projection: %s", out.String())
						}
					} else {
						if envelope["error"] != nil || row["overview"] != true || row["readiness"] != "not_assessed" || row["inference_tested"] != false {
							t.Fatalf("overview became readiness gate: %s", out.String())
						}
						diagnostics, ok := row["diagnostics"].([]any)
						if !ok || len(diagnostics) != 1 || diagnostics[0].(map[string]any)["code"] != "missing" {
							t.Fatalf("overview diagnostic lost under projection: %s", out.String())
						}
					}
				}
			}
		})
	}
}
