// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

// Offline remote fixtures: httptest servers speaking enough of the
// streamable HTTP transport to cover the shapes real servers use.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type httpFixture struct {
	sse        bool
	listSSE    bool
	sessionID  string
	stall      bool
	status     int
	redirect   string
	truncate   bool
	bigBody    bool
	structured json.RawMessage

	mu       sync.Mutex
	requests []recordedRequest
	calls    atomic.Int64
	deletes  atomic.Int64
}

type recordedRequest struct {
	Method   string
	Auth     string
	Header   string
	RPC      string
	Protocol string
}

func (f *httpFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var msg rpcMessage
	_ = json.Unmarshal(body, &msg)
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{Method: r.Method, Auth: r.Header.Get("Authorization"), Header: r.Header.Get("X-Api-Key"), RPC: msg.Method, Protocol: r.Header.Get("MCP-Protocol-Version")})
	f.mu.Unlock()
	if f.redirect != "" {
		http.Redirect(w, r, f.redirect, http.StatusFound)
		return
	}
	if f.status != 0 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="x"`)
		http.Error(w, "nope", f.status)
		return
	}
	switch r.Method {
	case http.MethodGet:
		http.Error(w, "no stream", http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		f.deletes.Add(1)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if f.sessionID != "" && msg.Method != "initialize" && msg.Method != "server/discover" && r.Header.Get("Mcp-Session-Id") != f.sessionID {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}
	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	var rpcErr map[string]any
	switch msg.Method {
	case "server/discover":
		rpcErr = map[string]any{"code": -32601, "message": "method not found"}
	case "initialize":
		if f.stall {
			<-r.Context().Done()
			return
		}
		if f.sessionID != "" {
			w.Header().Set("Mcp-Session-Id", f.sessionID)
		}
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "http-fixture", "version": "1"}}
	case "tools/list":
		result = map[string]any{"tools": []any{fixtureTool("echo", "Echo.", echoSchema)}}
	case "tools/call":
		f.calls.Add(1)
		text := "remote-ok"
		if f.bigBody {
			text = strings.Repeat("r", 9<<20)
		}
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
		if f.structured != nil {
			result = map[string]any{"content": []any{}, "structuredContent": f.structured}
		}
	default:
		rpcErr = map[string]any{"code": -32601, "message": "method not found"}
	}
	reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
	if rpcErr != nil {
		reply["error"] = rpcErr
	} else {
		reply["result"] = result
	}
	raw, _ := json.Marshal(reply)
	useSSE := f.sse || (f.listSSE && msg.Method == "tools/list")
	if f.truncate && msg.Method == "tools/call" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprint(len(raw)+100))
		_, _ = w.Write(raw[:len(raw)/2])
		return
	}
	if useSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)
		fmt.Fprintf(bw, "event: message\ndata: %s\n\n", raw)
		_ = bw.Flush()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (f *httpFixture) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func httpServer(t *testing.T, f *httpFixture) (Server, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	return Server{URL: ts.URL + "/mcp"}, ts
}

func TestHTTPShapes(t *testing.T) {
	for name, f := range map[string]*httpFixture{
		"json":    {},
		"sse":     {sse: true},
		"mixed":   {listSSE: true},
		"session": {sessionID: "sess-1"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := httpServer(t, f)
			m := attach(t, "remote", srv, interactive())
			if len(m.Tools()) != 1 || m.Servers()[0].Transport != "http" {
				t.Fatalf("tools = %v", m.Tools())
			}
			result, err := m.Call(callCtx(t), "remote", "echo", json.RawMessage(`{"text":"x"}`))
			if err != nil || result.Text != "remote-ok" {
				t.Fatalf("call = %+v, %v", result, err)
			}
			for _, req := range f.recorded() {
				if req.Method == http.MethodGet {
					t.Error("a standalone SSE stream was opened")
				}
			}
			m.Close()
			if name == "session" && f.deletes.Load() != 1 {
				t.Errorf("session DELETE count %d; want 1", f.deletes.Load())
			}
		})
	}
}

func TestHTTPConnectDeadline(t *testing.T) {
	f := &httpFixture{stall: true}
	srv, _ := httpServer(t, f)
	srv.ConnectTimeoutMS = 1000
	start := time.Now()
	err := attachErr(t, "remote", srv, interactive())
	wantCode(t, err, CodeTimeout)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("connect took %s", elapsed)
	}
}

func TestHTTPAuth(t *testing.T) {
	f := &httpFixture{}
	srv, _ := httpServer(t, f)
	srv.Auth = &Auth{Type: "bearer", Env: "FIXTURE_REMOTE_CRED"}
	opts := interactive()
	opts.Environ = []string{"FIXTURE_REMOTE_CRED=cred-canary-55"}
	m := attach(t, "remote", srv, opts)
	for _, req := range f.recorded() {
		if req.Auth != "Bearer cred-canary-55" {
			t.Errorf("request %s carried auth %q", req.RPC, req.Auth)
		}
	}
	meta, _ := json.Marshal(m.Servers())
	if strings.Contains(string(meta), "cred-canary-55") {
		t.Error("credential reached metadata")
	}

	hf := &httpFixture{}
	hsrv, _ := httpServer(t, hf)
	hsrv.Auth = &Auth{Type: "header", Name: "X-Api-Key", Env: "FIXTURE_REMOTE_CRED"}
	attach(t, "remote2", hsrv, opts)
	if r := hf.recorded(); len(r) == 0 || r[0].Header != "cred-canary-55" || r[0].Auth != "" {
		t.Errorf("header auth = %+v", r)
	}

	err := attachErr(t, "remote3", srv, interactive())
	e := wantCode(t, err, CodeConfig)
	if !strings.Contains(e.Message, "FIXTURE_REMOTE_CRED") {
		t.Errorf("message %q", e.Message)
	}

	denied := &httpFixture{status: http.StatusUnauthorized}
	dsrv, _ := httpServer(t, denied)
	dsrv.Auth = srv.Auth
	err = attachErr(t, "remote4", dsrv, opts)
	e = wantCode(t, err, CodeAuth)
	if strings.Contains(err.Error(), "cred-canary-55") {
		t.Error("the credential was echoed in an error")
	}
	if n := len(denied.recorded()); n != 1 {
		t.Errorf("a 401 was retried: %d requests", n)
	}
	_ = e
}

func TestHTTPServerErrorsNotRetried(t *testing.T) {
	f := &httpFixture{status: http.StatusInternalServerError}
	srv, _ := httpServer(t, f)
	err := attachErr(t, "remote", srv, interactive())
	if c := CodeOf(err); c != CodeUnavailable && c != CodeProtocol {
		t.Fatalf("code %s (%v)", c, err)
	}
	if n := len(f.recorded()); n > 2 {
		t.Errorf("%d requests to a failing server; want no retries", n)
	}
}

func TestHTTPBodyCaps(t *testing.T) {
	big := &httpFixture{bigBody: true}
	srv, _ := httpServer(t, big)
	m := attach(t, "remote", srv, interactive())
	result, err := m.Call(callCtx(t), "remote", "echo", json.RawMessage(`{"text":"x"}`))
	if result.Text != "" {
		t.Error("an oversized body produced a result")
	}
	if c := CodeOf(err); c != CodeResultTooLarge && c != CodeProtocol {
		t.Fatalf("code %s (%v)", c, err)
	}

	cut := &httpFixture{truncate: true}
	csrv, _ := httpServer(t, cut)
	cm := attach(t, "remote", csrv, interactive())
	result, err = cm.Call(callCtx(t), "remote", "echo", json.RawMessage(`{"text":"x"}`))
	if err == nil || result.Text != "" {
		t.Fatalf("a truncated body returned %+v, %v", result, err)
	}
}

func TestHTTPTLSRules(t *testing.T) {
	target := &httpFixture{}
	tsrv, _ := httpServer(t, target)
	redirector := &httpFixture{redirect: tsrv.URL}
	rsrv, _ := httpServer(t, redirector)
	rsrv.Auth = &Auth{Type: "bearer", Env: "FIXTURE_REMOTE_CRED"}
	opts := interactive()
	opts.Environ = []string{"FIXTURE_REMOTE_CRED=redirect-cred"}
	err := attachErr(t, "remote", rsrv, opts)
	e := wantCode(t, err, CodeProtocol)
	if !strings.Contains(e.Message, "redirect") {
		t.Errorf("message %q", e.Message)
	}
	if n := len(target.recorded()); n != 0 {
		t.Errorf("the redirect target received %d requests", n)
	}
	if r := redirector.recorded(); len(r) == 0 || r[0].Auth != "Bearer redirect-cred" {
		t.Errorf("initial request auth = %+v", r)
	}

	var connections atomic.Int64
	plain := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	plain.Config.ConnState = func(net.Conn, http.ConnState) { connections.Add(1) }
	plain.Start()
	defer plain.Close()
	port := plain.URL[strings.LastIndex(plain.URL, ":"):]
	err = attachErr(t, "remote", Server{URL: "http://example.invalid" + port + "/mcp"}, interactive())
	wantCode(t, err, CodeConfig)
	if connections.Load() != 0 {
		t.Error("a refused plaintext server saw a connection")
	}

	tls := httptest.NewTLSServer(&httpFixture{})
	defer tls.Close()
	err = attachErr(t, "remote", Server{URL: tls.URL + "/mcp", ConnectTimeoutMS: 3000}, interactive())
	if c := CodeOf(err); c != CodeUnavailable {
		t.Errorf("self-signed TLS: %s (%v); want mcp_unavailable", c, err)
	}
}

func TestHTTPRetriesDisabled(t *testing.T) {
	l, err := newHTTPLink("s", Server{URL: "https://example.invalid/mcp"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	sct, ok := l.transport().(*sdk.StreamableClientTransport)
	if !ok {
		t.Fatalf("transport is %T", l.transport())
	}
	if sct.MaxRetries >= 0 || !sct.DisableStandaloneSSE || sct.MaxEventSize != MaxFrameBytes || sct.HTTPClient.Jar != nil {
		t.Errorf("transport config = %+v", sct)
	}
}
