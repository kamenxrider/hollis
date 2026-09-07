// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package imagegen

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestImageDiagnosticHelper(t *testing.T) {
	if os.Getenv("HOLLIS_DIAGNOSTIC_HELPER") != "1" {
		return
	}
	_, _ = os.Stderr.WriteString(os.Getenv("HOLLIS_DIAGNOSTIC_TEXT"))
	if os.Getenv("HOLLIS_DIAGNOSTIC_SUCCESS") == "1" {
		if err := writeHelperPNG(argumentValue("--output-path")); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(1)
}

func TestImageFailureContractSubprocess(t *testing.T) {
	for _, tc := range []struct {
		text    string
		success bool
		want    ErrorKind
	}{
		{"Error: Try describing something different to create an image.\n", false, KindRequestDeclined},
		{"Error: Try describing something different to create an image. EXTRA", false, KindNonZeroExit},
		{"User said: Try describing something different to create an image.", false, KindNonZeroExit},
		{"Error: an unrelated failure", false, KindNonZeroExit},
		{"Error: Try describing something different to create an image.\n", true, ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			calls := 0
			g := New()
			g.TempDir = t.TempDir()
			g.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
				calls++
				cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestImageDiagnosticHelper$", "--"}, args...)...)
				cmd.Env = append(os.Environ(), "HOLLIS_DIAGNOSTIC_HELPER=1", "HOLLIS_DIAGNOSTIC_TEXT="+tc.text)
				if tc.success {
					cmd.Env = append(cmd.Env, "HOLLIS_DIAGNOSTIC_SUCCESS=1")
				}
				return cmd
			}
			result, err := g.Generate(context.Background(), Request{Prompt: "fixture", BridgeRef: "fixture", Style: "illustration"})
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
				_ = result.Cleanup()
			} else {
				var classified *Error
				if !errors.As(err, &classified) || classified.Kind != tc.want {
					t.Fatalf("got %v want %s", err, tc.want)
				}
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
