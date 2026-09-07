// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func TestFailureContractMachineCodesAndPrivacy(t *testing.T) {
	private := errors.New("/Users/private/customer.txt token=TEST_SECRET_CANARY")
	for _, code := range []string{"request_declined", "shortcut_failed"} {
		for _, image := range []bool{false, true} {
			var err error
			if image {
				kind := imagegen.ErrorKind(code)
				if code == "shortcut_failed" {
					kind = imagegen.KindNonZeroExit
				}
				err = toImageCLIError(&imagegen.Error{Kind: kind, ExitCode: 1, Err: private})
			} else {
				err = toCLIError(&runner.Error{Kind: runner.Kind(code), ExitCode: 1, Err: private})
			}
			if ExitCode(err) != 5 || errorCode(err) != code {
				t.Errorf("image=%v code=%s got=%s exit=%d", image, code, errorCode(err), ExitCode(err))
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "CANARY") || strings.Contains(err.Error(), "install") {
				t.Errorf("unsafe or misleading error: %v", err)
			}
		}
	}
}

func TestFailureContractDirectAndStoredCommands(t *testing.T) {
	stubConfigPath(t)
	if err := saveConfig(config{ImageBridge: "fixture image bridge"}); err != nil {
		t.Fatal(err)
	}
	old := openStore
	db := filepath.Join(t.TempDir(), "chats.db")
	openStore = func() (*store.Store, error) { return store.Open(db) }
	t.Cleanup(func() { openStore = old })
	for _, code := range []string{"request_declined", "shortcut_failed"} {
		for _, args := range [][]string{
			{"respond", "--model", "cloud", "fixture"},
			{"chat", "--model", "cloud", "fixture"},
			{"image", "generate", "--style", "illustration", "--output", filepath.Join(t.TempDir(), "direct.png"), "fixture"},
			{"chat", "--generate-image", "--image-style", "illustration", "--output", filepath.Join(t.TempDir(), "chat.png"), "fixture"},
		} {
			for _, agent := range []bool{false, true} {
				t.Run(code+strings.Join(args, " "), func(t *testing.T) {
					private := errors.New("/Users/private/customer.txt token=TEST_SECRET_CANARY")
					kind := imagegen.KindRequestDeclined
					if code == "shortcut_failed" {
						kind = imagegen.KindNonZeroExit
					}
					gen := &fakeImageGenerator{err: &imagegen.Error{Kind: kind, Err: private}}
					cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return &fakeRunner{err: &runner.Error{Kind: runner.Kind(code), Err: private}} }, gen)
					var out, stderr bytes.Buffer
					cmd.SetOut(&out)
					cmd.SetErr(&stderr)
					argv := append([]string{}, args...)
					if agent {
						argv = append(argv, "--agent")
					}
					cmd.SetArgs(argv)
					err := executeCommand(cmd, flags)
					if err == nil || ExitCode(err) != 5 || errorCode(err) != code {
						t.Fatalf("error=%v machine=%s", err, errorCode(err))
					}
					combined := err.Error() + out.String() + stderr.String()
					if agent {
						var envelope struct {
							Error struct {
								Code     string
								ExitCode int `json:"exit_code"`
							}
							Meta struct {
								SchemaVersion string `json:"schema_version"`
							}
						}
						if decodeErr := json.Unmarshal(out.Bytes(), &envelope); decodeErr != nil || envelope.Error.Code != code || envelope.Error.ExitCode != 5 || envelope.Meta.SchemaVersion != "2" {
							t.Fatalf("invalid agent envelope: %s (%v)", out.String(), decodeErr)
						}
					}
					if strings.Contains(combined, "private") || strings.Contains(combined, "CANARY") {
						t.Fatalf("leak: %s", combined)
					}
					if gen.calls > 1 {
						t.Fatalf("generator called %d times", gen.calls)
					}
				})
			}
		}
	}
}
