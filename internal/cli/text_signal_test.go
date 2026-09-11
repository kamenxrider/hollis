// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func TestTextRootSignalHelper(t *testing.T) {
	if os.Getenv("HOLLIS_TEXT_SIGNAL_HELPER") != "1" {
		return
	}
	stubResolution(t, allImportedNames(), true, 27)
	r := runner.New()
	r.ShortcutsPath = os.Getenv("HOLLIS_TEXT_SIGNAL_SHORTCUTS")
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
	cmd.SetArgs([]string{os.Getenv("HOLLIS_TEXT_SIGNAL_COMMAND"), "synthetic private text\n    quoted \"café\"", "--model", "cloud", "--agent"})
	if os.Getenv("HOLLIS_TEXT_SIGNAL_COMMAND") == "batch" {
		dir := os.Getenv("TMPDIR")
		input := filepath.Join(dir, "input")
		if err := os.Mkdir(input, 0700); err != nil {
			t.Fatal(err)
		}
		instruction := filepath.Join(dir, "instruction.txt")
		for path, content := range map[string]string{instruction: "Summarize", filepath.Join(input, "note.txt"): "synthetic source"} {
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
		job := filepath.Join(dir, "job.json")
		plan, _ := newRootCmdWithFlags(func() runner.Runner { return r })
		plan.SetArgs([]string{"batch", "plan", "--input-dir", input, "--prompt-file", instruction, "--model", "cloud", "--output-dir", filepath.Join(dir, "results"), "--job", job, "--json"})
		plan.SetOut(io.Discard)
		if err := plan.Execute(); err != nil {
			t.Fatal(err)
		}
		cmd.SetArgs([]string{"batch", "run", "--job", job, "--max-calls", "1", "--agent"})
	}
	cmd.SetIn(strings.NewReader(""))
	if mode := os.Getenv("HOLLIS_TEXT_SIGNAL_READ_MODE"); mode != "" {
		interactiveStdin = func() bool { return mode == "interactive" }
		cmd.SetArgs([]string{os.Getenv("HOLLIS_TEXT_SIGNAL_COMMAND"), "--model", "cloud"})
		cmd.SetIn(signalReadyReader{ready: os.Getenv("HOLLIS_TEXT_SIGNAL_READY")})
	}
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	os.Exit(ExitCode(executeCommand(cmd, flags)))
}

