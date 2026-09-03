// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCountBridgesIgnoresSignedImports(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	dir := t.TempDir()
	for _, name := range []string{"AFM Bridge - Cloud.shortcut", "AFM Bridge - Cloud.signed.shortcut"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if result := runCountBridges(t, repo, dir, "1"); result.exit != 0 {
		t.Fatalf("count_bridges.py: exit=%d\n%s", result.exit, result.output)
	}
}

func TestCountBridgesValidation(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	dir := t.TempDir()
	if result := runCountBridges(t, repo, dir, "not-a-count"); result.exit != 2 {
		t.Fatalf("invalid expected count: exit=%d output=%q", result.exit, result.output)
	}
	if result := runCountBridges(t, repo, dir); result.exit != 2 {
		t.Fatalf("missing arguments: exit=%d output=%q", result.exit, result.output)
	}
	if result := runCountBridges(t, repo, filepath.Join(dir, "missing"), "0"); result.exit != 1 {
		t.Fatalf("missing directory: exit=%d output=%q", result.exit, result.output)
	}

	// A directory with a .shortcut suffix is not a bridge file and must not
	// make the count pass accidentally.
	if err := os.Mkdir(filepath.Join(dir, "not-a-bridge.shortcut"), 0o700); err != nil {
		t.Fatal(err)
	}
	if result := runCountBridges(t, repo, dir, "0"); result.exit != 0 {
		t.Fatalf("directory-shaped shortcut: exit=%d output=%q", result.exit, result.output)
	}
}

type countResult struct {
	exit   int
	output string
}

func runCountBridges(t *testing.T, repo, dir string, args ...string) countResult {
	t.Helper()
	cmdArgs := append([]string{filepath.Join(repo, "scripts", "count_bridges.py"), dir}, args...)
	cmd := exec.Command("python3", cmdArgs...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	exit := 0
	if err != nil {
		if processErr, ok := err.(*exec.ExitError); ok {
			exit = processErr.ExitCode()
		} else {
			exit = -1
		}
	}
	return countResult{exit: exit, output: strings.TrimSpace(output.String())}
}
