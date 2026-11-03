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

func TestNativeImageErrorEnvelopePrivacy(t *testing.T) {
	for _, mode := range []string{"human", "json", "agent"} {
		for _, source := range []string{"missing", "corrupt", "staging-failure"} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "PRIVATE_SOURCE_CANARY.png")
				stage := filepath.Join(dir, "PRIVATE_STAGE_CANARY")
				if err := os.Mkdir(stage, 0o700); err != nil {
					t.Fatal(err)
				}
				if source == "corrupt" {
					if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nPRIVATE_CONTENT_CANARY"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if source == "staging-failure" {
					stage = filepath.Join(stage, "absent")
				}
				t.Setenv("TMPDIR", stage)
				native := &runner.NativeRunner{}
				_, failure := native.RunCompleteWithImages(t.Context(), runner.ModelLocal, "PRIVATE_PROMPT_CANARY", []string{path})
				if failure == nil {
					t.Fatal("expected pre-helper failure")
				}
				cmd, flags := newRootCmdWithFlags(func() runner.Runner { return native })
				flags.asJSON, flags.agent = mode == "json", mode == "agent"
				var out, stderr bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&stderr)
				reported := reportCommandError(cmd, flags, toCLIError(failure))
				wantExit := 2
				if source == "staging-failure" {
					wantExit = 5
				}
				if reported == nil || ExitCode(reported) != wantExit {
					t.Fatalf("unexpected error exit: %v", reported)
				}
				if mode != "human" {
					var envelope struct {
						Error struct {
							Message string `json:"message"`
							Exit    int    `json:"exit_code"`
						} `json:"error"`
					}
					if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.Error.Exit != wantExit || envelope.Error.Message == "" {
						t.Fatalf("invalid error envelope: %v", err)
					}
				}
				diagnostics := reported.Error() + out.String() + stderr.String()
				if strings.Contains(diagnostics, "CANARY") || strings.Contains(diagnostics, dir) ||
					strings.Contains(diagnostics, "hollis-image-inputs-") {
					t.Fatal("private image diagnostic escaped public error reporting")
				}
				if source != "staging-failure" {
					entries, err := os.ReadDir(stage)
					if err != nil || len(entries) != 0 {
						t.Fatal("failed image left private staging behind")
					}
				}
			})
		}
	}
}
