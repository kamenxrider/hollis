// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
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
		"--notes-file",
		"docs/releases/$GITHUB_REF_NAME.md",
		"python3 scripts/package-bridges.py dist",
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

func TestReadmeQuickstartVerifiesAllArtifactsBeforeUse(t *testing.T) {
	t.Parallel()
	repo := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	start := strings.Index(readme, "## Quickstart")
	end := strings.Index(readme, "### Other install routes")
	if start < 0 || end <= start {
		t.Fatal("README quickstart boundaries are missing")
	}
	quickstart := readme[start:end]
	for _, required := range []string{
		`set -euo pipefail`,
		`HOLLIS_INSTALL_DIR="$(mktemp -d)"`,
		`chmod 700 "$HOLLIS_INSTALL_DIR"`,
		`HOLLIS_VERSION="$(gh release view --repo kamenxrider/hollis --json tagName --jq .tagName)"`,
		`HOLLIS_RELEASE_URL="https://github.com/kamenxrider/hollis/releases/download/$HOLLIS_VERSION"`,
		`curl -fsSL -o "$HOLLIS_ASSET"`,
		`curl -fsSL -o hollis-bridges.zip`,
		`$2 == asset`,
		`$2 == "hollis-bridges.zip"`,
		`shasum -a 256 -c SELECTED_SHA256SUMS`,
		`gh attestation verify "$HOLLIS_ASSET" --repo kamenxrider/hollis`,
		`gh attestation verify hollis-bridges.zip --repo kamenxrider/hollis`,
	} {
		if !strings.Contains(quickstart, required) {
			t.Errorf("README quickstart is missing %q", required)
		}
	}
	if strings.Contains(quickstart, "--ignore-missing") {
		t.Fatal("README quickstart permits missing checksum targets")
	}
	if strings.Contains(quickstart, "releases/latest") {
		t.Fatal("README quickstart resolves release assets independently through latest URLs")
	}
	verifyAt := strings.Index(quickstart, `gh attestation verify hollis-bridges.zip`)
	for _, use := range []string{"chmod +x", "sudo mv", "unzip hollis-bridges.zip", "shortcuts sign", "open \"$HOLLIS_SIGNED\""} {
		if useAt := strings.Index(quickstart, use); verifyAt < 0 || useAt < verifyAt {
			t.Errorf("README uses release artifact via %q before checksum and provenance verification", use)
		}
	}
}

func TestQuickstartChecksumFailureStopsBeforeArtifactUse(t *testing.T) {
	binary := []byte("binary")
	archive := []byte("verified archive")
	binaryHash := sha256.Sum256(binary)
	archiveHash := sha256.Sum256(archive)
	for _, test := range []struct {
		name      string
		checksums string
	}{
		{
			name:      "missing archive entry",
			checksums: fmt.Sprintf("%x  hollis-darwin-arm64\n", binaryHash),
		},
		{
			name:      "mismatched archive",
			checksums: fmt.Sprintf("%x  hollis-darwin-arm64\n%x  hollis-bridges.zip\n", binaryHash, sha256.Sum256([]byte("other archive"))),
		},
		{
			name:      "control",
			checksums: fmt.Sprintf("%x  hollis-darwin-arm64\n%x  hollis-bridges.zip\n", binaryHash, archiveHash),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string][]byte{
				"hollis-darwin-arm64": binary,
				"hollis-bridges.zip":  archive,
				"SHA256SUMS":          []byte(test.checksums),
			} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("/bin/zsh", "-c", `
set -euo pipefail
asset=hollis-darwin-arm64
awk -v asset="$asset" '
  $2 == asset { binary++ }
  $2 == "hollis-bridges.zip" { bridges++ }
  $2 == asset || $2 == "hollis-bridges.zip" { print }
  END { if (binary != 1 || bridges != 1) exit 1 }
' SHA256SUMS > SELECTED_SHA256SUMS
shasum -a 256 -c SELECTED_SHA256SUMS
touch artifact-used
`)
			command.Dir = dir
			err := command.Run()
			_, usedErr := os.Stat(filepath.Join(dir, "artifact-used"))
			if test.name == "control" {
				if err != nil || usedErr != nil {
					t.Fatalf("valid control err=%v marker=%v", err, usedErr)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid checksum set reached artifact use")
			}
			if !os.IsNotExist(usedErr) {
				t.Fatalf("invalid checksum set created use marker: %v", usedErr)
			}
		})
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
		"pull_request_target:",
		"github.event.pull_request.base.ref == github.event.repository.default_branch",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"github.event.pull_request.draft == false",
		"ref: ${{ github.event.pull_request.base.sha }}",
		"persist-credentials: false",
		"path: trusted-source",
		"POOLSIDE_API_KEY: ${{ secrets.POOLSIDE_API_KEY }}",
		"trusted-source/scripts/poolside-review/poolside_review.py fetch-diff",
		"trusted-source/scripts/poolside-review/poolside_review.py create-review",
		"actions/upload-artifact@",
		"needs: review",
		"issues: write",
		`test ! -L "$REVIEW_ARTIFACT_DIR/review.json"`,
		`(.review_markdown | utf8bytelength > 0 and utf8bytelength <= 60000)`,
		`/usr/bin/gh api --silent --method POST`,
		`--input "$REVIEW_REQUEST"`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("Poolside review workflow missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"pool install",
		"POOL_INSTALL_ACCEPT_EULA",
		"ref: ${{ github.event.pull_request.head.sha }}",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("Poolside review workflow contains unsafe behavior %q", forbidden)
		}
	}
	postAt := strings.Index(workflow, "\n  post:")
	if postAt < 0 {
		t.Fatal("Poolside workflow has no isolated comment-posting job")
	}
	if strings.Contains(workflow[postAt:], "POOLSIDE_API_KEY") {
		t.Fatal("Poolside API key crossed into the write-capable comment job")
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