// Signal the actual CLI execution process, not a context created by this test.
// The child stands in for Shortcuts, records its separate process group and
// private prompt file, then remains running until the runner cancels it.
func TestTextRootSignalsCleanTransport(t *testing.T) {
	for _, command := range []string{"respond", "chat", "batch"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(command+"/"+sig.String(), func(t *testing.T) {
				dir := t.TempDir()
				ready := filepath.Join(dir, "ready")
				shortcut := filepath.Join(dir, "shortcuts")
				script := `#!/bin/sh
set -eu
[ "$5" = --input-path ] && [ -f "$6" ]
printf '%s\n%s\n' "$$" "$6" > "$HOLLIS_TEXT_SIGNAL_READY"
exec /bin/sleep 300
`
				if err := os.WriteFile(shortcut, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTextRootSignalHelper$")
				cmd.Env = append(os.Environ(), "HOLLIS_TEXT_SIGNAL_HELPER=1", "HOLLIS_TEXT_SIGNAL_COMMAND="+command, "HOLLIS_TEXT_SIGNAL_SHORTCUTS="+shortcut, "HOLLIS_TEXT_SIGNAL_READY="+ready, "HOLLIS_STATE_DIR="+filepath.Join(dir, "state"), "TMPDIR="+dir)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				// Prevent terminal/job-control signals from accidentally reaching the test.
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				childPID := 0
				t.Cleanup(func() {
					_ = cmd.Process.Kill()
					if childPID > 0 {
						_ = syscall.Kill(-childPID, syscall.SIGKILL)
					}
				})
				var promptPath string
				for {
					raw, err := os.ReadFile(ready)
					fields := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
					if err == nil && len(fields) == 2 {
						childPID, err = strconv.Atoi(fields[0])
						if err == nil && childPID > 0 {
							promptPath = fields[1]
							break
						}
					}
					select {
					case err := <-done:
						t.Fatalf("CLI ended before child ready: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					case <-ctx.Done():
						t.Fatal("CLI did not become ready")
					case <-time.After(10 * time.Millisecond):
					}
				}
				if _, err := os.Stat(promptPath); err != nil {
					t.Fatalf("prompt was not staged: %v", err)
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 5 {
						t.Errorf("wanted handled cancellation exit 5; got %v", err)
					}
				case <-ctx.Done():
					t.Fatal("CLI failed to finish signal cancellation")
				}
				if _, err := os.Stat(promptPath); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("private prompt remains after signal: %v", err)
				}
				// The direct child is reaped before the CLI returns, so its own process
				// group must no longer exist, not merely have received a kill request.
				if err := syscall.Kill(-childPID, 0); !errors.Is(err, syscall.ESRCH) {
					t.Errorf("Shortcuts process group remains after cancellation: %v", err)
				}
				var envelope struct {
					Error struct {
						Code     string `json:"code"`
						ExitCode int    `json:"exit_code"`
					} `json:"error"`
					Meta struct {
						SchemaVersion string `json:"schema_version"`
					} `json:"meta"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Errorf("missing cancellation envelope: %v stdout=%q", err, stdout.String())
				}
				if envelope.Error.Code != "transport" || envelope.Error.ExitCode != 5 || envelope.Meta.SchemaVersion != "2" {
					t.Errorf("changed cancellation contract: %s", stdout.String())
				}
				if stderr.Len() != 0 {
					t.Errorf("structured cancellation leaked stderr: %s", stderr.String())
				}
			})
		}
	}
}

func TestExecuteCommandPreservesCallerContext(t *testing.T) {
	type contextKey struct{}
	parent, cancel := context.WithTimeout(context.WithValue(t.Context(), contextKey{}, "caller"), time.Minute)
	defer cancel()
	expectedDeadline, _ := parent.Deadline()
	cmd := &cobra.Command{RunE: func(cmd *cobra.Command, _ []string) error {
		if cmd.Context().Value(contextKey{}) != "caller" {
			t.Error("lost caller context value")
		}
		deadline, has := cmd.Context().Deadline()
		if !has || !deadline.Equal(expectedDeadline) {
			t.Error("changed caller deadline")
		}
		if cmd.Context().Err() != parent.Err() {
			t.Error("lost caller cancellation")
		}
		return nil
	}}
	cmd.SetArgs([]string{})
	cmd.SetContext(parent)
	for range 2 {
		if err := executeCommand(cmd, &rootFlags{}); err != nil {
			t.Fatal(err)
		}
		if cmd.Context() != parent {
			t.Fatal("command retained stopped signal context")
		}
	}
	cancel()
	if err := executeCommand(cmd, &rootFlags{}); err != nil {
		t.Fatal(err)
	}
}

// The marker is written only once the command actually starts reading input.
type signalReadyReader struct{ ready string }

func (r signalReadyReader) Read(p []byte) (int, error) {
	if err := os.WriteFile(r.ready, []byte("reading"), 0600); err != nil {
		return 0, err
	}
	return os.Stdin.Read(p)
}

func TestTextRootIdleInputKeepsSignalExit(t *testing.T) {
	for _, mode := range []string{"pipe", "interactive"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(mode+"/"+sig.String(), func(t *testing.T) {
				dir := t.TempDir()
				ready := filepath.Join(dir, "ready")
				command := "respond"
				if mode == "interactive" {
					command = "chat"
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTextRootSignalHelper$")
				cmd.Env = append(os.Environ(), "HOLLIS_TEXT_SIGNAL_HELPER=1", "HOLLIS_TEXT_SIGNAL_COMMAND="+command, "HOLLIS_TEXT_SIGNAL_READ_MODE="+mode, "HOLLIS_TEXT_SIGNAL_READY="+ready, "HOLLIS_STATE_DIR="+filepath.Join(dir, "state"), "TMPDIR="+dir)
				input, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer cmd.Process.Kill()
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					select {
					case err := <-done:
						t.Fatalf("command exited before input read: %v stderr=%s", err, stderr.String())
					case <-ctx.Done():
						t.Fatal("command did not read input")
					case <-time.After(10 * time.Millisecond):
					}
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					var exit *exec.ExitError
					if !errors.As(err, &exit) {
						t.Fatalf("wanted normal signal exit: %v", err)
					}
					status, ok := exit.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != sig {
						t.Fatalf("idle signal behavior changed: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("signal was swallowed while waiting for input")
				}
				files, err := filepath.Glob(filepath.Join(dir, "hollis-prompt-*.txt"))
				if err != nil || len(files) != 0 {
					t.Fatalf("idle input staged a prompt: %v %v", files, err)
				}
			})
		}
	}
}
