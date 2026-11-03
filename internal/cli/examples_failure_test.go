// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExamplesRecipesStopOnFailure(t *testing.T) {
	recipes := exampleRecipes(t)
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			if _, err := os.Stat(shell); os.IsNotExist(err) {
				t.Skip("shell not installed")
			}
			for _, tc := range []struct {
				name, heading, target, failCommand, wantCalls string
				readOnly                                      bool
			}{
				{"notes directory", "Summarize meeting notes", "notes.md", "", "", false},
				{"notes read-only", "Summarize meeting notes", "notes.md", "", "", true},
				{"schema directory", "Request a structured local answer", "person.json", "", "", false},
				{"schema read-only", "Request a structured local answer", "person.json", "", "", true},
				{"chat failure", "Save and find a conversation", "", "chat --model", "chat --model\n", false},
				{"list failure", "Save and find a conversation", "", "chats list", "chat --model\nchats list\n", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if tc.readOnly && os.Geteuid() == 0 {
						t.Skip("root can write read-only files")
					}
					dir := t.TempDir()
					bin, home := filepath.Join(dir, "bin"), filepath.Join(dir, "home")
					for _, path := range []string{bin, home} {
						if err := os.Mkdir(path, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					target := filepath.Join(dir, tc.target)
					if tc.target != "" {
						var err error
						if tc.readOnly {
							err = os.WriteFile(target, []byte("stale synthetic input\n"), 0o400)
						} else {
							err = os.Mkdir(target, 0o700)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					// This shell stub records dispatch, fails the selected command,
					// and succeeds otherwise. No production transport is reachable.
					wrapper := "#!/bin/sh\nprintf '%s\\n' \"$1 $2\" >> \"$HOLLIS_EXAMPLE_LOG\"\nif [ \"$1 $2\" = \"$HOLLIS_EXAMPLE_FAIL\" ]; then exit 5; fi\nexit 0\n"
					if err := os.WriteFile(filepath.Join(bin, "hollis"), []byte(wrapper), 0o700); err != nil {
						t.Fatal(err)
					}
					logPath := filepath.Join(dir, "calls")
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, shell, "-c", recipes[tc.heading])
					cmd.Dir = dir
					cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home,
						"HOLLIS_STATE_DIR=" + filepath.Join(dir, "state"),
						"HOLLIS_EXAMPLE_LOG=" + logPath, "HOLLIS_EXAMPLE_FAIL=" + tc.failCommand}
					out, err := cmd.CombinedOutput()
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("recipe did not report failure: %v output=%s", err, out)
					}
					if tc.failCommand != "" && exit.ExitCode() != 5 {
						t.Fatalf("failure masked: %v output=%s", err, out)
					}
					calls, err := os.ReadFile(logPath)
					if err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					if string(calls) != tc.wantCalls {
						t.Fatalf("dependent command dispatched: %q want %q", calls, tc.wantCalls)
					}
					if tc.readOnly {
						data, err := os.ReadFile(target)
						if err != nil || string(data) != "stale synthetic input\n" {
							t.Fatalf("stale input changed: %q %v", data, err)
						}
					}
				})
			}
		})
	}
}
