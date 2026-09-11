// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestServeSignalHelper(t *testing.T) {
	if os.Getenv("HOLLIS_SERVE_SIGNAL_HELPER") != "1" {
		return
	}
	if err := os.Unsetenv("HOLLIS_API_TOKEN"); err != nil {
		t.Fatal(err)
	}
	stubResolution(t, allImportedNames(), true, 27)
	r := runner.New()
	r.ShortcutsPath = os.Getenv("HOLLIS_SERVE_SIGNAL_SHORTCUTS")
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
	cmd.SetArgs([]string{"serve", "--addr", "127.0.0.1:0", "--no-auth"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	err := executeCommand(cmd, flags)
	if err != nil && !ErrorReported(err) {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(ExitCode(err))
}

func TestServeSignalsCancelActiveTransport(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			dir := t.TempDir()
			ready := filepath.Join(dir, "ready")
			shortcut := filepath.Join(dir, "shortcuts")
			script := `#!/bin/sh
set -eu
[ "$5" = --input-path ] && [ -f "$6" ]
printf '%s\n%s\n' "$$" "$6" > "$HOLLIS_SERVE_SIGNAL_READY"
exec /bin/sleep 300
`
			if err := os.WriteFile(shortcut, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServeSignalHelper$")
			cmd.Env = append(os.Environ(), "HOLLIS_SERVE_SIGNAL_HELPER=1", "HOLLIS_SERVE_SIGNAL_SHORTCUTS="+shortcut, "HOLLIS_SERVE_SIGNAL_READY="+ready, "HOLLIS_STATE_DIR="+filepath.Join(dir, "state"), "TMPDIR="+dir)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
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
			line, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil {
				<-done
				t.Fatalf("server did not announce listener: %v stderr=%s", err, stderr.String())
			}
			fields := strings.Fields(line)
			if len(fields) < 5 || !strings.HasPrefix(fields[4], "http://127.0.0.1:") {
				t.Fatalf("unexpected announcement: %q", line)
			}
			type response struct {
				status int
				body   []byte
				err    error
			}
			responseDone := make(chan response, 1)
			go func() {
				client := &http.Client{Timeout: 12 * time.Second}
				res, err := client.Post(fields[4]+"/v1/responses", "application/json", strings.NewReader(`{"model":"cloud","input":"synthetic cancellation fixture"}`))
				if err != nil {
					responseDone <- response{err: err}
					return
				}
				defer res.Body.Close()
				raw, err := io.ReadAll(res.Body)
				responseDone <- response{status: res.StatusCode, body: raw, err: err}
			}()
			var promptPath string
			for {
				raw, err := os.ReadFile(ready)
				parts := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
				if err == nil && len(parts) == 2 {
					childPID, err = strconv.Atoi(parts[0])
					if err == nil && childPID > 0 {
						promptPath = parts[1]
						break
					}
				}
				select {
				case res := <-responseDone:
					t.Fatalf("request ended before fake transport: status=%d err=%v body=%s", res.status, res.err, res.body)
				case err := <-done:
					t.Fatalf("server ended before fake transport: %v", err)
				case <-ctx.Done():
					t.Fatal("fake transport did not start")
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
				if err != nil {
					t.Errorf("graceful server termination failed: %v stderr=%s", err, stderr.String())
				}
			case <-ctx.Done():
				t.Fatal("server shutdown did not finish")
			}
			if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
				t.Errorf("staged prompt remains after server termination: %v", err)
			}
			if err := syscall.Kill(-childPID, 0); err != syscall.ESRCH {
				t.Errorf("transport process group remains after server termination: %v", err)
			}
			select {
			case res := <-responseDone:
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(res.body, &body); err != nil {
					t.Errorf("missing HTTP cancellation response: %v; request error=%v", err, res.err)
				}
				if res.err != nil || res.status != http.StatusGatewayTimeout || body.Error.Code != "shortcut_timeout" {
					t.Errorf("changed HTTP cancellation contract: status=%d error=%v body=%s", res.status, res.err, res.body)
				}
			case <-ctx.Done():
				t.Fatal("active HTTP request did not finish")
			}
		})
	}
}
