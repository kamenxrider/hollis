// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestHTTPJSONBridgeIntegration(t *testing.T) {
	for _, spec := range runner.Specs() {
		for _, installed := range []bool{false, true} {
			for _, endpoint := range []string{"chat", "responses"} {
				for _, raw := range []string{`{"price":19.949999999999999}`, "prose\n" + strings.Repeat("x", 150), ""} {
					t.Run(string(spec.ID)+"/"+endpoint+fmt.Sprint(installed)+"/"+fmt.Sprint(len(raw)), func(t *testing.T) {
						dir := t.TempDir()
						t.Setenv("JSON_CAPTURE", filepath.Join(dir, "argv"))
						t.Setenv("JSON_RESULT", raw)
						path := filepath.Join(dir, "shortcuts")
						if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$JSON_CAPTURE\"\nprintf '%s' \"$JSON_RESULT\"\n"), 0o700); err != nil {
							t.Fatal(err)
						}
						r := runner.New()
						r.ShortcutsPath = path
						r.BridgeRefs = map[runner.Model]string{spec.ID: "opaque-text"}
						r.JSONBridgeRefs = map[runner.Model]string{}
						r.JSONDiscoveryKnown = true
						if installed {
							r.JSONBridgeRefs[spec.ID] = "opaque-dictionary"
						}
						body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Return an object."}],"response_format":{"type":"json_object"}}`, spec.ID)
						url := "/v1/chat/completions"
						if endpoint == "responses" {
							url = "/v1/responses"
							body = fmt.Sprintf(`{"model":%q,"input":"Return an object.","text":{"format":{"type":"json_object"}}}`, spec.ID)
						}
						res := post(t, NewUnauthenticated(r).Handler(), url, body)
						want := 502
						if strings.HasPrefix(raw, "{") {
							want = 200
						}
						if res.Code != want || (!installed && !strings.Contains(res.Body.String(), "json_bridge_hint")) ||
							(want == 200 && !strings.Contains(res.Body.String(), "19.949999999999999")) {
							t.Fatalf("status=%d body=%s", res.Code, res.Body)
						}
						argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
						wire, ref := "public.plain-text", "opaque-text"
						if installed {
							wire, ref = "public.json", "opaque-dictionary"
						}
						if !strings.HasPrefix(string(argv), "run\n"+ref+"\n--output-type\n"+wire+"\n") {
							t.Fatalf("argv=%s", argv)
						}
					})
				}
			}
		}
	}
}
