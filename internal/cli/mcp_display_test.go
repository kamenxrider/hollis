package cli

import (
	"strings"
	"testing"
)

func TestMCPURLViewsOmitPathAndQuery(t *testing.T) {
	for _, mode := range []string{"", "--json", "--agent"} {
		stubMCP(t)
		args := []string{"mcp", "add", "synthetic", "--url", "https://example.test:8443/private-path-canary?region=query-canary"}
		if mode != "" {
			args = append(args, mode)
		}
		added, err := runMCP(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		args = []string{"mcp", "list"}
		if mode != "" {
			args = append(args, mode)
		}
		listed, err := runMCP(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, output := range []string{added, listed} {
			if strings.Contains(output, "private-path-canary") || strings.Contains(output, "query-canary") {
				t.Fatalf("URL components disclosed: %s", output)
			}
		}
		if !strings.Contains(listed, "https://example.test:8443") {
			t.Fatalf("origin missing: %s", listed)
		}
	}
}

func TestMCPTableCellEscapesNewlines(t *testing.T) {
	if got := tableCell("line\nnext\tcell"); got != `line\nnext\tcell` {
		t.Fatalf("table cell=%q", got)
	}
}
