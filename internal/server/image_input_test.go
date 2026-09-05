// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageImagesPreservesBytesOrderPermissionsAndCleansUp(t *testing.T) {
	pngBytes := encodeTestPNG(t, color.RGBA{R: 0x11, A: 0xff})
	jpegBytes := encodeTestJPEG(t, color.RGBA{G: 0x77, A: 0xff})
	staged, err := stageImages(t.Context(), []encodedImage{
		{DataURL: imageDataURL("image/png", pngBytes)},
		{DataURL: imageDataURL("image/jpeg", jpegBytes)},
	})
	if err != nil {
		t.Fatalf("stageImages: %v", err)
	}
	t.Cleanup(staged.Cleanup)
	if len(staged.Paths) != 2 {
		t.Fatalf("paths = %v, want two", staged.Paths)
	}
	wantBytes := [][]byte{pngBytes, jpegBytes}
	wantExtensions := []string{".png", ".jpg"}
	for i, path := range staged.Paths {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read staged image %d: %v", i, err)
		}
		if sha256.Sum256(got) != sha256.Sum256(wantBytes[i]) {
			t.Fatalf("staged image %d bytes changed", i)
		}
		if filepath.Ext(path) != wantExtensions[i] {
			t.Fatalf("path %d extension = %q, want %q", i, filepath.Ext(path), wantExtensions[i])
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat staged image %d: %v", i, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("image %d permissions = %o, want 600", i, info.Mode().Perm())
		}
	}
	dir := filepath.Dir(staged.Paths[0])
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat staging directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions = %o, want 700", info.Mode().Perm())
	}

	staged.Cleanup()
	staged.Cleanup()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory remains after cleanup: %v", err)
	}
}

func TestStageImagesRejectsUnsupportedSourcesWithoutEchoingInput(t *testing.T) {
	inputs := []string{
		"https://example.invalid/image.png",
		"file:///private/example.png",
		"/private/example.png",
		"data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==",
		"data:image/png;charset=utf-8;base64,AAAA",
		"DATA:image/png;base64,AAAA",
	}
	for _, input := range inputs {
		t.Run(input[:min(len(input), 24)], func(t *testing.T) {
			_, err := stageImages(t.Context(), []encodedImage{{DataURL: input}})
			assertImageValidationError(t, err, http.StatusBadRequest, "invalid_image")
			if strings.Contains(err.Error(), input) {
				t.Fatalf("error echoed image input: %v", err)
			}
		})
	}
}

func TestStageImagesRejectsInvalidBase64AndMIMEMismatch(t *testing.T) {
	pngBytes := encodeTestPNG(t, color.RGBA{B: 0x44, A: 0xff})
	jpegBytes := encodeTestJPEG(t, color.RGBA{R: 0x99, A: 0xff})
	tests := []struct {
		name string
		url  string
	}{
		{name: "invalid alphabet", url: "data:image/png;base64,!!!!"},
		{name: "invalid padding", url: "data:image/png;base64,AA=A"},
		{name: "noncanonical trailing bits", url: "data:image/png;base64,AB=="},
		{name: "png declared jpeg", url: imageDataURL("image/jpeg", pngBytes)},
		{name: "jpeg declared png", url: imageDataURL("image/png", jpegBytes)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := stageImages(t.Context(), []encodedImage{{DataURL: tc.url}})
			assertImageValidationError(t, err, http.StatusBadRequest, "invalid_image")
		})
	}
}

func TestStageImagesRejectsCorruptAndTruncatedImages(t *testing.T) {
	validPNG := encodeTestPNG(t, color.RGBA{R: 1, G: 2, B: 3, A: 0xff})
	validJPEG := encodeTestJPEG(t, color.RGBA{R: 4, G: 5, B: 6, A: 0xff})
	tests := []struct {
		name string
		mime string
		data []byte
	}{
		{name: "truncated png", mime: "image/png", data: validPNG[:len(validPNG)-8]},
		{name: "corrupt png", mime: "image/png", data: append(append([]byte(nil), validPNG[:len(validPNG)-9]...), 0xff)},
		{name: "truncated jpeg", mime: "image/jpeg", data: validJPEG[:len(validJPEG)/2]},
		{name: "header only png", mime: "image/png", data: pngConfigOnly(1, 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := stageImages(t.Context(), []encodedImage{{DataURL: imageDataURL(tc.mime, tc.data)}})
			assertImageValidationError(t, err, http.StatusBadRequest, "invalid_image")
		})
	}
}

