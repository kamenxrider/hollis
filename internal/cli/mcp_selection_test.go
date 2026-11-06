// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/mcp"
)

func TestMCPToolSelectionAddSetInspect(t *testing.T) {
	path := stubMCP(t)
	f := newMCPFixture(t)
	if _, err := runMCP(t, "mcp", "add", "wiki", "--url", f.url, "--tool", "read_page", "--tool", "search"); err != nil {
		t.Fatal(err)
	}
	if _, err := runMCP(t, "mcp", "allow", "wiki", "read_page"); err != nil {
		t.Fatal(err)
	}
	if _, err := runMCP(t, "mcp", "mode", "wiki", "auto"); err != nil {
		t.Fatal(err)
	}
	before, err := mcp.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before.Servers["wiki"].Tools, []string{"read_page", "search"}) {
		t.Fatal("repeatable --tool did not round-trip")
	}
	if _, err := runMCP(t, "mcp", "set", "wiki", "--tool", "read_page"); err != nil {
		t.Fatal(err)
	}
	selected, err := runMCP(t, "mcp", "set", "wiki", "--tool", "read_page", "--json", "--select", "server.tools")
	if err != nil || !strings.Contains(selected, `"tools":["read_page"]`) || strings.Contains(selected, `"url"`) {
		t.Fatalf("set selection = %s, %v", selected, err)
	}
	cfg, err := mcp.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Servers["wiki"]
	if !slices.Equal(s.Tools, []string{"read_page"}) || s.Mode() != mcp.ApprovalAuto || !slices.Equal(s.Allow, before.Servers["wiki"].Allow) || s.Pins["read_page"] != before.Servers["wiki"].Pins["read_page"] || s.URL != f.url {
		t.Fatalf("set changed other settings: %+v", s)
	}
	out, err := runMCP(t, "mcp", "tools", "wiki", "--json")
	if err != nil {
		t.Fatal(err)
	}
	data := decodeResults(t, out)
	if data["offered"] != float64(1) || data["filtered"] != float64(2) || data["unsupported"] != float64(0) {
		t.Fatalf("inspection counts = %s", out)
	}
	for _, raw := range data["tools"].([]any) {
		tool := raw.(map[string]any)
		if tool["filtered"] != (tool["name"] != "read_page") {
			t.Fatalf("inspection tool = %v", tool)
		}
	}
	selected, err = runMCP(t, "mcp", "tools", "wiki", "--json", "--select", "filtered,tools.filtered")
	if err != nil || !strings.Contains(selected, `"filtered":2`) || strings.Contains(selected, `"description"`) {
		t.Fatalf("inspection selection = %s, %v", selected, err)
	}
	out, err = runMCP(t, "mcp", "tools", "wiki")
	if err != nil || !strings.Contains(out, "2 filtered out") || !strings.Contains(out, "OFFER") {
		t.Fatalf("human inspection = %s, %v", out, err)
	}
	if _, err := runMCP(t, "mcp", "set", "wiki", "--all-tools"); err != nil {
		t.Fatal(err)
	}
	cfg, err = mcp.Load(path)
	if err != nil || len(cfg.Servers["wiki"].Tools) != 0 {
		t.Fatalf("clear filter = %+v, %v", cfg, err)
	}
	if f.calls.Load() != 0 {
		t.Fatal("configuration/inspection executed a tool")
	}
}

func TestMCPToolSelectionSetRefusalsAtomic(t *testing.T) {
	path := stubMCP(t)
	if _, err := runMCP(t, "mcp", "add", "wiki", "--command", "fixture"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	for _, args := range [][]string{
		{"wiki"}, {"wiki", "--all-tools=false"}, {"wiki", "--tool", "echo", "--all-tools"},
		{"wiki", "--tool", ""}, {"wiki", "--tool", "*"}, {"wiki", "--tool", "echo", "--tool", "echo"},
		{"missing", "--tool", "echo"},
	} {
		if _, err := runMCP(t, append([]string{"mcp", "set"}, args...)...); err == nil {
			t.Fatalf("accepted %v", args)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Fatalf("refused set changed configuration: %v", args)
		}
	}
}
