// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/shortcutdiagnostic"
)

func TestImageBridgeProcess(t *testing.T) {
	if os.Getenv("HOLLIS_IMAGE_BRIDGE_FIXTURE") != "1" {
		return
	}
	var input, output string
	for i, arg := range os.Args {
		if i+1 < len(os.Args) {
			switch arg {
			case "--input-path":
				input = os.Args[i+1]
			case "--output-path":
				output = os.Args[i+1]
			}
		}
	}
	data, err := os.ReadFile(input)
	if err != nil || os.WriteFile(os.Getenv("HOLLIS_IMAGE_BRIDGE_CAPTURE"), data, 0o600) != nil {
		os.Exit(2)
	}
	if text := os.Getenv("HOLLIS_IMAGE_BRIDGE_ERROR"); text != "" {
		_, _ = os.Stderr.WriteString(text)
		os.Exit(1)
	}
	f, err := os.Create(output)
	if err != nil || png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2, 2))) != nil || f.Close() != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func fakeImageBridgeProcess(t *testing.T, diagnostic string) (*imagegen.ShortcutTransport, string, *int) {
	t.Helper()
	capture := filepath.Join(t.TempDir(), "input")
	calls := new(int)
	g := imagegen.New()
	g.TempDir = t.TempDir()
	g.Command = func(_ context.Context, _ string, args ...string) *exec.Cmd {
		*calls++
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestImageBridgeProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "HOLLIS_IMAGE_BRIDGE_FIXTURE=1",
			"HOLLIS_IMAGE_BRIDGE_CAPTURE="+capture, "HOLLIS_IMAGE_BRIDGE_ERROR="+diagnostic)
		return cmd
	}
	return g, capture, calls
}

func TestExplicitBundledImageBridgeJSONPayload(t *testing.T) {
	stubConfigPath(t)
	if err := saveConfig(config{ImageBridge: imageSetupBridge}); err != nil {
		t.Fatal(err)
	}
	for _, bridge := range []string{imageSetupBridge, "", imageSetupBridge + " copy"} {
		g, capture, calls := fakeImageBridgeProcess(t, "")
		cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return &fakeRunner{} }, g)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		args := []string{"image", "generate", "fixture", "--output", filepath.Join(t.TempDir(), "image.png")}
		if bridge != "" {
			args = append(args, "--bridge", bridge)
		}
		cmd.SetArgs(args)
		if err := executeCommand(cmd, flags); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(capture)
		want := `{"prompt":"fixture","style":"Animation"}`
		if bridge == imageSetupBridge+" copy" {
			want = "fixture"
		}
		if err != nil || string(raw) != want || *calls != 1 {
			t.Fatalf("bridge=%q input=%q calls=%d err=%v", bridge, raw, *calls, err)
		}
	}
}

func TestExplicitImageBridgeFailureHint(t *testing.T) {
	stubConfigPath(t)
	if err := saveConfig(config{ImageBridge: imageSetupBridge}); err != nil {
		t.Fatal(err)
	}
	const generic = "Error: The Shortcut execution failed for an unrecognized reason.\n"
	for _, tc := range []struct {
		bridge, diagnostic, code string
		hint                     bool
	}{
		{"Custom JSON", generic, "shortcut_failed", true},
		{imageSetupBridge, generic, "shortcut_failed", false},
		{"", generic, "shortcut_failed", false},
		{"Custom JSON", "Error: Try describing something different to create an image.\n", "request_declined", false},
		{"Custom JSON", "Error: This shortcut requires your Mac to be unlocked.", "transport", false},
		{"Custom JSON", "Error: Try describing something different to create an image. EXTRA", "shortcut_failed", true},
	} {
		for _, mode := range []string{"human", "json", "agent"} {
			g, _, calls := fakeImageBridgeProcess(t, tc.diagnostic)
			cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return &fakeRunner{} }, g)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			args := []string{"image", "generate", "fixture", "--output", filepath.Join(t.TempDir(), "image.png")}
			if tc.bridge != "" {
				args = append(args, "--bridge", tc.bridge)
			}
			if mode != "human" {
				args = append(args, "--"+mode)
			}
			cmd.SetArgs(args)
			err := executeCommand(cmd, flags)
			if err == nil || *calls != 1 || errorCode(err) != tc.code {
				t.Fatalf("%+v mode=%s calls=%d err=%v code=%s", tc, mode, *calls, err, errorCode(err))
			}
			fields := commandErrorFields(err)
			const wantHint = `If this is a Hollis JSON image Shortcut (such as "Hollis Image - Reference Input v2"), run: hollis config set image-bridge "Custom JSON" and use --style <style>.`
			if tc.hint {
				wantMessage := shortcutdiagnostic.FailedMessage
				if mode == "human" {
					wantMessage += "\n" + wantHint
				}
				if fields["suggestion"] != wantHint || err.Error() != wantMessage || ExitCode(err) != 5 {
					t.Fatalf("hint/error changed: %+v err=%v", fields, err)
				}
			} else if fields["suggestion"] != nil || strings.Contains(err.Error(), "If this is") {
				t.Fatalf("unexpected hint: %+v", fields)
			}
			if mode != "human" {
				var body struct{ Error map[string]any }
				if json.Unmarshal(out.Bytes(), &body) != nil || body.Error["code"] != tc.code ||
					body.Error["suggestion"] != fields["suggestion"] {
					t.Fatalf("JSON hint mismatch: %s", &out)
				}
			}
		}
	}
}
