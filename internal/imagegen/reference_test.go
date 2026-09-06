// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNewReferenceImagePopulatesMetadataAndCopiesBytes(t *testing.T) {
	data := referencePNG(t, 3, 2)
	reference, err := NewReferenceImage(data)
	if err != nil {
		t.Fatalf("NewReferenceImage: %v", err)
	}
	if reference.MIMEType != "image/png" || reference.Width != 3 || reference.Height != 2 {
		t.Fatalf("metadata = %+v", reference)
	}
	wantHash := sha256.Sum256(data)
	if reference.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("SHA256 = %q, want %x", reference.SHA256, wantHash)
	}
	data[0] = 0
	if reference.Bytes[0] == 0 {
		t.Fatal("constructor did not make a defensive copy")
	}
	if err := ValidateReferenceImage(reference); err != nil {
		t.Fatalf("ValidateReferenceImage: %v", err)
	}
}

func TestNewReferenceImageAcceptsJPEGAndRejectsUnsupportedOrCorruptData(t *testing.T) {
	jpegData := referenceJPEG(t, 4, 3)
	reference, err := NewReferenceImage(jpegData)
	if err != nil {
		t.Fatalf("NewReferenceImage JPEG: %v", err)
	}
	if reference.MIMEType != "image/jpeg" || reference.Width != 4 || reference.Height != 3 {
		t.Fatalf("JPEG metadata = %+v", reference)
	}
	for name, data := range map[string][]byte{
		"empty":   nil,
		"garbage": []byte("not an image"),
		"truncated": func() []byte {
			return jpegData[:len(jpegData)/2]
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewReferenceImage(data); !errors.Is(err, ErrInvalidReference) {
				t.Fatalf("err=%v, want ErrInvalidReference", err)
			}
		})
	}
}

func TestNewReferenceImageRejectsOversizedBytesBeforeDecode(t *testing.T) {
	data := bytes.Repeat([]byte{0x7f}, int(MaxReferenceBytes)+1)
	if _, err := NewReferenceImage(data); !errors.Is(err, ErrReferenceTooLarge) {
		t.Fatalf("err=%v, want ErrReferenceTooLarge", err)
	}
}

func TestValidateReferenceImageRejectsMutatedMetadataAndBytes(t *testing.T) {
	data := referencePNG(t, 2, 2)
	tests := []struct {
		name   string
		mutate func(*ReferenceImage)
	}{
		{name: "bytes", mutate: func(reference *ReferenceImage) { reference.Bytes[0] ^= 0xff }},
		{name: "mime", mutate: func(reference *ReferenceImage) { reference.MIMEType = "image/jpeg" }},
		{name: "width", mutate: func(reference *ReferenceImage) { reference.Width++ }},
		{name: "height", mutate: func(reference *ReferenceImage) { reference.Height++ }},
		{name: "checksum", mutate: func(reference *ReferenceImage) { reference.SHA256 = strings.Repeat("0", sha256.Size*2) }},
		{name: "handcrafted", mutate: func(reference *ReferenceImage) {
			*reference = ReferenceImage{Bytes: append([]byte(nil), data...)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reference, err := NewReferenceImage(data)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(reference)
			if err := ValidateReferenceImage(reference); !errors.Is(err, ErrInvalidReference) {
				t.Fatalf("err=%v, want ErrInvalidReference", err)
			}
		})
	}
}

func TestNewReferenceImageFromPathChecksExpectedHashAndRejectsSymlinks(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "reference.png")
	data := referencePNG(t, 5, 4)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	reference, err := NewReferenceImageFromPath(path, hex.EncodeToString(hash[:]))
	if err != nil {
		t.Fatalf("NewReferenceImageFromPath: %v", err)
	}
	if !bytes.Equal(reference.Bytes, data) {
		t.Fatal("path constructor changed reference bytes")
	}
	if _, err := NewReferenceImageFromPath(path, strings.Repeat("0", sha256.Size*2)); !errors.Is(err, ErrReferenceChecksumMismatch) {
		t.Fatalf("mismatch err=%v, want ErrReferenceChecksumMismatch", err)
	}
	link := filepath.Join(directory, "link.png")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReferenceImageFromPath(link, ""); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("symlink err=%v, want ErrInvalidReference", err)
	}
}

func TestNewReferenceImageFromPathMissingErrorDoesNotExposePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-account-reference.png")
	_, err := NewReferenceImageFromPath(path, "")
	if !errors.Is(err, ErrInvalidReference) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err=%v, want ErrInvalidReference and fs.ErrNotExist", err)
	}
	if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), filepath.Base(path)) {
		t.Fatalf("error exposed reference path: %v", err)
	}
}

