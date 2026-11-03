// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

const precisionPayload = `{"decimal":0.123456789012345678901,"integer":9007199254740993,"nested":[-9007199254740993,1e-400,-0,1.2300E+12]}`

func TestStructuredPrecisionTransports(t *testing.T) {
	for name, payload := range map[string]string{"mixed": precisionPayload, "overflow": "1e400"} {
		for _, transport := range []string{"stdio", "http-json", "http-sse"} {
			t.Run(name+"/"+transport, func(t *testing.T) {
				var srv Server
				tool, args := "precision", json.RawMessage(`{}`)
				var fixture *httpFixture
				if transport == "stdio" {
					srv, _ = fixtureServer(t, "precision", "variant="+name)
				} else {
					fixture = &httpFixture{sse: transport == "http-sse", sessionID: "precision-session", structured: json.RawMessage(payload)}
					srv, _ = httpServer(t, fixture)
					tool, args = "echo", json.RawMessage(`{"text":"x"}`)
				}
				m := attach(t, "fixture", srv, interactive())
				for i := 0; i < 2; i++ {
					result, err := m.Call(callCtx(t), "fixture", tool, args)
					if err != nil {
						t.Fatal(err)
					}
					want := structuredLabel + "\n" + payload
					if result.Text != want || result.Bytes != len(want) {
						t.Fatalf("structured result = %q; want %q", result.Text, want)
					}
					if strings.Contains(result.Text, "9007199254740992") {
						t.Fatal("integer rounded")
					}
				}
				m.Close()
				if fixture != nil && fixture.deletes.Load() != 1 {
					t.Fatal("HTTP session teardown changed")
				}
				if fixture != nil {
					for _, req := range fixture.recorded() {
						if req.RPC == "tools/call" && req.Protocol != "2025-06-18" {
							t.Fatalf("negotiated protocol header = %q", req.Protocol)
						}
					}
				}
			})
		}
	}
}
