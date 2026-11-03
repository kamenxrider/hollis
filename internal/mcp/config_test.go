// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const validConfig = `{
  "schema_version": 1,
  "servers": {
    "files": {"command": "npx", "args": ["-y", "pkg"], "env": {"LOG_LEVEL": "error"}, "env_from": {"GH_FILE": "GITHUB_TOKEN"}, "dir": "/tmp", "approval": "ask"},
    "wiki": {"url": "https://mcp.example.dev/mcp", "auth": {"type": "bearer", "env": "WIKI_TOKEN"}, "approval": "auto", "allow": ["read_page"], "call_timeout_ms": 20000, "connect_timeout_ms": 10000},
    "local": {"url": "http://127.0.0.1:8080/mcp"},
    "v6": {"url": "http://[::1]:9/mcp", "auth": {"type": "header", "name": "X-Api-Key", "env": "K"}}
  }
}`

func TestConfigValid(t *testing.T) {
	cfg, err := Parse([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Names(); strings.Join(got, ",") != "files,local,v6,wiki" {
		t.Errorf("names = %v", got)
	}
	if cfg.Servers["files"].Transport() != "stdio" || cfg.Servers["wiki"].Transport() != "http" {
		t.Error("transport misreported")
	}
	if cfg.Servers["local"].Mode() != ApprovalAsk {
		t.Error("absent approval must mean ask")
	}
}

func server(fields string) string {
	return `{"schema_version":1,"servers":{"s":` + fields + `}}`
}

func TestConfigInvariants(t *testing.T) {
	cases := []struct {
		name, doc, want string
	}{
		{"version missing", `{"servers":{}}`, "schema_version must be 1"},
		{"version newer", `{"schema_version":2,"servers":{}}`, "newer Hollis"},
		{"unknown field", `{"schema_version":1,"servers":{},"x":1}`, "unknown field"},
		{"unknown server field", server(`{"command":"a","shell":true}`), "unknown field"},
		{"duplicate server", `{"schema_version":1,"servers":{"a":{"command":"x"},"a":{"command":"y"}}}`, `duplicate key "a"`},
		{"duplicate nested", server(`{"command":"a","command":"b"}`), `duplicate key "command"`},
		{"trailing data", `{"schema_version":1,"servers":{}} {}`, "exactly one JSON object"},
		{"non object", `[]`, "parse"},
		{"bad name", `{"schema_version":1,"servers":{"Bad Name":{"command":"x"}}}`, "server name"},
		{"neither", server(`{}`), "exactly one of"},
		{"both", server(`{"command":"a","url":"https://x"}`), "exactly one of"},
		{"args with url", server(`{"url":"https://x","args":["a"]}`), "only valid with \"command\""},
		{"auth with command", server(`{"command":"a","auth":{"type":"bearer","env":"T"}}`), "only valid with \"url\""},
		{"relative command", server(`{"command":"./run"}`), "absolute path or a bare program name"},
		{"shell string", server(`{"command":"npx -y pkg"}`), "no shell is used"},
		{"secret env", server(`{"command":"a","env":{"GITHUB_TOKEN":"x"}}`), "looks like a credential"},
		{"hollis env", server(`{"command":"a","env":{"HOLLIS_X":"x"}}`), "looks like a credential"},
		{"secret env_from child", server(`{"command":"a","env_from":{"MY_TOKEN":"GITHUB_TOKEN"}}`), "would never reach the server"},
		{"bad env name", server(`{"command":"a","env":{"1X":"x"}}`), "not a valid variable name"},
		{"relative dir", server(`{"command":"a","dir":"rel"}`), "absolute path"},
		{"plaintext remote", server(`{"url":"http://example.com/mcp"}`), "plaintext http is only allowed for loopback"},
		{"userinfo", server(`{"url":"https://u:p@example.com/mcp"}`), "user information"},
		{"fragment", server(`{"url":"https://example.com/mcp#x"}`), "fragment"},
		{"ftp", server(`{"url":"ftp://example.com/mcp"}`), "must use https"},
		{"auto empty", server(`{"command":"a","approval":"auto"}`), "allow is empty"},
		{"bad approval", server(`{"command":"a","approval":"yes"}`), "must be \"ask\" or \"auto\""},
		{"wildcard", server(`{"command":"a","approval":"auto","allow":["*"]}`), "wildcards are not supported"},
		{"prefix", server(`{"command":"a","approval":"auto","allow":["read*"]}`), "wildcards are not supported"},
		{"duplicate allow", server(`{"command":"a","allow":["x","x"]}`), "twice"},
		{"pin without allow", server(`{"command":"a","pins":{"x":"` + strings.Repeat("a", 64) + `"}}`), "not in"},
		{"bad pin", server(`{"command":"a","allow":["x"],"pins":{"x":"zz"}}`), "SHA-256"},
		{"call timeout", server(`{"command":"a","call_timeout_ms":5}`), "call_timeout_ms must be between"},
		{"connect timeout", server(`{"command":"a","connect_timeout_ms":600000}`), "connect_timeout_ms must be between"},
		{"auth type", server(`{"url":"https://x","auth":{"type":"basic","env":"T"}}`), "bearer"},
		{"auth header authorization", server(`{"url":"https://x","auth":{"type":"header","name":"Authorization","env":"T"}}`), "must not be Authorization"},
		{"invalid utf8", "{\"schema_version\":1,\"servers\":{\"s\":{\"command\":\"\xff\"}}}", "UTF-8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc))
			if err == nil {
				t.Fatalf("accepted %s", tc.doc)
			}
			e := wantCode(t, err, CodeConfig)
			if !strings.Contains(e.Message, tc.want) {
				t.Errorf("message %q; want %q", e.Message, tc.want)
			}
		})
	}
}

func TestConfigServerCap(t *testing.T) {
	servers := map[string]Server{}
	for i := 0; i <= MaxServers; i++ {
		servers[strings.Repeat("a", 1)+string(rune('a'+i%26))+strings.Repeat("b", i/26)] = Server{Command: "x"}
	}
	err := Config{SchemaVersion: 1, Servers: servers}.Validate()
	e := wantCode(t, err, CodeConfig)
	if !strings.Contains(e.Message, "33 servers") {
		t.Errorf("message %q", e.Message)
	}
}

func TestConfigFileRules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, FileName)
	cfg, err := Load(path)
	if err != nil || len(cfg.Servers) != 0 {
		t.Fatalf("missing file = %v, %v; want empty config", cfg, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(validConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	dirInfo, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o600 || dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("modes %v %v; want 0600 file in 0700 dir", info.Mode().Perm(), dirInfo.Mode().Perm())
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Error("a symlinked config was followed")
	}
	big := filepath.Join(dir, "big.json")
	_ = os.WriteFile(big, []byte(`{"x":"`+strings.Repeat("a", maxConfigBytes)+`"}`), 0o600)
	if _, err := Load(big); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized config: %v", err)
	}
	sub := filepath.Join(dir, "sub")
	_ = os.Mkdir(sub, 0o700)
	if _, err := Load(sub); err == nil {
		t.Error("a directory was accepted as a config file")
	}
}

func TestConfigUpdateAtomicAndValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Update(path, func(c *Config) error {
		c.Servers["a"] = Server{Command: "x"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	err := Update(path, func(c *Config) error {
		s := c.Servers["a"]
		s.Approval = ApprovalAuto
		c.Servers["a"] = s
		return nil
	})
	wantCode(t, err, CodeConfig)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("a refused update changed the file")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".mcp.json-") {
			t.Errorf("temp file %s left behind", entry.Name())
		}
	}
	var decoded Config
	if err := json.Unmarshal(after, &decoded); err != nil || decoded.SchemaVersion != 1 {
		t.Errorf("written file = %s", after)
	}
}

func TestChildEnvScrub(t *testing.T) {
	parent := []string{
		"PATH=/bin", "HOME=/h", "TMPDIR=/t", "LANG=C", "LC_ALL=C", "PYTHONPATH=/p", "VIRTUAL_ENV=/v",
		"HOLLIS_X=canary1", "SOME_TOKEN=canary2", "MY_KEY=canary3", "A_SECRET=canary4", "PASSWORD_VAR=canary5",
		"AWS_CREDENTIALS=canary6", "AUTHORIZATION=canary7", "UNRELATED=canary8", "GITHUB_TOKEN=passed",
		"PYTHON_API_KEY=canary9",
	}
	s := Server{Command: "x", Env: map[string]string{"LOG_LEVEL": "error", "PATH": "/override"}, EnvFrom: map[string]string{"GH_VALUE": "GITHUB_TOKEN"}}
	env, err := ChildEnv(parent, "s", s)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(env, "\n")
	for i := 1; i <= 9; i++ {
		if strings.Contains(got, "canary"+string(rune('0'+i))) {
			t.Errorf("canary%d leaked: %v", i, env)
		}
	}
	want := []string{"GH_VALUE=passed", "HOME=/h", "LANG=C", "LC_ALL=C", "LOG_LEVEL=error", "PATH=/override", "PYTHONPATH=/p", "TMPDIR=/t", "VIRTUAL_ENV=/v"}
	if !sort.StringsAreSorted(env) || strings.Join(env, ",") != strings.Join(want, ",") {
		t.Errorf("env = %v; want %v", env, want)
	}
	noPath, _ := ChildEnv([]string{"HOME=/h"}, "s", Server{Command: "x"})
	if !strings.Contains(strings.Join(noPath, ","), "PATH="+fallbackPath) {
		t.Errorf("PATH not synthesized: %v", noPath)
	}
	_, err = ChildEnv(parent, "s", Server{Command: "x", EnvFrom: map[string]string{"A": "MISSING_PARENT"}})
	e := wantCode(t, err, CodeConfig)
	if !strings.Contains(e.Message, "MISSING_PARENT") || !strings.Contains(e.Message, "A ") {
		t.Errorf("message %q", e.Message)
	}
}

func TestEnvironmentScrubbedEndToEnd(t *testing.T) {
	srv, dir := fixtureServer(t, "envdump")
	srv.Env = map[string]string{"LOG_LEVEL": "error"}
	srv.EnvFrom = map[string]string{"GH_VALUE": "GITHUB_TOKEN"}
	opts := interactive()
	opts.Environ = []string{"PATH=/usr/bin:/bin", "HOME=/h", "HOLLIS_X=canary-a", "SOME_TOKEN=canary-b", "MY_KEY=canary-c", "A_SECRET=canary-d", "PASSWORD_VAR=canary-e", "GITHUB_TOKEN=forwarded", "RANDOM=canary-f"}
	m := attach(t, "fixture", srv, opts)
	result, err := m.Call(callCtx(t), "fixture", "env_as_json", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(result.Text), &env); err != nil {
		t.Fatal(err)
	}
	startup, _ := os.ReadFile(filepath.Join(dir, "env.json"))
	for _, canary := range []string{"canary-a", "canary-b", "canary-c", "canary-d", "canary-e", "canary-f"} {
		if strings.Contains(result.Text, canary) || strings.Contains(string(startup), canary) {
			t.Errorf("%s reached the child", canary)
		}
	}
	if env["LOG_LEVEL"] != "error" || env["GH_VALUE"] != "forwarded" || env["HOME"] != "/h" {
		t.Errorf("configured variables missing: %v", env)
	}
}

func TestStderrNeverSurfaces(t *testing.T) {
	canary := "stderr-canary-7f3a"
	srv, dir := fixtureServer(t, "stderr")
	srv.Env = map[string]string{"FIXTURE_CANARY": canary}
	m := attach(t, "fixture", srv, interactive())
	result, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, canary) {
		t.Error("stderr reached the result")
	}
	status := m.Servers()[0]
	// A stdout reply does not synchronize the separate stderr reader.
	deadline := time.Now().Add(2 * time.Second)
	for status.StderrBytes < 64<<10 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		status = m.Servers()[0]
	}
	if status.StderrBytes < 64<<10 {
		t.Errorf("stderr bytes = %d; want the full count", status.StderrBytes)
	}
	statusJSON, _ := json.Marshal(m.Servers())
	recordsJSON, _ := json.Marshal(m.Records())
	if strings.Contains(string(statusJSON)+string(recordsJSON), canary) {
		t.Error("stderr reached metadata")
	}
	_ = dir
	m.Close()
	ring := m.sessions[0].link.(*stdioLink).stderr
	ring.mu.Lock()
	kept := len(ring.buf)
	ring.mu.Unlock()
	if kept > StderrCap {
		t.Errorf("kept %d stderr bytes; cap %d", kept, StderrCap)
	}
}
