// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type completionRunner struct{ calls int }

func (r *completionRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	return prompt, model, nil
}

func TestShellCompletionsGenerateWithoutRunnerOrConfigAccess(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("HOLLIS_STATE_DIR", stateDir)
	configPathCalls := 0
	previousConfigPath := configPath
	configPath = func() (string, error) {
		configPathCalls++
		return filepath.Join(stateDir, "config.json"), nil
	}
	t.Cleanup(func() { configPath = previousConfigPath })

	spy := &completionRunner{}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			cmd := NewRootCmd(func() runner.Runner { return spy })
			cmd.SetArgs([]string{"completion", shell})
			var stderr strings.Builder
			cmd.SetErr(&stderr)
			capture, err := os.CreateTemp(t.TempDir(), "completion-")
			if err != nil {
				t.Fatal(err)
			}
			defer capture.Close()
			previousStdout, err := syscall.Dup(1)
			if err != nil {
				t.Fatal(err)
			}
			defer syscall.Close(previousStdout)
			defer syscall.Dup2(previousStdout, 1)
			if redirectErr := syscall.Dup2(int(capture.Fd()), 1); redirectErr != nil {
				t.Fatal(redirectErr)
			}
			execErr := cmd.Execute()
			if restoreErr := syscall.Dup2(previousStdout, 1); restoreErr != nil {
				t.Fatal(restoreErr)
			}
			if _, err := capture.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			out, readErr := io.ReadAll(capture)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if execErr != nil {
				t.Fatalf("completion %s: %v (%s)", shell, execErr, stderr.String())
			}
			if len(out) == 0 || !strings.Contains(string(out), "hollis") {
				t.Fatalf("completion %s output is empty or missing command name: %q", shell, out)
			}
			if stderr.Len() != 0 {
				t.Fatalf("completion %s wrote unexpected stderr: %q", shell, stderr.String())
			}
		})
	}
	if spy.calls != 0 {
		t.Fatalf("shell completion invoked runner %d times", spy.calls)
	}
	if configPathCalls != 0 {
		t.Fatalf("shell completion accessed config path %d times", configPathCalls)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("shell completion changed state directory: stat error %v", err)
	}
}
