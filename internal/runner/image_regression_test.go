// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These cases exercise bounded filesystem and transport behavior using a
// local fake. They make no claim about provider understanding.
func TestImageRequestRejectsNonregularAndBrokenLinksWithoutSpawn(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "folder.png")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "pipe.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.png")
	if err := os.Symlink(filepath.Join(dir, "missing.png"), broken); err != nil {
		t.Fatal(err)
	}
	linkedFIFO := filepath.Join(dir, "linked-pipe.png")
	if err := os.Symlink(fifo, linkedFIFO); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{folder, fifo, broken, linkedFIFO} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			r, records := runnerWithFake(t, "echo-image")
			// A FIFO must be rejected before os.Open, which would block waiting
			// for a writer. Supply a writer so a regression fails rather than hangs.
			fd, err := syscall.Open(fifo, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer syscall.Close(fd)
			_, _, err = r.RunWithImages(context.Background(), ModelCloud, "Describe", []string{path})
			var runErr *Error
			if !errors.As(err, &runErr) || runErr.Kind != KindUsage {
				t.Fatalf("err=%v, want usage error", err)
			}
			if _, err := os.Stat(filepath.Join(records, "count.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid image reached transport: %v", err)
			}
		})
	}
}

func TestImageRequestRejectsFinalSymlinkAndAcceptsUppercaseExtension(t *testing.T) {
	original := writeTestImage(t, "original.png")
	link := filepath.Join(t.TempDir(), "linked image.PNG")
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
	r, records := runnerWithFake(t, "echo-image")
	_, _, err := r.RunWithImages(context.Background(), ModelChatGPT, "Describe", []string{link})
	var runErr *Error
	if !errors.As(err, &runErr) || runErr.Kind != KindUsage {
		t.Fatalf("err=%v, want usage error for final symlink", err)
	}
	if _, err := os.Stat(filepath.Join(records, "count.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink reached transport: %v", err)
	}

	uppercase := writeTestImage(t, "linked image.PNG")
	r, records = runnerWithFake(t, "echo-image")
	got, used, err := r.RunWithImages(context.Background(), ModelChatGPT, "Describe", []string{uppercase})
	if err != nil || got != "Describe" || used != ModelChatGPT {
		t.Fatalf("uppercase extension got=%q used=%q err=%v", got, used, err)
	}
	args, err := os.ReadFile(filepath.Join(records, "argv-lines.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), uppercase) {
		t.Fatalf("transport received original uppercase path: %q", args)
	}
}

func TestImageRequestRejectsSymlinkedParentDirectories(t *testing.T) {
	privateImage := writeTestImage(t, "private.png")
	workspace := t.TempDir()
	link := filepath.Join(workspace, "photos")
	if err := os.Symlink(filepath.Dir(privateImage), link); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(filepath.Dir(privateImage), "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(privateImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "private.png"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(link, "private.png"), filepath.Join(link, "nested", "private.png")} {
		t.Run(path, func(t *testing.T) {
			r, records := runnerWithFake(t, "echo-image")
			_, _, err := r.RunWithImages(context.Background(), ModelCloud, "Describe", []string{path})
			var runErr *Error
			if !errors.As(err, &runErr) || runErr.Kind != KindUsage {
				t.Fatalf("err=%v, want usage refusal for symlinked parent", err)
			}
			if _, err := os.Stat(filepath.Join(records, "count.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("symlinked parent reached transport: %v", err)
			}
		})
	}
}

func TestImageRequestAllowsSearchableDirectoryWithoutListingAccess(t *testing.T) {
	path := writeTestImage(t, "photo.png")
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	r, _ := runnerWithFake(t, "echo-image")
	if got, _, err := r.RunWithImages(context.Background(), ModelCloud, "Describe", []string{path}); err != nil || got != "Describe" {
		t.Fatalf("readable image in searchable directory got=%q err=%v", got, err)
	}
}

func TestImageRequestRejectsMalformedArgumentsWithoutSpawn(t *testing.T) {
	image := writeTestImage(t, "one.png")
	for _, tc := range []struct {
		name   string
		model  Model
		prompt string
		paths  []string
	}{
		{name: "no images", model: ModelCloud, prompt: "Describe"},
		{name: "blank path", model: ModelCloud, prompt: "Describe", paths: []string{" \t"}},
		{name: "invalid UTF-8", model: ModelCloud, prompt: "Describe\xff", paths: []string{image}},
		{name: "unknown model", model: Model("unknown"), prompt: "Describe", paths: []string{image}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, records := runnerWithFake(t, "echo-image")
			_, _, err := r.RunWithImages(context.Background(), tc.model, tc.prompt, tc.paths)
			var runErr *Error
			if !errors.As(err, &runErr) || runErr.Kind != KindUsage {
				t.Fatalf("err=%v, want usage error", err)
			}
			if _, err := os.Stat(filepath.Join(records, "count.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid image request reached transport: %v", err)
			}
		})
	}
}

func TestImagePromptCleanupAfterInterruptedTransport(t *testing.T) {
	image := writeTestImage(t, "one.png")
	for _, tc := range []struct {
		name string
		kind Kind
	}{
		{name: "start failure", kind: KindTransport},
		{name: "canceled before start", kind: KindContextCanceled},
		{name: "timeout", kind: KindTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, records := runnerWithFake(t, "hang")
			promptDir := t.TempDir()
			t.Setenv("TMPDIR", promptDir)
			ctx := context.Background()
			switch tc.name {
			case "start failure":
				r.ShortcutsPath = filepath.Join(records, "missing-shortcuts")
			case "canceled before start":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "timeout":
				r.Timeout = 500 * time.Millisecond
			}
			_, _, err := r.RunWithImages(ctx, ModelCloud, "Private image prompt", []string{image})
			var runErr *Error
			if !errors.As(err, &runErr) || runErr.Kind != tc.kind {
				t.Fatalf("err=%v, want %s", err, tc.kind)
			}
			if tc.name == "timeout" {
				args, err := os.ReadFile(filepath.Join(records, "argv-lines.txt"))
				if err != nil {
					t.Fatalf("transport did not start before timeout: %v", err)
				}
				if !strings.Contains(string(args), "--input-path\n"+promptDir+string(os.PathSeparator)) {
					t.Fatalf("transport did not receive private prompt file: %q", args)
				}
			}
			remaining, err := os.ReadDir(promptDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(remaining) != 0 {
				t.Fatalf("interrupted transport left %d temporary files", len(remaining))
			}
		})
	}
}
