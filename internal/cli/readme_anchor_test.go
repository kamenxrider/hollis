// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var readmeLinkPattern = regexp.MustCompile(`README\.md#([a-z0-9-]+)`)

// readmeAnchors returns every explicit anchor id and every GitHub heading
// slug in the repository README.
func readmeAnchors(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]bool{}
	for _, m := range regexp.MustCompile(`<a id="([^"]+)"></a>`).FindAllStringSubmatch(string(raw), -1) {
		anchors[m[1]] = true
	}
	inFence := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
		var slug strings.Builder
		for _, r := range strings.ToLower(heading) {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
				slug.WriteRune(r)
			case r == ' ':
				slug.WriteRune('-')
			}
		}
		anchors[slug.String()] = true
	}
	return anchors
}

// CLI guidance that sends readers to the README must name a section that
// exists, so a README restructure cannot silently strand the remedy.
func TestCLIReadmeReferencesResolve(t *testing.T) {
	anchors := readmeAnchors(t)
	for name, text := range map[string]string{
		"missingBridgeRemedy":  missingBridgeRemedy,
		"modelsBackgroundNote": modelsBackgroundNote,
	} {
		links := readmeLinkPattern.FindAllStringSubmatch(text, -1)
		if len(links) == 0 {
			t.Errorf("%s names no README anchor: %q", name, text)
		}
		for _, link := range links {
			if !anchors[link[1]] {
				t.Errorf("%s points at README.md#%s, which README.md does not define", name, link[1])
			}
		}
	}
}
