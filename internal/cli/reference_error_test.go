// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func TestMissingAndInvalidReferenceErrorsArePathFreeAndNeverGenerate(t *testing.T) {
	stubConfigPath(t)
	if err := saveConfig(config{ImageBridge: "fixture image bridge"}); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "hollis.db")
	oldOpenStore := openStore
	openStore = func() (*store.Store, error) { return store.Open(statePath) }
	t.Cleanup(func() { openStore = oldOpenStore })

	privateDir := t.TempDir()
	missingPath := filepath.Join(privateDir, "private-account-missing.png")
	invalidPath := filepath.Join(privateDir, "private-account-invalid.png")
	if err := os.WriteFile(invalidPath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, reference := range []struct {
		name string
		path string
	}{
		{name: "missing", path: missingPath},
		{name: "invalid", path: invalidPath},
	} {
		for _, command := range []struct {
			name string
			args func(string, string) []string
		}{
			{
				name: "direct image",
				args: func(referencePath, outputPath string) []string {
					return []string{"image", "generate", "draw", "--style", "animation", "--reference-image", referencePath, "--output", outputPath}
				},
			},
			{
				name: "chat image",
				args: func(referencePath, outputPath string) []string {
					return []string{"chat", "--generate-image", "--image-style", "animation", "--image-reference", referencePath, "--output", outputPath, "draw"}
				},
			},
		} {
			for _, agent := range []bool{false, true} {
				mode := "human"
				if agent {
					mode = "agent"
				}
				t.Run(reference.name+"/"+command.name+"/"+mode, func(t *testing.T) {
					generator := &fakeImageGenerator{}
					cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return &fakeRunner{} }, generator)
					args := command.args(reference.path, filepath.Join(t.TempDir(), "output.png"))
					if agent {
						args = append([]string{"--agent"}, args...)
					}
					var stdout, stderr bytes.Buffer
					cmd.SetOut(&stdout)
					cmd.SetErr(&stderr)
					cmd.SetArgs(args)
					err := cmd.Execute()
					if !errors.Is(err, imagegen.ErrInvalidReference) || ExitCode(err) != 2 {
						t.Fatalf("err=%v exit=%d, want ErrInvalidReference usage error", err, ExitCode(err))
					}
					if flags.agent != agent {
						t.Fatalf("agent mode=%v, want %v", flags.agent, agent)
					}
					if generator.calls != 0 {
						t.Fatalf("generator calls=%d, want 0", generator.calls)
					}
					combined := err.Error() + stdout.String() + stderr.String()
					if strings.Contains(combined, reference.path) || strings.Contains(combined, filepath.Base(reference.path)) {
						t.Fatalf("%s error exposed reference path: %q", mode, combined)
					}
				})
			}
		}
	}
}

func TestStoredArtifactReferenceErrorIsPathFreeAndNeverGenerates(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "private-prior-artifact.png")
	artifact, err := json.Marshal(chatImageArtifact{
		Type:   "hollis.image_artifact.v1",
		Path:   privatePath,
		SHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	history := []store.Message{{
		Role:          "assistant",
		Content:       "HOLLIS_IMAGE_ARTIFACT " + string(artifact),
		ImageArtifact: true,
	}}
	generator := &fakeImageGenerator{}
	_, _, err = executeChatImageTurn(context.Background(), history, "revise it", chatImageOptions{
		Reference:      "auto",
		ResolvedBridge: "fixture image bridge",
		ResolvedStyle:  "animation",
		Output:         filepath.Join(t.TempDir(), "output.png"),
		Generator:      generator,
	})
	if !errors.Is(err, imagegen.ErrInvalidReference) || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want ErrInvalidReference usage error", err, ExitCode(err))
	}
	if generator.calls != 0 {
		t.Fatalf("generator calls=%d, want 0", generator.calls)
	}
	if strings.Contains(err.Error(), privatePath) || strings.Contains(err.Error(), filepath.Base(privatePath)) {
		t.Fatalf("stored artifact error exposed reference path: %v", err)
	}
}
