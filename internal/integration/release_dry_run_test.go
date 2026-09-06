// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestReleaseDryRun performs the portable part of packaging without contacting
// GitHub: both macOS binaries, unsigned model and image bridges, their archive,
// and checksum input. SBOM creation and provenance attestations remain covered
// by the pinned workflow contract because those are GitHub-hosted operations.
func TestReleaseDryRun(t *testing.T) {
	repo := repoRoot(t)
	dist := t.TempDir()
	for _, arch := range []string{"arm64", "amd64"} {
		output := filepath.Join(dist, "hollis-darwin-"+arch)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X github.com/kamenxrider/hollis/internal/cli.version=0.3.0-dry-run", "-o", output, "./cmd/hollis")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=darwin", "GOARCH="+arch)
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s release candidate: %v\n%s", arch, err, raw)
		}
		assertNonEmptyRegularFile(t, output)
	}

	packageBridges := exec.Command("python3", filepath.Join(repo, "scripts", "package-bridges.py"), dist)
	if raw, err := packageBridges.CombinedOutput(); err != nil {
		t.Fatalf("package release bridges: %v\n%s", err, raw)
	}
	archive, err := zip.OpenReader(filepath.Join(dist, "hollis-bridges.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	sort.Strings(names)
	expected := []string{
		"AFM Bridge - ChatGPT.shortcut", "AFM Bridge - Cloud Pro.shortcut",
		"AFM Bridge - Cloud.shortcut", "AFM Bridge - On-Device.shortcut",
		"Hollis Image - Reference Input v2.shortcut",
	}
	if strings.Join(names, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("release bridge membership = %v, want %v", names, expected)
	}

	for _, name := range []string{"hollis-darwin-arm64", "hollis-darwin-amd64", "hollis-bridges.zip"} {
		file, err := os.Open(filepath.Join(dist, name))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", hash.Sum(nil)); len(got) != 64 {
			t.Fatalf("invalid SHA-256 for %s: %q", name, got)
		}
	}
}

func assertNonEmptyRegularFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("%s is not a non-empty regular file", path)
	}
}
