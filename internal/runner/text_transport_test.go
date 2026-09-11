// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The fake deliberately distinguishes native input channels: stdin reproduces
// the observed UUID-shaped failure, while file input echoes the actual bytes.
// This regression must fail on the old runner, not merely accept new argv.
func TestTextTransportPreservesCodeFixture(t *testing.T) {
	r, dir := textTransportFake(t)
	prompt := "Output exactly these two lines and nothing else. Preserve indentation. Do not add a code fence or explanation:\n\ndef answer():\n    return \"READY\""
	got, _, err := r.Run(context.Background(), ModelCloud, prompt)
	if err != nil || got != prompt {
		t.Fatalf("code fixture changed: got %q, err %v", got, err)
	}
	assertTextTransport(t, dir, prompt)
}

func textTransportFake(t *testing.T) (*ShortcutRunner, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shortcuts")
	script := `#!/bin/sh
set -eu
dir=$(dirname "$0")
printf '%s\n' "$@" > "$dir/args"
cat > "$dir/stdin"
if [ "$#" -ne 6 ]; then printf '00000000-0000-0000-0000-000000000000'; exit; fi
[ "$1" = run ] && [ "$3" = --output-type ] && [ "$4" = public.plain-text ] && [ "$5" = --input-path ]
[ ! -s "$dir/stdin" ]
cat "$6" > "$dir/staged"
case "${HOLLIS_TEXT_FAKE_OUTCOME:-echo}" in
  fail) exit 1;;
  hang) sleep 300 & echo $! > "$dir/child.pid"; wait;;
  *) cat "$6";;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r := New()
	r.ShortcutsPath = path
	return r, dir
}

func assertTextTransport(t *testing.T, dir, prompt string) {
	t.Helper()
	for name, want := range map[string]string{"stdin": "", "staged": prompt} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s byte mismatch: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(args) != 6 || args[4] != "--input-path" || filepath.Ext(args[5]) != ".txt" {
		t.Fatalf("unexpected arguments: %q", args)
	}
	if strings.Contains(string(data), prompt) {
		t.Fatal("prompt leaked to child arguments")
	}
	if _, err := os.Stat(args[5]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged prompt was not cleaned: %v", err)
	}
}

func TestTextTransportByteCorpus(t *testing.T) {
	for name, prompt := range map[string]string{
		"quotes":           "'single' \"double\" `tick` $HOME $(false)",
		"whitespace":       "  first\n\n\tsecond\n    third  ",
		"unicode":          "café e\u0301 日本語 🚀\u00a0end",
		"crlf":             "first\r\n\r\n\tsecond\r\n",
		"final-newline":    "first\nsecond\n",
		"no-final-newline": "first\nsecond",
		"128KiB":           strings.Repeat("x", 128<<10),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, dir := textTransportFake(t)
			got, _, err := r.Run(context.Background(), ModelCloudPro, prompt)
			if err != nil || got != prompt {
				t.Fatalf("byte round-trip failed: %v", err)
			}
			assertTextTransport(t, dir, prompt)
		})
	}
}

func TestTextTransportCleanupOnFailure(t *testing.T) {
	for _, outcome := range []string{"fail", "hang"} {
		t.Run(outcome, func(t *testing.T) {
			t.Setenv("HOLLIS_TEXT_FAKE_OUTCOME", outcome)
			r, dir := textTransportFake(t)
			r.Timeout = 150 * time.Millisecond
			_, _, err := r.Run(context.Background(), ModelCloud, "private payload")
			if err == nil {
				t.Fatal("expected failure")
			}
			assertTextTransport(t, dir, "private payload")
		})
	}
}

func TestTextStagingFailureDoesNotDispatchOrFallback(t *testing.T) {
	r, dir := textTransportFake(t)
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	_, _, fallback, err := r.RunWithFallback(context.Background(), ModelAuto, "private payload")
	var runErr *Error
	if !errors.As(err, &runErr) || runErr.Kind != KindTransport || fallback.Used {
		t.Fatalf("want transport failure without fallback: %v, %#v", err, fallback)
	}
	if _, err := os.Stat(filepath.Join(dir, "args")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dispatched despite failed staging")
	}
}

func TestTextPromptFilesArePrivateAndUnique(t *testing.T) {
	const count = 16
	paths := make(chan string, count)
	var wg sync.WaitGroup
	for range count {
		wg.Go(func() {
			path, cleanup, err := writePromptFile("  bytes\r\n\t✓\n")
			if err != nil {
				t.Error(err)
				return
			}
			t.Cleanup(cleanup)
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("prompt must have mode 0600: %v", err)
			}
			paths <- path
		})
	}
	wg.Wait()
	close(paths)
	seen := make(map[string]bool)
	for path := range paths {
		if seen[path] {
			t.Fatalf("concurrent staging reused path %s", path)
		}
		seen[path] = true
	}
	if len(seen) != count {
		t.Fatalf("staged %d files, want %d", len(seen), count)
	}
}

func TestTextPromptCleanupAfterSpawnFailureOrPrecancel(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "spawn-failure", true: "pre-canceled"}[canceled], func(t *testing.T) {
			r, records := textTransportFake(t)
			stage := t.TempDir()
			t.Setenv("TMPDIR", stage)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := KindTransport
			if canceled {
				cancel()
				want = KindContextCanceled
			} else {
				r.ShortcutsPath = filepath.Join(records, "missing-executable")
			}
			_, _, err := r.Run(ctx, ModelCloud, "private payload")
			var runErr *Error
			if !errors.As(err, &runErr) || runErr.Kind != want {
				t.Fatalf("want %s, got %v", want, err)
			}
			files, err := os.ReadDir(stage)
			if err != nil || len(files) != 0 {
				t.Fatalf("staged files remain: %v %v", files, err)
			}
			if _, err := os.Stat(filepath.Join(records, "args")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("child ran")
			}
		})
	}
}

func TestTextPromptCleanupAfterRunningCancellation(t *testing.T) {
	t.Setenv("HOLLIS_TEXT_FAKE_OUTCOME", "hang")
	r, dir := textTransportFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := r.Run(ctx, ModelCloud, "private payload"); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "child.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("child did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	var runErr *Error
	if err := <-done; !errors.As(err, &runErr) || runErr.Kind != KindContextCanceled {
		t.Fatalf("want cancellation, got %v", err)
	}
	assertTextTransport(t, dir, "private payload")
}

// Exercise an actual write failure after CreateTemp succeeds, in an isolated
// process so a filesystem limit cannot affect other tests or the developer.
func TestTextPromptWriteFailureHelper(t *testing.T) {
	if os.Getenv("HOLLIS_PROMPT_WRITE_FAILURE_HELPER") != "1" {
		return
	}
	signal.Ignore(syscall.SIGXFSZ)
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	limit.Cur = 128
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	r := New()
	r.ShortcutsPath = os.Getenv("HOLLIS_PROMPT_WRITE_FAILURE_FAKE")
	_, _, fallback, err := r.RunWithFallback(context.Background(), ModelAuto, strings.Repeat("private-prompt", 512))
	var runErr *Error
	if !errors.As(err, &runErr) || runErr.Kind != KindTransport || fallback.Used {
		t.Fatalf("write failure must prevent dispatch and fallback: %v", err)
	}
	files, err := os.ReadDir(os.TempDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("partial staging file survived write failure: %v %v", files, err)
	}
}

func TestTextPromptWriteFailureRemovesPartialFileWithoutDispatch(t *testing.T) {
	r, records := textTransportFake(t)
	stage := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTextPromptWriteFailureHelper$")
	cmd.Env = append(os.Environ(), "HOLLIS_PROMPT_WRITE_FAILURE_HELPER=1", "HOLLIS_PROMPT_WRITE_FAILURE_FAKE="+r.ShortcutsPath, "TMPDIR="+stage)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write-failure helper: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(records, "args")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("child dispatched after partial write")
	}
}
