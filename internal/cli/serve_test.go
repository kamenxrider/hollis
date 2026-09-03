// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestLoadServeTokenRequiresPrivateRegularFile(t *testing.T) {
	t.Setenv("HOLLIS_API_TOKEN", "temporary")
	if err := os.Unsetenv("HOLLIS_API_TOKEN"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadServeToken(path)
	if err != nil || got != strings.Repeat("x", 32) {
		t.Fatalf("token=%q err=%v", got, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeToken(path); err == nil {
		t.Fatal("world-readable token file accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeToken(link); err == nil {
		t.Fatal("token symlink accepted")
	}
}

func TestServeRejectsUnsafeArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
	}{
		{"removed command line token", []string{"serve", "--token", strings.Repeat("x", 32)}, ""},
		{"extra argument", []string{"serve", "extra"}, ""},
		{"bad concurrency low", []string{"serve", "--max-concurrency", "0"}, ""},
		{"bad concurrency high", []string{"serve", "--max-concurrency", "5"}, ""},
		{"remote without opt in", []string{"serve", "--addr", "0.0.0.0:0"}, strings.Repeat("x", 32)},
		{"remote without auth", []string{"serve", "--allow-remote", "--addr", "0.0.0.0:0"}, ""},
		{"short token", []string{"serve", "--allow-remote", "--addr", "0.0.0.0:0"}, "short"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOLLIS_API_TOKEN", tc.env)
			cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
			cmd.SetArgs(tc.args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.Execute(); err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
			}
		})
	}
}

func TestServeRejectsTokenFileAndEnvironmentTogether(t *testing.T) {
	t.Setenv("HOLLIS_API_TOKEN", strings.Repeat("e", 32))
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(strings.Repeat("f", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"serve", "--token-file", path})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 10 {
		t.Fatalf("err=%v exit=%d, want config 10", err, ExitCode(err))
	}
}

func TestServeRejectsExplicitEmptyTokenSources(t *testing.T) {
	dir := t.TempDir()
	emptyFile := filepath.Join(dir, "empty-token")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		env  *string
	}{
		{name: "empty token file", args: []string{"serve", "--token-file", emptyFile}},
		{name: "empty environment token", args: []string{"serve"}, env: new(string)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == nil {
				if err := os.Unsetenv("HOLLIS_API_TOKEN"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("HOLLIS_API_TOKEN", *tc.env)
			}
			cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
			cmd.SetArgs(tc.args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
			}
		})
	}
}

func TestServeAuthorizesResolvedAddressNotHostname(t *testing.T) {
	if loopback, literal := literalLoopback("localhost"); loopback || literal {
		t.Fatal("localhost hostname was trusted before resolving and binding")
	}
	if loopback, literal := literalLoopback("127.0.0.1"); !loopback || !literal {
		t.Fatal("literal loopback address was not recognized")
	}
	if listenerLoopback(&net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 1978}) {
		t.Fatal("resolved non-loopback listener was accepted as loopback")
	}
	if !listenerLoopback(&net.TCPAddr{IP: net.ParseIP("::1"), Port: 1978}) {
		t.Fatal("resolved IPv6 loopback listener was not recognized")
	}
}

func TestServeGracefulContextShutdown(t *testing.T) {
	t.Setenv("HOLLIS_API_TOKEN", "temporary")
	if err := os.Unsetenv("HOLLIS_API_TOKEN"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"serve", "--addr", "127.0.0.1:0"})
	out := &notifyingWriter{ready: make(chan struct{})}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case <-out.ready:
	case err := <-done:
		if err != nil && strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("sandbox forbids loopback listeners")
		}
		t.Fatalf("server stopped before readiness: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not announce readiness")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("server did not shut down within grace period")
	}
}

type notifyingWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *notifyingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(data)
	w.mu.Unlock()
	w.once.Do(func() { close(w.ready) })
	return n, err
}
