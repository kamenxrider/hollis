// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fallbackPath is used when the parent has no PATH, so a bare command name
// still resolves to the usual system and package-manager locations.
const fallbackPath = "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

var inheritedNames = []string{"PATH", "HOME", "TMPDIR", "LANG", "VIRTUAL_ENV"}

var inheritedPrefixes = []string{"LC_", "PYTHON"}

// ChildEnv builds a stdio server's environment from scratch. The parent's
// environment contributes only the allowlisted names and prefixes; then the
// server's literal env; then env_from values, which override both. Any
// credential-like name is dropped last, whatever its source, so no
// configuration can re-enable it.
func ChildEnv(parent []string, server string, s Server) ([]string, error) {
	values := map[string]string{}
	parentValues := map[string]string{}
	for _, entry := range parent {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		parentValues[name] = value
		if inherited(name) {
			values[name] = value
		}
	}
	for name, value := range s.Env {
		values[name] = value
	}
	for _, child := range sortedKeys(s.EnvFrom) {
		parentName := s.EnvFrom[child]
		value, ok := parentValues[parentName]
		if !ok {
			return nil, newError(CodeConfig, server, "", "env_from %s needs %s, which is not set in this environment", child, parentName)
		}
		values[child] = value
	}
	for name := range values {
		if secretLikeName(name) {
			delete(values, name)
		}
	}
	if strings.TrimSpace(values["PATH"]) == "" {
		values["PATH"] = fallbackPath
	}
	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	sort.Strings(env)
	return env, nil
}

func inherited(name string) bool {
	for _, allowed := range inheritedNames {
		if name == allowed {
			return true
		}
	}
	for _, prefix := range inheritedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// resolveCommand finds a bare program name on the child's PATH, not on
// Hollis's own, so the program that runs is the one the scrubbed environment
// would find.
func resolveCommand(command string, env []string) (string, error) {
	if strings.ContainsRune(command, '/') {
		return command, checkExecutable(command)
	}
	path := fallbackPath
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			path = value
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, command)
		if checkExecutable(candidate) == nil {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return os.ErrPermission
	}
	if info.Mode().Perm()&0o111 == 0 {
		return os.ErrPermission
	}
	return nil
}
