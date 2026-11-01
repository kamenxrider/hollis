// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A listing that floods the capture must fail as a listing failure. Truncated
// bytes are a prefix of an unknown listing: turning them into names would
// invent bridges, and reporting success would hide that the listing did not
// complete.
func TestListShortcutsRejectsFloodedCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "list-shortcuts")
	script := "#!/bin/sh\n" +
		"head -c 2000000 /dev/zero | tr '\\0' 'n'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	names, err := (&ShortcutRunner{ShortcutsPath: path}).ListShortcuts(ctx)
	elapsed := time.Since(start)
	if err == nil || names != nil {
		t.Fatalf("flooded listing returned names=%d err=%v, want a listing failure", len(names), err)
	}
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a typed error", err)
	}
	if re.Kind != KindListFailure {
		t.Errorf("kind = %v, want KindListFailure", re.Kind)
	}
	if re.ListTimeout {
		t.Error("ListTimeout = true for a capture overflow; the generic timeout hint would mislead")
	}
	if !strings.Contains(err.Error(), "no bridge is proven missing") {
		t.Errorf("message makes an absence claim or is unrecognizable: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("flooded listing did not end early: elapsed=%v", elapsed)
	}
}