func TestStageImagesEnforcesCountAndDecodedByteLimits(t *testing.T) {
	valid := imageDataURL("image/png", encodeTestPNG(t, color.RGBA{A: 0xff}))
	_, err := stageImages(t.Context(), []encodedImage{{valid}, {valid}, {valid}, {valid}})
	assertImageValidationError(t, err, http.StatusRequestEntityTooLarge, "context_too_large")

	oversized := make([]byte, MaxDecodedImageBytes+1)
	_, err = stageImages(t.Context(), []encodedImage{{DataURL: imageDataURL("image/png", oversized)}})
	assertImageValidationError(t, err, http.StatusRequestEntityTooLarge, "context_too_large")

	first := make([]byte, MaxDecodedImageBytes/2+1)
	second := make([]byte, MaxDecodedImageBytes/2)
	_, err = stageImages(t.Context(), []encodedImage{
		{DataURL: imageDataURL("image/png", first)},
		{DataURL: imageDataURL("image/png", second)},
	})
	assertImageValidationError(t, err, http.StatusRequestEntityTooLarge, "context_too_large")
}

func TestStageImagesChecksPixelLimitsBeforeFullDecode(t *testing.T) {
	_, err := stageImages(t.Context(), []encodedImage{{
		DataURL: imageDataURL("image/png", pngConfigOnly(MaxImagePixels+1, 1)),
	}})
	assertImageValidationError(t, err, http.StatusRequestEntityTooLarge, "context_too_large")

	_, err = stageImages(t.Context(), []encodedImage{
		{DataURL: imageDataURL("image/png", pngConfigOnly(12_000_001, 1))},
		{DataURL: imageDataURL("image/png", pngConfigOnly(12_000_000, 1))},
	})
	assertImageValidationError(t, err, http.StatusRequestEntityTooLarge, "context_too_large")
}

func TestStageImagesHonorsCanceledContextAndLeavesNoFiles(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := stageImages(ctx, []encodedImage{{
		DataURL: imageDataURL("image/png", encodeTestPNG(t, color.RGBA{A: 0xff})),
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	entries, readErr := os.ReadDir(tempRoot)
	if readErr != nil {
		t.Fatalf("read temp root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("canceled staging left files: %v", entries)
	}
}

func TestStageImagesCleansPartialStagingAfterCancellation(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	first := encodeTestPNG(t, color.RGBA{R: 0x33, A: 0xff})
	second := encodeTestJPEG(t, color.RGBA{B: 0x66, A: 0xff})
	ctx := &cancelAfterErrChecks{Context: context.Background(), cancelOn: 9}
	_, err := stageImages(ctx, []encodedImage{
		{DataURL: imageDataURL("image/png", first)},
		{DataURL: imageDataURL("image/jpeg", second)},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	entries, readErr := os.ReadDir(tempRoot)
	if readErr != nil {
		t.Fatalf("read temp root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("partial staging remains after cancellation: %v", entries)
	}
}

type cancelAfterErrChecks struct {
	context.Context
	checks   int
	cancelOn int
}

func (c *cancelAfterErrChecks) Err() error {
	c.checks++
	if c.checks >= c.cancelOn {
		return context.Canceled
	}
	return nil
}

func assertImageValidationError(t *testing.T, err error, status int, code string) {
	t.Helper()
	if err == nil {
		t.Fatal("stageImages succeeded, want error")
	}
	var validationErr *requestValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want requestValidationError: %v", err, err)
	}
	if validationErr.status != status || validationErr.code != code {
		t.Fatalf("error = status %d code %q, want status %d code %q", validationErr.status, validationErr.code, status, code)
	}
}

func imageDataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func encodeTestPNG(t *testing.T, pixel color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, pixel)
	img.Set(1, 0, color.RGBA{A: 0xff})
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return output.Bytes()
}

func encodeTestJPEG(t *testing.T, pixel color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, pixel)
	img.Set(1, 0, color.RGBA{A: 0xff})
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, nil); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return output.Bytes()
}

// pngConfigOnly is a valid PNG signature and IHDR followed by IEND, with no
// image data. DecodeConfig accepts its dimensions while a full decode fails.
func pngConfigOnly(width, height int) []byte {
	var output bytes.Buffer
	output.Write([]byte("\x89PNG\r\n\x1a\n"))
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8
	ihdr[9] = 2
	writePNGChunk(&output, "IHDR", ihdr)
	writePNGChunk(&output, "IEND", nil)
	return output.Bytes()
}

func writePNGChunk(output *bytes.Buffer, kind string, data []byte) {
	_ = binary.Write(output, binary.BigEndian, uint32(len(data)))
	output.WriteString(kind)
	output.Write(data)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte(kind))
	_, _ = checksum.Write(data)
	_ = binary.Write(output, binary.BigEndian, checksum.Sum32())
}
