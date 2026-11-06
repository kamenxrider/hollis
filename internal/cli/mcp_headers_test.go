package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/mcp"
)

func TestMCPStaticHeaderFlags(t *testing.T) {
	path := stubMCP(t)
	_, err := runMCP(t, "mcp", "add", "search", "--url", "https://example.invalid/mcp",
		"--header", `X-Provider-Config: {"url":"https://example.invalid","limit":6000}`,
		"--header", "X-Mode: small", "--auth", "bearer:SEARCH_CRED")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mcp.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Servers["search"]
	if s.Headers["x-provider-config"] != `{"url":"https://example.invalid","limit":6000}` || s.Headers["x-mode"] != "small" || s.Auth.Env != "SEARCH_CRED" {
		t.Fatalf("unexpected configuration: %+v", s)
	}
	for _, args := range [][]string{{"mcp", "list"}, {"mcp", "list", "--json"}} {
		out, err := runMCP(t, args...)
		if err != nil || strings.Contains(out, "6000") {
			t.Fatalf("list printed header value or failed: %s, %v", out, err)
		}
	}
}

func TestMCPStaticHeaderRefusals(t *testing.T) {
	path := stubMCP(t)
	for _, tc := range []struct {
		headers []string
		exit    int
	}{
		{[]string{"Authorization: header-value-canary"}, 10},
		{[]string{"X-API-Key: header-value-canary"}, 10},
		{[]string{"Cookie: header-value-canary"}, 10},
		{[]string{"X-Config: header-value-canary", "x-config: other"}, 2},
		{[]string{"header-value-canary"}, 2},
		{[]string{"Content-Type: header-value-canary"}, 10},
		{[]string{"X-Config: header-value-canary\r\ninjected"}, 10},
	} {
		args := []string{"mcp", "add", "s", "--url", "https://example.invalid"}
		for _, h := range tc.headers {
			args = append(args, "--header", h)
		}
		out, err := runMCP(t, args...)
		if ExitCode(err) != tc.exit {
			t.Errorf("exit=%d want=%d", ExitCode(err), tc.exit)
		}
		if strings.Contains(out, "header-value-canary") || (err != nil && strings.Contains(err.Error(), "header-value-canary")) {
			t.Fatal("header value reached output")
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("refused header created a config")
	}
}
