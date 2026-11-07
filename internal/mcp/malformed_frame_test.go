package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMalformedJSONRPCIsProtocolFailure(t *testing.T) {
	script := filepath.Join(t.TempDir(), "malformed-server")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nread -r line\nprintf '%s' '{\"jsonrpc\":'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	wantCode(t, attachErr(t, "synthetic", Server{Command: script}, interactive()), CodeProtocol)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":`))
	}))
	defer srv.Close()
	wantCode(t, attachErr(t, "synthetic", Server{URL: srv.URL}, interactive()), CodeProtocol)
}
