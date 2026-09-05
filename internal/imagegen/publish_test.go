// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeTestPNG(t *testing.T, path string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 13), G: uint8(y * 17), B: 91, A: 255})
		}
	}
	var contents bytes.Buffer
	if err := png.Encode(&contents, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	if err := os.WriteFile(path, contents.Bytes(), 0o600); err != nil {
		t.Fatalf("write test PNG: %v", err)
	}
	return contents.Bytes()
}

func TestPreflightDestinationRejectsInvalidPathsBeforeGeneration(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.png")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.png")
	if err := os.Symlink(existing, link); err != nil {
		t.Fatal(err)
	}
	symlinkedDir := filepath.Join(dir, "linked-dir")
	if err := os.Symlink(dir, symlinkedDir); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "empty", path: "", want: ErrInvalidDestination},
		{name: "wrong extension", path: filepath.Join(dir, "output.jpg"), want: ErrInvalidDestination},
		{name: "missing parent", path: filepath.Join(dir, "missing", "output.png"), want: ErrInvalidDestination},
		{name: "symlinked parent", path: filepath.Join(symlinkedDir, "output.png"), want: ErrInvalidDestination},
		{name: "existing file", path: existing, want: ErrDestinationExists},
		{name: "existing symlink", path: link, want: ErrDestinationExists},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := PreflightDestination(test.path)
			if !errors.Is(err, test.want) {
				t.Fatalf("PreflightDestination err=%v, want errors.Is(..., %v)", err, test.want)
			}
		})
	}
}

func TestPublishWritesValidatedPNGWithMetadata(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged.png")
	contents := writeTestPNG(t, staged, 3, 2)
	destination := filepath.Join(dir, "final.png")

	published, err := Publish(staged, destination)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if published.Path != destination || published.Format != "PNG" || published.Bytes != int64(len(contents)) {
		t.Fatalf("unexpected metadata: %+v", published)
	}
	if published.Width != 3 || published.Height != 2 || len(published.SHA256) != 64 {
		t.Fatalf("unexpected dimensions/checksum: %+v", published)
	}

	info, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("destination mode=%#o, want 0600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("left private publish temporary entries: %v", entries)
	}
}

func TestPublishIndependentlyRejectsBadStagedFiles(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name     string
		makeFile func(path string) error
		want     error
	}{
		{name: "missing", makeFile: func(path string) error { return nil }, want: ErrNoOutput},
		{name: "empty", makeFile: func(path string) error { return os.WriteFile(path, nil, 0o600) }, want: ErrNoOutput},
		{name: "not PNG", makeFile: func(path string) error { return os.WriteFile(path, []byte("not png"), 0o600) }, want: ErrInvalidPNG},
		{
			name: "oversize",
			makeFile: func(path string) error {
				return os.WriteFile(path, bytes.Repeat([]byte{'x'}, int(MaxOutputBytes)+1), 0o600)
			},
			want: ErrImageTooLarge,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			staged := filepath.Join(dir, test.name+".png")
			if err := test.makeFile(staged); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(dir, test.name+"-final.png")
			_, err := Publish(staged, destination)
			if !errors.Is(err, test.want) {
				t.Fatalf("Publish err=%v, want errors.Is(..., %v)", err, test.want)
			}
			if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid publish created destination: %v", statErr)
			}
		})
	}
}

func TestPublishRejectsStagedSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.png")
	writeTestPNG(t, target, 2, 2)
	staged := filepath.Join(dir, "staged.png")
	if err := os.Symlink(target, staged); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "final.png")
	if _, err := Publish(staged, destination); !errors.Is(err, ErrInvalidPNG) {
		t.Fatalf("Publish symlink err=%v, want ErrInvalidPNG", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink publish created destination: %v", err)
	}
}

func TestPublishDoesNotReplaceExistingDestination(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged.png")
	writeTestPNG(t, staged, 2, 2)
	destination := filepath.Join(dir, "final.png")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Publish(staged, destination)
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("Publish collision err=%v, want ErrDestinationExists", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("collision replaced destination with %q", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("collision left unexpected entries: %v", entries)
	}
}

func TestValidateStagedPNGRejectsPixelLimit(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "large.png")
	img := image.NewPaletted(image.Rect(0, 0, 4001, 4001), color.Palette{color.Gray{Y: 90}})
	var contents bytes.Buffer
	if err := png.Encode(&contents, img); err != nil {
		t.Fatalf("encode large test PNG: %v", err)
	}
	if err := os.WriteFile(staged, contents.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(staged, filepath.Join(dir, "final.png")); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("Publish pixel-limit err=%v, want ErrImageTooLarge", err)
	}
}
