// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func newOwnedOutputResult(t *testing.T, width, height int) (Result, []byte) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "staging")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "image.png")
	contents := writeTestPNG(t, path, width, height)
	hash := sha256.Sum256(contents)
	return Result{
		Path:   path,
		Bytes:  int64(len(contents)),
		Width:  width,
		Height: height,
		SHA256: hex.EncodeToString(hash[:]),
		cleanup: &cleanupState{
			paths: []string{path},
			dir:   directory,
		},
	}, contents
}

func decodeOutputImage(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestTransformOutputResizesToExactSizeAndRefreshesMetadata(t *testing.T) {
	original, originalBytes := newOwnedOutputResult(t, 4, 2)
	output, err := TransformOutput(context.Background(), original, OutputOptions{Size: "6x4", Fit: "crop"})
	if err != nil {
		t.Fatalf("TransformOutput: %v", err)
	}
	if output.Path == original.Path {
		t.Fatal("transformation overwrote the original staged path")
	}
	if output.Width != 6 || output.Height != 4 {
		t.Fatalf("dimensions = %dx%d, want 6x4", output.Width, output.Height)
	}
	if output.cleanup != original.cleanup {
		t.Fatal("transformation did not preserve the original cleanup state")
	}
	if _, err := os.Stat(original.Path); err != nil {
		t.Fatalf("original staged image disappeared before cleanup: %v", err)
	}
	if got, err := os.ReadFile(original.Path); err != nil || !bytes.Equal(got, originalBytes) {
		t.Fatalf("original staged bytes changed: err=%v equal=%t", err, bytes.Equal(got, originalBytes))
	}

	data, err := os.ReadFile(output.Path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if output.Bytes != int64(len(data)) || output.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("stale output metadata: %+v bytes=%d checksum=%s", output, len(data), hex.EncodeToString(hash[:]))
	}
	info, err := os.Stat(output.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("transformed mode=%#o, want 0600", got)
	}
	if bounds := decodeOutputImage(t, output.Path).Bounds(); bounds.Dx() != 6 || bounds.Dy() != 4 {
		t.Fatalf("decoded bounds=%v, want 6x4", bounds)
	}

	if err := output.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(original.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original path after cleanup: %v", err)
	}
	if _, err := os.Stat(output.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transformed path after cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(original.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory after cleanup: %v", err)
	}
}

func TestTransformOutputAspectCropAndPad(t *testing.T) {
	t.Run("center crop", func(t *testing.T) {
		original, _ := newOwnedOutputResult(t, 4, 2)
		output, err := TransformOutput(context.Background(), original, OutputOptions{AspectRatio: "1:1", Fit: "crop"})
		if err != nil {
			t.Fatal(err)
		}
		if output.Width != 2 || output.Height != 2 {
			t.Fatalf("crop dimensions=%dx%d, want 2x2", output.Width, output.Height)
		}
		decoded := decodeOutputImage(t, output.Path)
		if got := color.RGBAModel.Convert(decoded.At(0, 0)).(color.RGBA); got == (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
			t.Fatal("center crop unexpectedly contains the white pad color")
		}
		if err := output.Cleanup(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("opaque white pad", func(t *testing.T) {
		original, _ := newOwnedOutputResult(t, 4, 2)
		output, err := TransformOutput(context.Background(), original, OutputOptions{AspectRatio: "1:1", Fit: "pad"})
		if err != nil {
			t.Fatal(err)
		}
		if output.Width != 4 || output.Height != 4 {
			t.Fatalf("pad dimensions=%dx%d, want 4x4", output.Width, output.Height)
		}
		decoded := decodeOutputImage(t, output.Path)
		for _, point := range []image.Point{{0, 0}, {3, 0}, {0, 3}, {3, 3}} {
			got := color.RGBAModel.Convert(decoded.At(point.X, point.Y)).(color.RGBA)
			if got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
				t.Fatalf("pad pixel %v=%v, want opaque white", point, got)
			}
		}
		if err := output.Cleanup(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTransformOutputZeroOptionsReturnsNativeResultUnchanged(t *testing.T) {
	original, originalBytes := newOwnedOutputResult(t, 3, 2)
	entriesBefore, err := os.ReadDir(filepath.Dir(original.Path))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutputOptions(OutputOptions{}); err != nil {
		t.Fatalf("ValidateOutputOptions zero value: %v", err)
	}
	output, err := TransformOutput(context.Background(), original, OutputOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if output.Path != original.Path || output.Bytes != original.Bytes || output.Width != original.Width || output.Height != original.Height || output.SHA256 != original.SHA256 || output.cleanup != original.cleanup {
		t.Fatalf("zero options changed result: got %+v want %+v", output, original)
	}
	entriesAfter, err := os.ReadDir(filepath.Dir(original.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entriesAfter) != len(entriesBefore) {
		t.Fatalf("zero options changed staging entries: before=%v after=%v", entriesBefore, entriesAfter)
	}
	if got, err := os.ReadFile(original.Path); err != nil || !bytes.Equal(got, originalBytes) {
		t.Fatalf("zero options changed original bytes: err=%v", err)
	}
	if err := output.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestTransformOutputRejectsInvalidAndOversizedOptionsWithoutMutation(t *testing.T) {
	cases := []struct {
		name    string
		options OutputOptions
		wantErr error
	}{
		{name: "fit alone", options: OutputOptions{Fit: "crop"}, wantErr: ErrInvalidOutputOptions},
		{name: "unknown fit", options: OutputOptions{AspectRatio: "1:1", Fit: "stretch"}, wantErr: ErrInvalidOutputOptions},
		{name: "zero ratio", options: OutputOptions{AspectRatio: "0:1", Fit: "crop"}, wantErr: ErrInvalidOutputOptions},
		{name: "negative ratio", options: OutputOptions{AspectRatio: "-1:1", Fit: "crop"}, wantErr: ErrInvalidOutputOptions},
		{name: "both controls", options: OutputOptions{AspectRatio: "1:1", Size: "2x2", Fit: "crop"}, wantErr: ErrInvalidOutputOptions},
		{name: "zero size", options: OutputOptions{Size: "4x0", Fit: "crop"}, wantErr: ErrInvalidOutputOptions},
		{name: "size overflow", options: OutputOptions{Size: "9223372036854775808x1", Fit: "crop"}, wantErr: ErrOutputTooLarge},
		{name: "too many pixels", options: OutputOptions{Size: "4001x4001", Fit: "crop"}, wantErr: ErrOutputTooLarge},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			original, originalBytes := newOwnedOutputResult(t, 3, 2)
			beforeEntries, err := os.ReadDir(filepath.Dir(original.Path))
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateOutputOptions(test.options); !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateOutputOptions=%v, want errors.Is(...,%v)", err, test.wantErr)
			}
			if _, err := TransformOutput(context.Background(), original, test.options); !errors.Is(err, test.wantErr) {
				t.Fatalf("TransformOutput=%v, want errors.Is(...,%v)", err, test.wantErr)
			}
			if got, err := os.ReadFile(original.Path); err != nil || !bytes.Equal(got, originalBytes) {
				t.Fatalf("invalid options changed original: err=%v", err)
			}
			afterEntries, err := os.ReadDir(filepath.Dir(original.Path))
			if err != nil {
				t.Fatal(err)
			}
			if len(afterEntries) != len(beforeEntries) {
				t.Fatalf("invalid options left staging entries: before=%v after=%v", beforeEntries, afterEntries)
			}
			if err := original.Cleanup(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type cancelAfterChecksContext struct {
	checks int64
	after  int64
}

func (c *cancelAfterChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterChecksContext) Done() <-chan struct{}       { return nil }
func (c *cancelAfterChecksContext) Value(any) any               { return nil }
func (c *cancelAfterChecksContext) Err() error {
	if atomic.AddInt64(&c.checks, 1) > c.after {
		return context.Canceled
	}
	return nil
}

func TestTransformOutputCancellationRemovesTemporaryOutput(t *testing.T) {
	original, originalBytes := newOwnedOutputResult(t, 64, 64)
	beforeEntries, err := os.ReadDir(filepath.Dir(original.Path))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &cancelAfterChecksContext{after: 40}
	_, err = TransformOutput(ctx, original, OutputOptions{Size: "2000x2000", Fit: "crop"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TransformOutput error=%v, want context.Canceled", err)
	}
	if got, readErr := os.ReadFile(original.Path); readErr != nil || !bytes.Equal(got, originalBytes) {
		t.Fatalf("canceled transform changed original: err=%v", readErr)
	}
	afterEntries, err := os.ReadDir(filepath.Dir(original.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEntries) != len(beforeEntries) {
		t.Fatalf("canceled transform left staging entries: before=%v after=%v", beforeEntries, afterEntries)
	}
	if err := original.Cleanup(); err != nil {
		t.Fatal(err)
	}
}
