// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseWorkflowIsImmutableAndAttested(t *testing.T) {
	t.Parallel()
	repo := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	for _, required := range []string{
		"mkdir -p dist",
		"go test -race ./...",
		"hollis.spdx.json",
		"actions/attest-build-provenance@",
		"refusing to overwrite immutable assets",
		"Unsigned macOS binaries",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("release workflow missing %q", required)
		}
	}
	if strings.Contains(workflow, "--clobber") {
		t.Error("release workflow must not overwrite existing assets")
	}
}

func TestReleaseBuildCreatesAllPublishedAssets(t *testing.T) {
	t.Parallel()
	repo := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)

	buildAt := strings.Index(workflow, "- name: Build macOS binaries")
	if buildAt < 0 {
		t.Fatal("release workflow has no macOS build step")
	}
	buildEnd := strings.Index(workflow[buildAt:], "- name: Bundle unsigned bridge shortcuts")
	if buildEnd < 0 {
		t.Fatal("release workflow has no bridge bundle step")
	}
	build := workflow[buildAt : buildAt+buildEnd]
	if !strings.Contains(build, "mkdir -p dist") {
		t.Fatal("build step must create dist before writing binaries")
	}
	if !strings.Contains(build, "-o \"dist/hollis-darwin-$arch\"") {
		t.Fatal("build step must emit both architecture-specific binaries")
	}

	checksumAt := strings.Index(workflow, "- name: Generate checksums")
	if checksumAt < 0 {
		t.Fatal("release workflow has no checksum step")
	}
	checksumEnd := strings.Index(workflow[checksumAt:], "- name: Generate SPDX SBOM")
	if checksumEnd < 0 {
		t.Fatal("release workflow has no SBOM step")
	}
	checksum := workflow[checksumAt : checksumAt+checksumEnd]
	for _, asset := range []string{"hollis-darwin-arm64", "hollis-darwin-amd64", "hollis-bridges.zip"} {
		if !strings.Contains(checksum, asset) {
			t.Errorf("checksums omit %s", asset)
		}
	}

	for _, asset := range []string{
		"dist/hollis-darwin-arm64",
		"dist/hollis-darwin-amd64",
		"dist/hollis-bridges.zip",
		"dist/SHA256SUMS",
		"dist/hollis.spdx.json",
	} {
		if !strings.Contains(workflow, asset) {
			t.Errorf("release publication omits %s", asset)
		}
	}
}

func TestActionsUseImmutableCommitPins(t *testing.T) {
	t.Parallel()
	repo := repoRoot(t)
	mutable := regexp.MustCompile(`uses:\s+[^\s]+@v[0-9]+(?:\s|$)`)
	for _, name := range []string{"ci.yml", "poolside-review.yml", "release.yml"} {
		raw, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		if hit := mutable.Find(raw); hit != nil {
			t.Errorf("%s contains mutable action pin %q", name, hit)
		}
	}
}

func TestPoolsideReviewWorkflowIsSecretSafe(t *testing.T) {
	t.Parallel()
	repo := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "poolside-review.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	for _, required := range []string{
		"github.event.pull_request.head.repo.full_name == github.repository",
		"github.event.pull_request.draft == false",
		"POOL_INSTALL_ACCEPT_EULA: \"1\"",
		"POOLSIDE_API_KEY: ${{ secrets.POOLSIDE_API_KEY }}",
		"POOLSIDE_STANDALONE_BASE_URL: https://inference.poolside.ai",
		"test -s review.md",
		"gh pr comment \"$PR_NUMBER\" --body-file review.md",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("Poolside review workflow missing %q", required)
		}
	}
	if strings.Contains(workflow, "pull_request_target") {
		t.Error("Poolside review must not expose its secret through pull_request_target")
	}
	if strings.Contains(workflow, "POOLSIDE_STANDALONE_MODEL:") {
		t.Error("Poolside review should use the API key's configured default model unless a tested model ID is selected")
	}
}

func TestCIIsReadOnlyAndRunsProviderFreeChecks(t *testing.T) {
	t.Parallel()
	repo := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	if !strings.Contains(workflow, "permissions:\n  contents: read") {
		t.Error("CI should grant only read access to repository contents")
	}
	for _, check := range []string{"gofmt -l .", "go vet ./...", "go test ./...", "go test -race ./...", "scripts/make-bridge.py", "scripts/count_bridges.py"} {
		if !strings.Contains(workflow, check) {
			t.Errorf("CI missing provider-free check %q", check)
		}
	}
}
