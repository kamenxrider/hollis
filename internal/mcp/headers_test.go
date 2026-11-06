package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStaticHeaderValidation(t *testing.T) {
	for _, name := range []string{"Authorization", "x-API-key", "X-Token", "X-Secret", "Password",
		"Credential", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Session-ID",
		"Host", "Content-Type", "Content-Length", "Accept", "Connection", "Transfer-Encoding",
		"MCP-Protocol-Version", "Mcp-Session-Id", "bad name", ""} {
		t.Run(name, func(t *testing.T) {
			s := Server{URL: "https://example.invalid/mcp", Headers: map[string]string{name: "value-canary"}}
			err := ValidateServer("s", s)
			wantCode(t, err, CodeConfig)
			if strings.Contains(err.Error(), "value-canary") {
				t.Fatal("header value reached error")
			}
		})
	}
	cases := []Server{
		{Command: "a", Headers: map[string]string{"X-Settings": "v"}},
		{URL: "https://example.invalid", Headers: map[string]string{"X-Settings": "a", "x-settings": "b"}},
		{URL: "https://example.invalid", Headers: map[string]string{"X-Settings": "a\r\nb"}},
		{URL: "https://example.invalid", Headers: map[string]string{"X-Settings": "a\x00b"}},
		{URL: "https://example.invalid", Headers: map[string]string{"X-Settings": "a\x7fb"}},
		{URL: "https://example.invalid", Headers: map[string]string{"X-Settings": strings.Repeat("x", 8193)}},
		{URL: "https://example.invalid", Headers: map[string]string{"X-Settings": "v"}, Auth: &Auth{Type: "header", Name: "x-settings", Env: "CRED"}},
	}
	many := map[string]string{}
	for i := 0; i < 33; i++ {
		many["X-"+strings.Repeat("a", i+1)] = "v"
	}
	cases = append(cases, Server{URL: "https://example.invalid", Headers: many})
	for i, s := range cases {
		if err := ValidateServer("s", s); err == nil {
			t.Errorf("accepted invalid case %d", i)
		}
	}
	raw := []byte(server(`{"url":"https://example.invalid/mcp","headers":{"x-settings":"{\"max_chars\":6000}"}}`))
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers["s"].Headers["x-settings"] != `{"max_chars":6000}` {
		t.Fatal("header did not round trip")
	}
}

func TestStaticHeadersOnEveryHTTPRequest(t *testing.T) {
	f := &httpFixture{sessionID: "settings-session"}
	var checked, bad atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checked.Add(1)
		if r.Header.Get("X-Provider-Config") != `{"max_chars":6000}` || r.Header.Get("Authorization") != "Bearer fixture-cred" {
			bad.Add(1)
		}
		if r.Method == http.MethodPost && !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			bad.Add(1)
		}
		f.ServeHTTP(w, r)
	}))
	defer ts.Close()
	opts := interactive()
	opts.Environ = []string{"CRED=fixture-cred"}
	s := Server{URL: ts.URL, Headers: map[string]string{"X-Provider-Config": `{"max_chars":6000}`}, Auth: &Auth{Type: "bearer", Env: "CRED"}}
	m := attach(t, "remote", s, opts)
	// A session snapshots its settings rather than sharing a mutable map.
	s.Headers["X-Provider-Config"] = "changed"
	_, err := m.Call(callCtx(t), "remote", "echo", json.RawMessage(`{"text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	if checked.Load() < 4 || bad.Load() != 0 || f.deletes.Load() != 1 {
		t.Fatalf("checked=%d bad=%d deletes=%d", checked.Load(), bad.Load(), f.deletes.Load())
	}
}
