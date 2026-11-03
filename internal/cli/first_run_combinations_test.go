// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestDoctorUnsupportedPlatformAndMissingTransportNeverCycles(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, false, 26)
	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	for _, args := range [][]string{{"doctor"}, {"doctor", "--model", "local"}, {"doctor", "--model", "cloud"}} {
		out, _ := runFirstRunCommand(t, &setupStatusRunner{}, args...)
		_, next, ok := strings.Cut(out, "Next: ")
		if !ok || strings.Contains(next, "hollis doctor") || strings.Contains(next, "curl") {
			t.Fatalf("%v: %s", args, out)
		}
		if !strings.Contains(next, "macOS") || !strings.Contains(strings.ToLower(next), "shortcuts") {
			t.Fatalf("missing actionable platform/transport guidance: %s", next)
		}
	}
}

func TestNativeDiagnosticCausesAgreeAcrossCommands(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		err          error
	}{
		{"unsupported architecture", "platform_unsupported", &runner.Error{Kind: runner.KindLocalUnavailable, NativeReason: "platform_unsupported", ExitCode: -1, Err: errors.New("native local requires Apple Silicon and macOS 27 or later")}},
		{"old macOS", "platform_unsupported", &runner.Error{Kind: runner.KindLocalUnavailable, NativeReason: "platform_unsupported", ExitCode: -1, Err: errors.New("native local requires macOS 27 or later")}},
		{"unknown platform", "platform_unknown", &runner.Error{Kind: runner.KindLocalUnavailable, NativeReason: "platform_unknown", ExitCode: -1, Err: errors.New("could not determine macOS version for native local")}},
		{"identity mismatch", "helper_mismatch", runner.GuidedOutputRefusal()},
		{"invalid JSON", "native_protocol", &runner.Error{Kind: runner.KindNativeProtocol, ExitCode: -1, Err: errors.New("invalid native event")}},
		{"early exit", "native_protocol", &runner.Error{Kind: runner.KindNativeProtocol, ExitCode: -1, Err: errors.New("native helper ended without completion")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, allImportedNames(), true, 27)
			for _, args := range [][]string{{"models", "--json"}, {"models", "--model", "local", "--json"}, {"doctor", "--model", "local", "--json"}} {
				out, _ := runFirstRunCommand(t, &setupStatusRunner{err: tc.err}, args...)
				var row map[string]any
				if args[0] == "doctor" {
					if err := json.Unmarshal([]byte(out), &row); err != nil {
						t.Fatal(err)
					}
				} else {
					row = modelRow(t, out, "local")
				}
				if row["reason_code"] != tc.reason {
					t.Fatalf("%v: %s", args, out)
				}
			}
			out, _ := runFirstRunCommand(t, &setupStatusRunner{err: tc.err}, "doctor", "--model", "local")
			if strings.Contains(out, "curl") || strings.Contains(out, "install-hollis.sh") {
				t.Fatalf("installation ownership guessed: %s", out)
			}
			if tc.reason == "helper_mismatch" && !strings.Contains(out, "same installation source") {
				t.Fatalf("missing matched-install guidance: %s", out)
			}
		})
	}
}

func TestAutoUnknownPlatformMatchesConcreteRoutes(t *testing.T) {
	for _, listed := range []bool{false, true} {
		for _, names := range [][]string{nil, allImportedNames()} {
			t.Run(fmt.Sprintf("listed=%t/names=%d", listed, len(names)), func(t *testing.T) {
				stubConfigPath(t)
				stubResolution(t, names, listed, 0)
				out, err := runFirstRunCommand(t, &setupStatusRunner{status: runner.LocalStatus{Available: true}}, "models", "--json")
				if err != nil {
					t.Fatal(err)
				}
				for _, model := range []string{"auto", "cloud", "on-device"} {
					row := modelRow(t, out, model)
					if row["reason_code"] != "platform_unknown" {
						t.Fatalf("%s: %v", model, row)
					}
				}
			})
		}
	}
}
