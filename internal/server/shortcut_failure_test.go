// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestShortcutTypedHTTPFailures(t *testing.T) {
	for kind, status := range map[runner.Kind]int{runner.KindRateLimited: 429, runner.KindContentRefused: 422, runner.KindAccessRequired: 403} {
		for _, endpoint := range nativeEndpoints {
			fake := &echoRunner{err: &runner.Error{Kind: kind}}
			body := strings.ReplaceAll(nativeBody(endpoint.body, false), `"local"`, `"cloud"`)
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
			if res.Code != status || !strings.Contains(res.Body.String(), `"code":"`+string(kind)+`"`) || res.Header().Get("Retry-After") != "" {
				t.Fatalf("%d %s", res.Code, res.Body.String())
			}
		}
	}
}

func TestHTTPPreservesSanitizedAppleText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shortcuts")
	diagnostic := "Error: You have reached the usage limit for this model. Please try again later."
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '"+diagnostic+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := runner.New()
	r.ShortcutsPath = path
	_, _, err := r.Run(context.Background(), runner.ModelCloud, "request-canary")
	res := post(t, NewUnauthenticated(&echoRunner{err: err}).Handler(), "/v1/responses", `{"model":"cloud","input":"request-canary"}`)
	if res.Code != 429 || !strings.Contains(res.Body.String(), diagnostic) || !strings.Contains(res.Body.String(), `"apple_stdout"`) || strings.Contains(res.Body.String(), "request-canary") {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
}
