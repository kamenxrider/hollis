// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

const (
	serveLocalStatusModeEnv     = "HOLLIS_SERVE_LOCAL_STATUS_MODE"
	serveLocalStatusShortcutEnv = "HOLLIS_SERVE_LOCAL_STATUS_SHORTCUTS"
)

// serveLocalStatusRunner is the routed transport a fake factory hands serve.
// It carries the real Shortcuts half so resolution runs exactly as in
// production, and answers only the non-generative status probe: a request that
// reaches inference fails the test instead of running anything.
type serveLocalStatusRunner struct {
	*runner.ShortcutRunner
	probeErr error
}

func (r *serveLocalStatusRunner) ShortcutTransport() *runner.ShortcutRunner { return r.ShortcutRunner }

func (r *serveLocalStatusRunner) NativeStatus(context.Context) (runner.LocalStatus, error) {
	return runner.LocalStatus{}, r.probeErr
}

func (r *serveLocalStatusRunner) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	panic("serve reached inference for a local request")
}

func (r *serveLocalStatusRunner) RunComplete(context.Context, runner.Model, string) (runner.Completion, error) {
	panic("serve reached inference for a local request")
}

func (r *serveLocalStatusRunner) Stream(context.Context, runner.Model, string, func(string) error) (runner.Completion, error) {
	panic("serve reached inference for a local request")
}

func (r *serveLocalStatusRunner) RunSchema(context.Context, runner.Model, string, *runner.Schema) (runner.Completion, error) {
	panic("serve reached inference for a local request")
}

func helperMismatchProbeError() error {
	return &runner.Error{Kind: runner.KindNativeProtocol, ExitCode: 5,
		Err: errors.New("native helper version, protocol or model mismatch; install the matching bundle")}
}

func missingHelperProbeError() error {
	return &runner.Error{Kind: runner.KindLocalUnavailable, ExitCode: 3,
		Err: errors.New("native local helper is missing or not executable; install the matching Hollis Apple Silicon bundle")}
}

// TestServeLocalStatusHelper is the child half of the classification test. It
// starts the real serve command with a runner whose status probe fails the way
// a paired-but-mismatched helper does at startup.
func TestServeLocalStatusHelper(t *testing.T) {
	mode := os.Getenv(serveLocalStatusModeEnv)
	if mode == "" {
		t.Skip("offline child fixture only")
	}
	if err := os.Unsetenv("HOLLIS_API_TOKEN"); err != nil {
		t.Fatal(err)
	}
	stubResolution(t, allImportedNames(), true, 27)
	var probeErr error
	switch mode {
	case "protocol":
		probeErr = helperMismatchProbeError()
	case "missing":
		probeErr = missingHelperProbeError()
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	r := &serveLocalStatusRunner{ShortcutRunner: runner.New(), probeErr: probeErr}
	r.ShortcutRunner.ShortcutsPath = os.Getenv(serveLocalStatusShortcutEnv)
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

// TestServeLocalStatusClassification pins the HTTP contract for a local
// request after a failed startup status probe. A protocol/version mismatch is
// the documented 502 native_protocol on every local request, guided or plain;
// a genuinely missing helper stays 400 model_unavailable. Either way nothing
// reaches the runner.
func TestServeLocalStatusClassification(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		status int
		code   string
	}{
		{"protocol mismatch", "protocol", http.StatusBadGateway, "native_protocol"},
		{"missing helper", "missing", http.StatusBadRequest, "model_unavailable"},
	}
	requests := []struct{ name, path, body string }{
		{"guided-chat", "/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"person","schema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}}}}`},
		{"guided-responses", "/v1/responses", `{"model":"local","input":"hi","text":{"format":{"type":"json_schema","name":"person","schema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}}}}`},
		{"plain-chat", "/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"hi"}]}`},
		{"plain-responses", "/v1/responses", `{"model":"local","input":"hi"}`},
		{"stream-chat", "/v1/chat/completions", `{"model":"local","stream":true,"messages":[{"role":"user","content":"hi"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServeLocalStatusHelper$")
			cmd.Env = append(os.Environ(),
				serveLocalStatusModeEnv+"="+tc.mode,
				serveLocalStatusShortcutEnv+"="+filepath.Join(dir, "shortcuts"),
				"HOLLIS_STATE_DIR="+filepath.Join(dir, "state"),
				"HOME="+filepath.Join(dir, "home"),
				"TMPDIR="+dir,
			)
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
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
			base := fields[4]
			for _, request := range requests {
				client := &http.Client{Timeout: 10 * time.Second}
				res, err := client.Post(base+request.path, "application/json", strings.NewReader(request.body))
				if err != nil {
					t.Fatalf("%s request failed: %v stderr=%s", request.name, err, stderr.String())
				}
				raw, readErr := io.ReadAll(res.Body)
				res.Body.Close()
				if readErr != nil {
					t.Fatalf("%s body: %v", request.name, readErr)
				}
				if res.StatusCode != tc.status {
					t.Fatalf("%s status=%d want=%d body=%s", request.name, res.StatusCode, tc.status, raw)
				}
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatalf("%s error JSON: %v (%s)", request.name, err, raw)
				}
				if body.Error.Code != tc.code {
					t.Fatalf("%s code=%q want=%q body=%s", request.name, body.Error.Code, tc.code, raw)
				}
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("server termination failed: %v stderr=%s", err, stderr.String())
				}
			case <-ctx.Done():
				t.Fatalf("server shutdown did not finish; stderr=%s", stderr.String())
			}
			if bytes.Contains(stderr.Bytes(), []byte("reached inference")) {
				t.Fatalf("a request reached the runner: %s", stderr.String())
			}
		})
	}
}
