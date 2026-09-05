// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func writeReferenceFlagPNG(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reference.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return path, hex.EncodeToString(digest[:])
}

func TestChatImageReferenceFlagForwarding(t *testing.T) {
	stubConfigPath(t)
	if err := updateConfig(func(c *config) error {
		c.ImageBridge = "fixture"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "hollis.db")
	oldOpenStore := openStore
	openStore = func() (*store.Store, error) { return store.Open(dbPath) }
	t.Cleanup(func() { openStore = oldOpenStore })

	refPath, refSHA256 := writeReferenceFlagPNG(t)
	for _, tc := range []struct {
		name       string
		reference  []string
		wantSHA256 string
	}{
		{name: "default auto without history"},
		{name: "none", reference: []string{"--image-reference", "none"}},
		{name: "explicit path", reference: []string{"--image-reference", refPath}, wantSHA256: refSHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			generator := &recordingImageGenerator{dir: t.TempDir()}
			flags := &rootFlags{asJSON: true}
			cmd := newChatCmdWithImageGenerator(flags, func() runner.Runner {
				return &recordingRunner{response: "must not run"}
			}, generator)
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			args := []string{"--generate-image", "--image-style", "animation"}
			args = append(args, tc.reference...)
			args = append(args, "--output", filepath.Join(t.TempDir(), "generated.png"), "draw a red kite")
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("chat image command: %v\nstderr: %s", err, stderr.String())
			}

			calls, _, requests := generator.snapshot()
			if calls != 1 || len(requests) != 1 {
				t.Fatalf("generator calls=%d requests=%d, want 1", calls, len(requests))
			}
			if tc.wantSHA256 == "" {
				if requests[0].Reference != nil {
					t.Fatalf("reference = %+v, want nil", requests[0].Reference)
				}
				return
			}
			if requests[0].Reference == nil {
				t.Fatal("explicit reference was not forwarded to generator")
			}
			if requests[0].Reference.SHA256 != tc.wantSHA256 {
				t.Fatalf("reference SHA-256=%q, want %q", requests[0].Reference.SHA256, tc.wantSHA256)
			}
		})
	}
}