func TestReferenceFileErrorClassificationDoesNotExposePath(t *testing.T) {
	const privatePath = "/private/account-name/reference.png"
	for _, test := range []struct {
		name       string
		cause      error
		want       string
		wantTarget error
	}{
		{name: "missing", cause: fs.ErrNotExist, want: fs.ErrNotExist.Error(), wantTarget: fs.ErrNotExist},
		{name: "permission", cause: fs.ErrPermission, want: fs.ErrPermission.Error(), wantTarget: fs.ErrPermission},
		{name: "other", cause: errors.New("device error mentioning " + privatePath), want: "file access failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := classifyReferenceFileError(&fs.PathError{Op: "open", Path: privatePath, Err: test.cause})
			if err.Error() != test.want {
				t.Fatalf("error=%q, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), privatePath) {
				t.Fatalf("error exposed reference path: %v", err)
			}
			if test.wantTarget != nil && !errors.Is(err, test.wantTarget) {
				t.Fatalf("errors.Is(%v, %v)=false", err, test.wantTarget)
			}
		})
	}
}

func TestNewReferenceImageFromPathAcceptsPNGAndJPEG(t *testing.T) {
	for _, test := range []struct {
		name     string
		data     func(*testing.T, int, int) []byte
		wantMIME string
	}{
		{name: "png", data: referencePNG, wantMIME: "image/png"},
		{name: "jpeg", data: referenceJPEG, wantMIME: "image/jpeg"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "alternate path with spaces")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "reference."+test.name)
			data := test.data(t, 3, 2)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			reference, err := NewReferenceImageFromPath(path, "")
			if err != nil {
				t.Fatalf("NewReferenceImageFromPath: %v", err)
			}
			if reference.MIMEType != test.wantMIME || reference.Width != 3 || reference.Height != 2 || !bytes.Equal(reference.Bytes, data) {
				t.Fatalf("reference=%+v, want %s 3x2 with unchanged bytes", reference, test.wantMIME)
			}
		})
	}
}

func TestNewReferenceImageFromPathRejectsOversizedFileBeforeDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxReferenceBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReferenceImageFromPath(path, ""); !errors.Is(err, ErrReferenceTooLarge) {
		t.Fatalf("err=%v, want ErrReferenceTooLarge", err)
	}
}

func TestNewReferenceImageFromPathRejectsNonRegularWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "reference.pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := NewReferenceImageFromPath(fifo, "")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidReference) {
			t.Fatalf("FIFO err=%v, want ErrInvalidReference", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO reference read blocked")
	}
}

func TestBridgeInputReferenceUsesRawBase64AndKeepsLegacyPayloadStable(t *testing.T) {
	prompt := "line one\nquoted \"snowman\" ☃"
	legacy, err := bridgeInput(Request{Prompt: prompt, Style: "animation"})
	if err != nil {
		t.Fatalf("legacy bridgeInput: %v", err)
	}
	if legacy != `{"prompt":"line one\nquoted \"snowman\" ☃","style":"Animation"}` {
		t.Fatalf("legacy payload changed: %q", legacy)
	}

	data := referencePNG(t, 2, 1)
	reference, err := NewReferenceImage(data)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := bridgeInput(Request{Prompt: prompt, Style: "animation", Reference: reference})
	if err != nil {
		t.Fatalf("reference bridgeInput: %v", err)
	}
	var decoded struct {
		Prompt          string `json:"prompt"`
		Style           string `json:"style"`
		ReferenceBase64 string `json:"reference_base64"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if decoded.Prompt != prompt || decoded.Style != "Animation" {
		t.Fatalf("payload fields = %+v", decoded)
	}
	if decoded.ReferenceBase64 != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("reference_base64 does not contain raw image bytes")
	}
	if _, err := bridgeInput(Request{Prompt: prompt, Reference: reference}); !errors.Is(err, ErrReferenceRequiresStyle) {
		t.Fatalf("legacy reference err=%v, want ErrReferenceRequiresStyle", err)
	}
}

func TestGenerateRejectsInvalidReferenceBeforeCommand(t *testing.T) {
	transport, trace := newTestTransport(t, "success")
	_, err := transport.Generate(t.Context(), Request{
		Prompt:    "draw",
		BridgeRef: "bridge",
		Style:     "animation",
		Reference: &ReferenceImage{Bytes: []byte("not an image")},
	})
	if !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("err=%v, want ErrInvalidReference", err)
	}
	if trace.name != "" {
		t.Fatalf("command started for invalid reference: %q", trace.name)
	}
}

func referencePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x + 10), G: uint8(y + 20), B: 90, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func referenceJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 40, G: uint8(x + 30), B: uint8(y + 50), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
