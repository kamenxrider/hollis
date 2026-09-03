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
// GitHub: both macOS binaries, deterministic unsigned bridges, their archive,
// and checksum input. SBOM creation and provenance attestations remain covered
// by the pinned workflow contract because those are GitHub-hosted operations.
func TestReleaseDryRun(t *testing.T) {
	repo := repoRoot(t)
	dist := t.TempDir()
	for _, arch := range []string{"arm64", "amd64"} {
		output := filepath.Join(dist, "hollis-darwin-"+arch)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X github.com/kamenxrider/hollis/internal/cli.version=0.2.0-dry-run", "-o", output, "./cmd/hollis")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=darwin", "GOARCH="+arch)
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s release candidate: %v\n%s", arch, err, raw)
		}
		assertNonEmptyRegularFile(t, output)
	}

	bridgeDir := filepath.Join(dist, "bridges")
	generate := exec.Command("python3", filepath.Join(repo, "scripts", "make-bridge.py"), "--os", "27", bridgeDir)
	if raw, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate release bridges: %v\n%s", err, raw)
	}
	entries, err := os.ReadDir(bridgeDir)
	if err != nil {
		t.Fatal(err)
	}
	var bridges []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".shortcut") && !strings.HasSuffix(entry.Name(), ".signed.shortcut") {
			bridges = append(bridges, entry.Name())
		}
	}
	sort.Strings(bridges)
	if len(bridges) != 4 {
		t.Fatalf("release dry-run found %d unsigned bridges, want 4: %v", len(bridges), bridges)
	}
	archive := filepath.Join(dist, "hollis-bridges.zip")
	zipFile, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(zipFile)
	for _, name := range bridges {
		writer, err := zipWriter.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(bridgeDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatal(err)
	}
	assertNonEmptyRegularFile(t, archive)

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
