// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeImageStagingPrivacyAndCleanup(t *testing.T) {
	for _, mode := range []string{"complete", "stream", "legacy"} {
		for _, source := range []string{"missing", "corrupt", "directory", "oversized", "pixels", "unsupported", "staging-failure"} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "PRIVATE_SOURCE_CANARY.png")
				stage := filepath.Join(dir, "PRIVATE_STAGE_CANARY")
				if err := os.Mkdir(stage, 0o700); err != nil {
					t.Fatal(err)
				}
				switch source {
				case "corrupt":
					if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nPRIVATE_CONTENT_CANARY"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "unsupported":
					if err := os.WriteFile(path, []byte("GIF89aPRIVATE_CONTENT_CANARY"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				case "oversized":
					f, err := os.Create(path)
					if err != nil {
						t.Fatal(err)
					}
					err = f.Truncate(maxImageBytes + 1)
					_ = f.Close()
					if err != nil {
						t.Fatal(err)
					}
				case "pixels":
					fixture := writeTestImage(t, "pixels.png")
					data, err := os.ReadFile(fixture)
					if err != nil {
						t.Fatal(err)
					}
					binary.BigEndian.PutUint32(data[16:20], 100_000)
					binary.BigEndian.PutUint32(data[20:24], 100_000)
					binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				case "staging-failure":
					stage = filepath.Join(stage, "missing")
				}
				t.Setenv("TMPDIR", stage)
				r := &NativeRunner{}
				var err error
				switch mode {
				case "complete":
					_, err = r.RunCompleteWithImages(t.Context(), ModelLocal, "PRIVATE_PROMPT_CANARY", []string{path})
				case "stream":
					_, err = r.StreamWithImages(t.Context(), ModelLocal, "PRIVATE_PROMPT_CANARY", []string{path}, func(string) error {
						t.Fatal("pre-helper failure emitted text")
						return nil
					})
				case "legacy":
					_, _, err = r.RunWithImages(t.Context(), ModelLocal, "PRIVATE_PROMPT_CANARY", []string{path})
				}
				want := KindUsage
				if source == "unsupported" {
					want = KindUnsupportedMedia
				} else if source == "staging-failure" {
					want = KindTransport
				}
				requireNativeKind(t, err, want)
				for e := err; e != nil; e = errors.Unwrap(e) {
					if strings.Contains(e.Error(), "CANARY") || strings.Contains(e.Error(), dir) || strings.Contains(e.Error(), "hollis-image-inputs-") {
						t.Fatalf("private diagnostic in %T", e)
					}
				}
				var classified *Error
				_ = errors.As(err, &classified)
				if source == "oversized" && (classified.Limit != int(maxImageBytes) || classified.Actual != int(maxImageBytes)+1 ||
					!strings.Contains(err.Error(), "67108865 bytes; the limit is 67108864 bytes")) {
					t.Fatal("file ceiling lost its measured numbers")
				}
				if source == "pixels" && (classified.Limit != int(maxImagePixels) || classified.Actual != 10_000_000_000 ||
					!strings.Contains(err.Error(), "10000000000 pixels; the limit is 64000000 pixels")) {
					t.Fatal("pixel ceiling lost its measured numbers")
				}
				if source != "staging-failure" {
					entries, err := os.ReadDir(stage)
					if err != nil || len(entries) != 0 {
						t.Fatalf("partial staging survived: entries=%d err=%v", len(entries), err)
					}
				}
			})
		}
	}
}

func TestNativeImageTransportDiagnosticsAreNotUnwrapped(t *testing.T) {
	for _, operation := range []string{"mkdir", "chmod", "open", "write", "close"} {
		original := &Error{Kind: KindTransport, ExitCode: -1,
			Err: fmt.Errorf("PRIVATE_SOURCE_CANARY: %w", &os.PathError{
				Op: operation, Path: "/private/PRIVATE_STAGE_CANARY", Err: os.ErrPermission,
			})}
		result := nativeImageStagingError(original)
		if result.Kind != KindTransport || strings.Contains(result.Error(), "CANARY") || errors.Is(result, os.ErrPermission) {
			t.Fatal("filesystem diagnostics escaped native staging")
		}
	}
}

func TestNativeImageShapeValidationDefersPrivatePathsToStaging(t *testing.T) {
	// Exercise the future qualified route without enabling it in production.
	original := routeSpecs[ModelLocal]
	qualified := original
	qualified.Capabilities.Images, qualified.Capabilities.MaxImages = true, 1
	routeSpecs[ModelLocal] = qualified
	t.Cleanup(func() { routeSpecs[ModelLocal] = original })
	if err := ValidateImageRequest(ModelLocal, "Describe", []string{"/PRIVATE_SOURCE_CANARY/missing.png"}); err != nil {
		t.Fatal("shape validation inspected the private source before native staging")
	}
	err := ValidateImageRequest(ModelLocal, "Describe", []string{"/PRIVATE_SOURCE_CANARY/a.png", "/PRIVATE_SOURCE_CANARY/b.png"})
	requireNativeKind(t, err, KindUnsupportedParameter)
	if strings.Contains(err.Error(), "CANARY") || !strings.Contains(err.Error(), "exactly one") {
		t.Fatal("count refusal lost its safe maximum")
	}
}

func TestNativeImagePostStagingCleanup(t *testing.T) {
	for _, failure := range []string{"timeout", "cancel", "protocol", "callback"} {
		t.Run(failure, func(t *testing.T) {
			path := writeTestImage(t, "image.png")
			stage := t.TempDir()
			script := "cat >/dev/null\n" + imageReady()
			if failure == "protocol" {
				script += "printf '%s\\n' 'bad protocol'\n"
			} else if failure == "callback" {
				script += nativePrint(nativeLine("snapshot", map[string]any{"text": "red"}))
			} else {
				script += "sleep 30\n"
			}
			r := fakeNative(t, script)
			r.Timeout = 100 * time.Millisecond
			t.Setenv("TMPDIR", stage)
			ctx := t.Context()
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := r.StreamWithImages(ctx, ModelLocal, "Describe", []string{path}, func(string) error {
				return errors.New("fixture callback failed")
			})
			if err == nil {
				t.Fatal("expected failure")
			}
			entries, err := os.ReadDir(stage)
			if err != nil || len(entries) != 0 {
				t.Fatalf("staging survived %s: entries=%d err=%v", failure, len(entries), err)
			}
		})
	}
}
