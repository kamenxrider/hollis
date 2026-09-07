// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
)

func TestFailureContractAllHTTPEndpoints(t *testing.T) {
	for _, code := range []string{"request_declined", "shortcut_failed", "transport"} {
		for _, tc := range []struct {
			path, body string
			image      bool
		}{
			{"/v1/chat/completions", `{"model":"cloud","messages":[{"role":"user","content":"fixture"}]}`, false},
			{"/v1/responses", `{"model":"cloud","input":"fixture"}`, false},
			{"/v1/images/generations", `{"prompt":"fixture","style":"illustration"}`, true},
			{"/v1/chat/completions", `{"model":"hollis-image","messages":[{"role":"user","content":"fixture"}],"image_generation":{}}`, true},
			{"/v1/responses", `{"model":"hollis-image","input":"fixture","image_generation":{}}`, true},
		} {
			t.Run(code+tc.path+tc.body, func(t *testing.T) {
				private := errors.New("/Users/private/customer.txt token=TEST_SECRET_CANARY")
				srv := NewUnauthenticated(&echoRunner{err: &runner.Error{Kind: runner.Kind(code), Err: private, ExitCode: 1}})
				var gen *recordingGenerator
				if tc.image {
					kind := imagegen.KindRequestDeclined
					if code == "shortcut_failed" {
						kind = imagegen.KindNonZeroExit
					}
					if code == "transport" {
						kind = imagegen.KindSpawn
					}
					gen = &recordingGenerator{err: &imagegen.Error{Kind: kind, Err: private, ExitCode: 1}}
					srv = testUnifiedGenerationServer(gen, "")
				}
				res := post(t, srv.Handler(), tc.path, tc.body)
				var payload struct {
					Error struct{ Code, Message, Type string }
				}
				if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if res.Code != http.StatusBadGateway || payload.Error.Code != code || payload.Error.Type != "server_error" {
					t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
				}
				if strings.Contains(res.Body.String(), "private") || strings.Contains(res.Body.String(), "CANARY") {
					t.Fatal("private diagnostic leaked")
				}
				if gen != nil && gen.callCount() != 1 {
					t.Fatalf("calls=%d", gen.callCount())
				}
			})
		}
	}
}
