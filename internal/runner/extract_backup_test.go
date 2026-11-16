// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeExtractSlowOCRBackupBounded(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "fast-started")
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			body := "printf fast > " + quote(marker) + "\nexec sleep 60\n"
			if harness := os.Getenv("HOLLIS_TEST_OCR_HARNESS"); harness != "" {
				// The native suite supplies the real policy with an injected
				// accurate failure and a slow fast call, not a model helper.
				body = "exec " + quote(harness) + " extract-backup " +
					quote(os.Getenv("HOLLIS_TEST_OCR_PDF")) + " slow-fast " + quote(marker) + "\n"
			}
			r := fakeNative(t, "cat >/dev/null\n"+extractReady()+body)
			r.Timeout = 2 * time.Second
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			start := time.Now()
			go func() {
				_, err := r.ExtractPDF(ctx, []byte(pdfFixture), nil)
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			ready := time.NewTimer(3 * time.Second)
			defer ready.Stop()
		waitForFast:
			for {
				select {
				case err := <-done:
					t.Fatalf("helper stopped before fast began: %v", err)
				case <-ready.C:
					t.Fatal("fast backup did not start")
				case <-ticker.C:
					if content, err := os.ReadFile(marker); err == nil && string(content) == "fast" {
						break waitForFast
					}
				}
			}
			want := KindTimeout
			if mode == "cancel" {
				cancel()
				want = KindContextCanceled
			}
			select {
			case err := <-done:
				requireNativeKind(t, err, want)
			case <-time.After(3 * time.Second):
				t.Fatal("slow fast backup outlived the helper's original bound")
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatalf("slow backup took %s; helper timeout is 2s", elapsed)
			}
		})
	}
}
