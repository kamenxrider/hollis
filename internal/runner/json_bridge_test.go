// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestJSONBridgeTransport(t *testing.T) {
	for _, spec := range Specs() {
		for _, installed := range []bool{false, true} {
			for _, jsonRequest := range []bool{false, true} {
				r, dir := runnerWithFake(t, "echo")
				r.BridgeRefs = map[Model]string{spec.ID: "text-reference"}
				r.JSONBridgeRefs = map[Model]string{}
				if installed {
					// The deliberately opaque name proves transport is capability
					// driven, not guessed from a bridge reference's spelling.
					r.JSONBridgeRefs[spec.ID] = "opaque-reference"
				}
				ctx := context.Background()
				if jsonRequest {
					ctx = WithJSONObject(ctx)
				}
				raw := `{"price":19.949999999999999}`
				got, used, err := r.Run(ctx, spec.ID, raw)
				if err != nil || got != raw || used != spec.ID {
					t.Fatalf("got=%q used=%s err=%v", got, used, err)
				}
				argv, err := os.ReadFile(dir + "/argv-lines.txt")
				if err != nil {
					t.Fatal(err)
				}
				ref, wire := "text-reference", "public.plain-text"
				if installed && jsonRequest {
					ref, wire = "opaque-reference", "public.json"
				}
				if !strings.HasPrefix(string(argv), "run\n"+ref+"\n--output-type\n"+wire+"\n--input-path\n") {
					t.Fatalf("argv=%s", argv)
				}
			}
		}
	}
}

func TestJSONBridgeDiscovery(t *testing.T) {
	for _, spec := range Specs() {
		names := []string{spec.JSONBridge.Names[1]}
		resolved := ResolveBridges(names, true, 27, nil)
		if got := resolved[spec.ID]; got.JSONRef != names[0] || got.Available {
			t.Fatalf("JSON-only discovery changed text readiness: %+v", got)
		}
		if got := ResolveBridges(names, false, 27, nil)[spec.ID]; got.JSONRef != "" || got.JSONDiscoveryKnown {
			t.Fatalf("failed listing must not discover JSON: %+v", got)
		}
	}
	if got := ResolveBridges([]string{"AFM Bridge JSON - Cloud Pro"}, true, 26, nil)[ModelCloudPro]; got.JSONRef != "" {
		t.Fatalf("unsupported Pro discovered: %+v", got)
	}
}
