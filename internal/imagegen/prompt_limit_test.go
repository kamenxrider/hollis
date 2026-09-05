// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestOversizedPromptDoesNotSpawn(t *testing.T) {
	g := New()
	g.TempDir = t.TempDir()
	g.Command = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("oversized prompt reached a process")
		return nil
	}
	_, err := g.Generate(t.Context(), Request{Prompt: strings.Repeat("x", MaxPromptBytes+1), BridgeRef: "fixture"})
	if err == nil || !strings.Contains(err.Error(), "128 KiB") {
		t.Fatalf("error = %v", err)
	}
}
