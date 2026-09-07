// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailureContractSubprocess(t *testing.T) {
	for _, tc := range []struct{ name, diagnostic, status, want string }{
		{"recorded-decline", "Error: Try describing something different to create an image.", "1", "request_declined"},
		{"usage-exit-keeps-contract", "Error: Try describing something different to create an image.", "64", "usage"},
		{"unknown", "private-path /Users/private/token=CANARY", "1", "shortcut_failed"},
		{"lookalike", "Error: Try describing something different to create an image. Additional failure", "1", "shortcut_failed"},
		{"quoted", "User wrote: Try describing something different to create an image.", "1", "shortcut_failed"},
		{"empty", "", "0", "shortcut_no_output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, dir := runnerWithFake(t, "echo")
			// Controlled fixture strings contain no single quotes.
			script := "#!/bin/sh\ncat >/dev/null\nprintf 'call\\n' >> '" + dir + "/calls'\nprintf '%s\\n' '" + tc.diagnostic + "' >&2\nexit " + tc.status + "\n"
			if err := os.WriteFile(r.ShortcutsPath, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			_, used, fallback, err := r.RunWithFallback(context.Background(), ModelAuto, "fixture")
			var classified *Error
			if !errors.As(err, &classified) || string(classified.Kind) != tc.want {
				t.Errorf("kind=%v, want %s", err, tc.want)
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			if strings.Count(string(calls), "call") != 1 || fallback.Used || used != ModelCloud {
				t.Errorf("calls=%q fallback=%+v used=%s", calls, fallback, used)
			}
		})
	}
}

func TestSuccessfulAnswerContainingDeclineIsUnchanged(t *testing.T) {
	r, _ := runnerWithFake(t, "echo")
	prompt := "Error: Try describing something different to create an image."
	got, used, err := r.Run(context.Background(), ModelAuto, prompt)
	if err != nil || got != prompt || used != ModelCloud {
		t.Fatalf("got=%q used=%s err=%v", got, used, err)
	}
}
