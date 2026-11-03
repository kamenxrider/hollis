// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/mcp"
)

func TestMCPInspectSignalHelper(t *testing.T) {
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) < 2 {
		return
	}
	if args[0] == "client" {
		mcpConfigPath = func() (string, error) { return args[1], nil }
		_, err := runMCP(t, args[2:]...)
		os.Exit(ExitCode(err))
	}
	signal.Ignore(syscall.SIGTERM)
	if args[0] == "grandchild" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	dir := args[1]
	_ = os.WriteFile(filepath.Join(dir, "pgid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	child := exec.Command(os.Args[0], "-test.run=^TestMCPInspectSignalHelper$", "--", "grandchild", dir)
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	_ = os.WriteFile(filepath.Join(dir, "grandchild"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if decoder.Decode(&req) != nil {
			os.Exit(0)
		}
		switch req.Method {
		case "server/discover":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "unsupported"}})
		case "initialize":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": "signal-fixture", "version": "1"},
			}})
		case "tools/list":
			_ = os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0o600)
			time.Sleep(time.Hour)
		}
	}
}

func TestMCPInspectionSignalsCleanProcessGroup(t *testing.T) {
	for _, action := range []string{"tools", "allow"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(action+"/"+sig.String(), func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, mcp.FileName)
				if err := mcp.Update(path, func(cfg *mcp.Config) error {
					cfg.Servers["fixture"] = mcp.Server{Command: os.Args[0], Args: []string{"-test.run=^TestMCPInspectSignalHelper$", "--", "server", dir}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				args := []string{"-test.run=^TestMCPInspectSignalHelper$", "--", "client", path, "mcp", action, "fixture"}
				if action == "allow" {
					args = append(args, "noargs")
				}
				cmd := exec.Command(os.Args[0], args...)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				t.Cleanup(func() {
					_ = cmd.Process.Kill()
					if raw, err := os.ReadFile(filepath.Join(dir, "pgid")); err == nil {
						pid, _ := strconv.Atoi(string(raw))
						if pid > 0 {
							_ = syscall.Kill(-pid, syscall.SIGKILL)
						}
					}
				})
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("fixture never reached tools/list")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 7 {
						t.Errorf("inspection exit = %v; want timeout exit 7", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("inspection did not stop after signal")
				}
				for _, name := range []string{"pgid", "grandchild"} {
					raw, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					pid, _ := strconv.Atoi(string(raw))
					if name == "pgid" {
						pid = -pid
					}
					deadline := time.Now().Add(time.Second)
					for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
						if time.Now().After(deadline) {
							t.Fatalf("%s %d survived signal cleanup", name, pid)
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
			})
		}
	}
}
