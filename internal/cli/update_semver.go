// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"regexp"
	"strings"
)

// Accept full SemVer 2.0.0 only (with an optional release-tag v prefix).
// Numeric identifiers are compared as strings to avoid integer overflow.
var updateVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type updateVersion struct {
	core []string
	pre  []string
}

func parseUpdateVersion(value string) (updateVersion, bool) {
	match := updateVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return updateVersion{}, false
	}
	v := updateVersion{core: match[1:4]}
	if match[4] != "" {
		v.pre = strings.Split(match[4], ".")
		for _, part := range v.pre {
			if numericVersionPart(part) && len(part) > 1 && part[0] == '0' {
				return updateVersion{}, false
			}
		}
	}
	return v, true
}

func numericVersionPart(part string) bool {
	return strings.Trim(part, "0123456789") == ""
}

func compareVersionNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func compareUpdateVersions(a, b updateVersion) int {
	for i := range a.core {
		if c := compareVersionNumber(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	if len(a.pre) == 0 && len(b.pre) != 0 {
		return 1
	}
	if len(b.pre) == 0 && len(a.pre) != 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		var c int
		switch {
		case numericVersionPart(x) && numericVersionPart(y):
			c = compareVersionNumber(x, y)
		case numericVersionPart(x):
			c = -1
		case numericVersionPart(y):
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}
