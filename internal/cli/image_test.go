// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/spf13/cobra"
)

type fakeImageGenerator struct {
	calls   int
	request imagegen.Request
	result  imagegen.Result
	err     error
}

func (g *fakeImageGenerator) Generate(_ context.Context, request imagegen.Request) (imagegen.Result, error) {
	g.calls++
	g.request = request
	if g.err != nil {
		return imagegen.Result{}, g.err
	}
	return g.result, nil
}

func newFakeImageGenerator(t *testing.T) *fakeImageGenerator {
	t.Helper()
	stagedPath := filepath.Join(t.TempDir(), "staged.png")
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 40), B: 120, A: 255})
		}
	}
	file, err := os.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create fake staged PNG: %v", err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatalf("encode fake staged PNG: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close fake staged PNG: %v", err)
	}
	return &fakeImageGenerator{result: imagegen.Result{Path: stagedPath}}
}

func executeImageCommand(t *testing.T, cmd *cobra.Command, args []string) (*bytes.Buffer, *bytes.Buffer, error) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout, stderr, err
}

func TestImageGeneratePublishesVerifiedPNGWithJSONMetadata(t *testing.T) {
	generator := newFakeImageGenerator(t)
	destination := filepath.Join(t.TempDir(), "bicycle.png")
	cmd := newImageCmd(&rootFlags{asJSON: true}, generator)
	stdout, _, err := executeImageCommand(t, cmd, []string{
		"generate", "A red bicycle beside a blue wall", "--bridge", "Synthetic Image Bridge", "--output", destination,
	})
	if err != nil {
		t.Fatalf("image generate: %v", err)
	}
	if generator.calls != 1 {
		t.Fatalf("generator calls=%d, want 1", generator.calls)
	}
	if generator.request.Prompt != "A red bicycle beside a blue wall" || generator.request.BridgeRef != "Synthetic Image Bridge" {
		t.Fatalf("unexpected request: %+v", generator.request)
	}
	if generator.request.Timeout != imagegen.MaxTimeout {
		t.Fatalf("timeout=%s, want %s", generator.request.Timeout, imagegen.MaxTimeout)
	}
	if _, statErr := os.Lstat(destination); statErr != nil {
		t.Fatalf("published destination: %v", statErr)
	}
	publishedInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("parse JSON: %v (%q)", err, stdout.String())
	}
	if payload["path"] != destination || payload["format"] != "PNG" {
		t.Fatalf("unexpected metadata: %+v", payload)
	}
	if payload["bytes"] != float64(publishedInfo.Size()) || payload["width"] != float64(4) || payload["height"] != float64(3) {
		t.Fatalf("unexpected counts: %+v", payload)
	}
	checksum, ok := payload["checksum"].(string)
	if !ok || len(checksum) != 64 {
		t.Fatalf("unexpected checksum: %+v", payload["checksum"])
	}
}

func TestImageGeneratePreflightRejectsDestinationBeforeGeneratorCall(t *testing.T) {
	generator := newFakeImageGenerator(t)
	dir := t.TempDir()
	destination := filepath.Join(dir, "existing.png")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newImageCmd(&rootFlags{asJSON: true}, generator)
	_, _, err := executeImageCommand(t, cmd, []string{
		"generate", "draw", "--bridge", "bridge", "--output", destination,
	})
	if !errors.Is(err, imagegen.ErrDestinationExists) {
		t.Fatalf("err=%v, want ErrDestinationExists", err)
	}
	if got := ExitCode(err); got != 5 {
		t.Fatalf("exit code=%d, want 5", got)
	}
	if generator.calls != 0 {
		t.Fatalf("generator calls=%d, want 0", generator.calls)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("preflight changed destination: %q", data)
	}
}

func TestImageGenerateRejectsInvalidUsageBeforeGeneratorCall(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing bridge", args: []string{"generate", "draw", "--output", "out.png"}},
		{name: "missing output", args: []string{"generate", "draw", "--bridge", "bridge"}},
		{name: "non-PNG output", args: []string{"generate", "draw", "--bridge", "bridge", "--output", "out.jpg"}},
		{name: "timeout above ceiling", args: []string{"generate", "draw", "--bridge", "bridge", "--output", "out.png", "--timeout", "121s"}},
		{name: "model flag", args: []string{"generate", "draw", "--bridge", "bridge", "--output", "out.png", "--model", "cloud"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := newFakeImageGenerator(t)
			cmd := newImageCmd(&rootFlags{asJSON: true}, generator)
			_, _, err := executeImageCommand(t, cmd, test.args)
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code=%d, err=%v, want 2", got, err)
			}
			if generator.calls != 0 {
				t.Fatalf("generator calls=%d, want 0", generator.calls)
			}
		})
	}
}

func TestImageGenerateHumanOutputDoesNotPrintBinaryData(t *testing.T) {
	generator := newFakeImageGenerator(t)
	destination := filepath.Join(t.TempDir(), "image.png")
	cmd := newImageCmd(&rootFlags{}, generator)
	stdout, _, err := executeImageCommand(t, cmd, []string{
		"generate", "draw", "--bridge", "bridge", "--output", destination,
	})
	if err != nil {
		t.Fatalf("image generate: %v", err)
	}
	if got := stdout.String(); got != "Saved PNG to "+destination+"\n" {
		t.Fatalf("human output=%q", got)
	}
}

func TestImageGenerateRejectsNonPositiveTimeout(t *testing.T) {
	generator := newFakeImageGenerator(t)
	cmd := newImageCmd(&rootFlags{asJSON: true}, generator)
	_, _, err := executeImageCommand(t, cmd, []string{
		"generate", "draw", "--bridge", "bridge", "--output", filepath.Join(t.TempDir(), "out.png"), "--timeout", "0s",
	})
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code=%d, err=%v, want 2", got, err)
	}
	if generator.calls != 0 {
		t.Fatalf("generator calls=%d, want 0", generator.calls)
	}
}

func TestImageGenerateAcceptsMaximumTimeout(t *testing.T) {
	generator := newFakeImageGenerator(t)
	destination := filepath.Join(t.TempDir(), "out.png")
	cmd := newImageCmd(&rootFlags{asJSON: true}, generator)
	_, _, err := executeImageCommand(t, cmd, []string{
		"generate", "draw", "--bridge", "bridge", "--output", destination, "--timeout", "120s",
	})
	if err != nil {
		t.Fatalf("image generate: %v", err)
	}
	if generator.request.Timeout != imagegen.MaxTimeout {
		t.Fatalf("timeout=%s, want %s", generator.request.Timeout, imagegen.MaxTimeout)
	}
}
