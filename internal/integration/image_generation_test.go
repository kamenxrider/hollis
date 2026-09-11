// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/imagegen"
)

const (
	imageAcceptanceHelperEnabled = "HOLLIS_IMAGE_ACCEPTANCE_HELPER"
	imageAcceptanceHelperMode    = "HOLLIS_IMAGE_ACCEPTANCE_MODE"
	imageAcceptanceHelperChild   = "HOLLIS_IMAGE_ACCEPTANCE_CHILD_PID"
)

type imageAcceptanceTrace struct {
	name         string
	args         []string
	calls        int
	childPIDPath string
}

func TestImageGenerationAcceptanceHelperProcess(t *testing.T) {
	if os.Getenv(imageAcceptanceHelperEnabled) != "1" {
		return
	}

	mode := os.Getenv(imageAcceptanceHelperMode)
	outputPath := imageAcceptanceArgument(os.Args, "--output-path")
	if outputPath == "" && mode != "hang" {
		os.Exit(2)
	}

	switch mode {
	case "success":
		if err := writeAcceptancePNG(outputPath, 2, 3); err != nil {
			os.Exit(3)
		}
		_, _ = os.Stdout.Write([]byte{0x89, 'P', 'N', 'G', 0, 0xff})
	case "invalid":
		if err := os.WriteFile(outputPath, []byte("not a PNG"), 0o600); err != nil {
			os.Exit(4)
		}
	case "oversize":
		if err := os.WriteFile(outputPath, bytes.Repeat([]byte{0}, int(imagegen.MaxOutputBytes)+1), 0o600); err != nil {
			os.Exit(5)
		}
	case "pixel-limit":
		if err := writeAcceptancePixelLimitPNG(outputPath); err != nil {
			os.Exit(6)
		}
	case "nonzero":
		_, _ = os.Stderr.WriteString("synthetic image bridge failure\n")
		os.Exit(17)
	case "hang":
		child := exec.Command("/bin/sh", "-c", "sleep 300")
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		if err := child.Start(); err != nil {
			os.Exit(7)
		}
		if path := os.Getenv(imageAcceptanceHelperChild); path != "" {
			_ = os.WriteFile(path, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		}
		done := make(chan struct{})
		go func() {
			time.Sleep(time.Hour)
			close(done)
		}()
		<-done
	default:
		os.Exit(8)
	}
	os.Exit(0)
}

func newImageAcceptanceTransport(t *testing.T, mode string, childPIDPath string) (*imagegen.ShortcutTransport, *imageAcceptanceTrace) {
	t.Helper()
	trace := &imageAcceptanceTrace{childPIDPath: childPIDPath}
	transport := imagegen.New()
	transport.ShortcutsPath = "synthetic-image-shortcuts"
	transport.TempDir = t.TempDir()
	// These checks exercise output validation, not generation latency. Allow
	// the race-instrumented helper to encode the 16-million-pixel fixture on CI.
	// Cancellation tests supply their own shorter request timeout.
	transport.Timeout = 10 * time.Second
	transport.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		trace.calls++
		trace.name = name
		trace.args = append([]string(nil), args...)
		helperArgs := append([]string{"-test.run=TestImageGenerationAcceptanceHelperProcess", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(),
			imageAcceptanceHelperEnabled+"=1",
			imageAcceptanceHelperMode+"="+mode,
		)
		if childPIDPath != "" {
			cmd.Env = append(cmd.Env, imageAcceptanceHelperChild+"="+childPIDPath)
		}
		return cmd
	}
	return transport, trace
}

func TestImageGenerationTransportPublishesVerifiedPNG(t *testing.T) {
	t.Parallel()

	transport, trace := newImageAcceptanceTransport(t, "success", "")
	destination := filepath.Join(t.TempDir(), "bicycle.png")
	if err := imagegen.PreflightDestination(destination); err != nil {
		t.Fatalf("PreflightDestination: %v", err)
	}

	result, err := transport.Generate(context.Background(), imagegen.Request{
		Prompt:    "A red bicycle beside a blue wall",
		BridgeRef: "Synthetic Image Bridge",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if trace.calls != 1 || trace.name == imagegen.DefaultShortcutsPath {
		t.Fatalf("unexpected command: calls=%d name=%q", trace.calls, trace.name)
	}
	if trace.args[0] != "run" || trace.args[1] != "Synthetic Image Bridge" {
		t.Fatalf("unexpected bridge arguments: %#v", trace.args)
	}
	if got := imageAcceptanceArgument(trace.args, "--output-type"); got != "public.png" {
		t.Fatalf("output type=%q, want public.png", got)
	}
	if got := imageAcceptanceArgument(trace.args, "--output-path"); got != result.Path {
		t.Fatalf("output path argument=%q, result path=%q", got, result.Path)
	}

	stageDir := filepath.Dir(result.Path)
	stageInfo, err := os.Stat(stageDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("stage mode=%#o, want 0700", got)
	}
	expectedChecksum := imageAcceptancePNGChecksum(t, 2, 3)
	if result.Bytes <= 0 || result.Width != 2 || result.Height != 3 {
		t.Fatalf("unexpected result dimensions: %+v", result)
	}
	if result.SHA256 != expectedChecksum {
		t.Fatalf("staged checksum=%q, want %q", result.SHA256, expectedChecksum)
	}

	published, err := imagegen.Publish(result.Path, destination)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if published.Path != destination || published.Format != "PNG" {
		t.Fatalf("unexpected publication metadata: %+v", published)
	}
	if published.Bytes != result.Bytes || published.Width != result.Width || published.Height != result.Height {
		t.Fatalf("metadata mismatch: staged=%+v published=%+v", result, published)
	}
	if published.SHA256 != expectedChecksum {
		t.Fatalf("published checksum=%q, want %q", published.SHA256, expectedChecksum)
	}

	fileInfo, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
		t.Fatalf("destination is not a regular file: mode=%#o", fileInfo.Mode())
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("destination mode=%#o, want 0600", got)
	}

	if err := result.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if err := result.Cleanup(); err != nil {
		t.Fatalf("second Cleanup: %v", err)
	}
	if _, err := os.Stat(stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory after cleanup: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(destination) {
		t.Fatalf("publish left unexpected entries: %v", entries)
	}
}

func TestImageGenerationPublishRefusesCollisionAfterGeneration(t *testing.T) {
	t.Parallel()

	transport, _ := newImageAcceptanceTransport(t, "success", "")
	destination := filepath.Join(t.TempDir(), "collision.png")
	if err := imagegen.PreflightDestination(destination); err != nil {
		t.Fatalf("PreflightDestination: %v", err)
	}
	result, err := transport.Generate(context.Background(), imagegen.Request{
		Prompt:    "draw",
		BridgeRef: "Synthetic Image Bridge",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = imagegen.Publish(result.Path, destination)
	if !errors.Is(err, imagegen.ErrDestinationExists) {
		t.Fatalf("Publish collision err=%v, want ErrDestinationExists", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("collision replaced destination with %q", data)
	}
	if err := result.Cleanup(); err != nil {
		t.Fatalf("Cleanup after collision: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(destination) {
		t.Fatalf("collision left unexpected entries: %v", entries)
	}
}

func TestImageGenerationPreflightRunsBeforeFakeGeneration(t *testing.T) {
	t.Parallel()

	generator := &fakeImageAcceptanceGenerator{stagedPath: filepath.Join(t.TempDir(), "staged.png")}
	destination := filepath.Join(t.TempDir(), "existing.png")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := imagegen.PreflightDestination(destination); !errors.Is(err, imagegen.ErrDestinationExists) {
		t.Fatalf("PreflightDestination err=%v, want ErrDestinationExists", err)
	}
	if generator.calls != 0 {
		t.Fatalf("generator calls=%d, want 0", generator.calls)
	}
}

func TestImageGenerationTransportRejectsInvalidPNGNonzeroExitAndLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode string
		want error
	}{
		{name: "invalid PNG", mode: "invalid", want: imagegen.ErrInvalidPNG},
		{name: "byte limit", mode: "oversize", want: imagegen.ErrImageTooLarge},
		{name: "pixel limit", mode: "pixel-limit", want: imagegen.ErrImageTooLarge},
		{name: "nonzero exit", mode: "nonzero", want: imagegen.ErrNonZeroExit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport, _ := newImageAcceptanceTransport(t, test.mode, "")
			destination := filepath.Join(t.TempDir(), "output.png")
			if err := imagegen.PreflightDestination(destination); err != nil {
				t.Fatalf("PreflightDestination: %v", err)
			}
			_, err := transport.Generate(context.Background(), imagegen.Request{
				Prompt:    "draw",
				BridgeRef: "Synthetic Image Bridge",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("Generate err=%v, want errors.Is(..., %v)", err, test.want)
			}
			if test.mode == "nonzero" && !strings.Contains(err.Error(), "synthetic image bridge failure") {
				t.Fatalf("error omitted bounded stderr: %v", err)
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed generation created destination: %v", err)
			}
			entries, err := os.ReadDir(transport.TempDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed generation left staging entries: %v", entries)
			}
		})
	}
}

func TestImageGenerationTransportRejectsTimeoutAboveCeiling(t *testing.T) {
	t.Parallel()

	transport, trace := newImageAcceptanceTransport(t, "success", "")
	destination := filepath.Join(t.TempDir(), "too-late.png")
	if err := imagegen.PreflightDestination(destination); err != nil {
		t.Fatalf("PreflightDestination: %v", err)
	}
	_, err := transport.Generate(context.Background(), imagegen.Request{
		Prompt:    "draw",
		BridgeRef: "Synthetic Image Bridge",
		Timeout:   imagegen.MaxTimeout + time.Nanosecond,
	})
	if !errors.Is(err, imagegen.ErrTimeoutTooLarge) {
		t.Fatalf("Generate err=%v, want ErrTimeoutTooLarge", err)
	}
	if trace.calls != 0 {
		t.Fatalf("over-ceiling request spawned helper %d times", trace.calls)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected request created destination: %v", err)
	}
}

func TestImageGenerationTransportCancelsOwnedProcessGroupAndCleans(t *testing.T) {
	t.Parallel()

	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	transport, _ := newImageAcceptanceTransport(t, "hang", childPIDPath)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var generationErr error
	go func() {
		_, generationErr = transport.Generate(ctx, imagegen.Request{
			Prompt:    "draw",
			BridgeRef: "Synthetic Image Bridge",
			Timeout:   15 * time.Second,
		})
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("canceled generation did not stop")
		}
	}()

	// Exercise cancellation of a running process group. A fixed timer can
	// cancel a race-instrumented helper before it even starts its child on CI.
	childPID := waitForImageAcceptancePID(t, childPIDPath)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled generation did not return")
	}
	if !errors.Is(generationErr, imagegen.ErrCanceled) {
		t.Fatalf("Generate err=%v, want ErrCanceled", generationErr)
	}
	waitForImageAcceptanceProcessExit(t, childPID)
	entries, err := os.ReadDir(transport.TempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("canceled generation left staging entries: %v", entries)
	}
}

type fakeImageAcceptanceGenerator struct {
	calls      int
	stagedPath string
}

func (g *fakeImageAcceptanceGenerator) Generate(_ context.Context, _ imagegen.Request) (imagegen.Result, error) {
	g.calls++
	return imagegen.Result{}, nil
}

func writeAcceptancePNG(path string, width, height int) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 90), G: uint8(y * 70), B: 160, A: 255})
		}
	}
	return png.Encode(file, img)
}

func writeAcceptancePixelLimitPNG(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	img := image.NewPaletted(image.Rect(0, 0, 4001, 4001), color.Palette{color.Gray{Y: 72}})
	return png.Encode(file, img)
}

func imageAcceptancePNGChecksum(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 90), G: uint8(y * 70), B: 160, A: 255})
		}
	}
	var contents bytes.Buffer
	if err := png.Encode(&contents, img); err != nil {
		t.Fatalf("encode expected PNG: %v", err)
	}
	sum := sha256.Sum256(contents.Bytes())
	return hex.EncodeToString(sum[:])
}

func imageAcceptanceArgument(args []string, name string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name {
			return args[index+1]
		}
	}
	return ""
}

func waitForImageAcceptancePID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("helper never recorded child PID at %s", path)
	return 0
}

func waitForImageAcceptanceProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("synthetic child process %d survived owned process-group cancellation", pid)
}
