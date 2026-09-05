// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/imagegen"
)

type signalImageGenerator struct{ ready string }

func (g signalImageGenerator) Generate(ctx context.Context, _ imagegen.Request) (imagegen.Result, error) {
	if err := os.WriteFile(g.ready, []byte("ready"), 0o600); err != nil {
		return imagegen.Result{}, err
	}
	<-ctx.Done()
	return imagegen.Result{}, &imagegen.Error{Kind: imagegen.KindCanceled, Err: imagegen.ErrCanceled}
}

func TestImageRootSignalHelper(t *testing.T) {
	if os.Getenv("HOLLIS_IMAGE_SIGNAL_HELPER") != "1" {
		return
	}
	cmd, _ := newRootCmdWithImageGenerator(nil, signalImageGenerator{ready: os.Getenv("HOLLIS_IMAGE_SIGNAL_READY")})
	cmd.SetArgs([]string{"image", "generate", "fixture", "--bridge", "fixture", "--output", os.Getenv("HOLLIS_IMAGE_SIGNAL_OUTPUT")})
	err := cmd.Execute()
	if errors.Is(err, imagegen.ErrCanceled) {
		os.Exit(5)
	}
	os.Exit(1)
}

func TestImageRootSIGTERMCancelsGenerator(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	output := filepath.Join(dir, "out.png")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestImageRootSignalHelper$")
	cmd.Env = append(os.Environ(), "HOLLIS_IMAGE_SIGNAL_HELPER=1", "HOLLIS_IMAGE_SIGNAL_READY="+ready, "HOLLIS_IMAGE_SIGNAL_OUTPUT="+output)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("helper exited before ready: %v", err)
		case <-ctx.Done():
			t.Fatal("helper did not become ready")
		case <-ticker.C:
		}
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 5 {
			t.Fatalf("signal was not handled as cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("generator did not cancel")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("cancellation created output: %v", err)
	}
}
