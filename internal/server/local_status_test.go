// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// wantMismatchMessage is the literal HTTP sentence for a preserved helper
// mismatch, written out here so a change to the production constant is a test
// failure rather than a silently matching pair.
const wantMismatchMessage = "the native local helper does not match this Hollis version; install the matching bundle"

// probeProtocolMismatch is what the startup status probe returns for a helper
// whose protocol or version disagrees with this CLI. It is a runner.Error of
// kind native_protocol, the same class the CLI reports with exit 5.
func probeProtocolMismatch() error {
	return &runner.Error{Kind: runner.KindNativeProtocol, ExitCode: 5,
		Err: errors.New("native helper version, protocol or model mismatch; install the matching bundle")}
}

// probeMissingHelper is an availability fact, not a mismatch: the helper was
// not installed or not executable.
func probeMissingHelper() error {
	return &runner.Error{Kind: runner.KindLocalUnavailable, ExitCode: 3,
		Err: errors.New("native local helper is missing or not executable; install the matching Hollis Apple Silicon bundle")}
}

func localStatusServer(r runner.Runner, probeErr error) *Server {
	s := NewUnauthenticated(r)
	s.Available = map[string]bool{
		"local": false, "cloud": false, "cloud-pro": false, "on-device": false, "chatgpt": false,
	}
	s.LocalStatusError = probeErr
	return s
}

// assertErrorBody pins the whole error object, not just its code: the message
// is part of the contract callers use to tell a component mismatch from a bad
// payload.
func assertErrorBody(t *testing.T, res *httptest.ResponseRecorder, wantType, wantCode, wantMessage string) {
	t.Helper()
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("error JSON: %v (%s)", err, res.Body.String())
	}
	if body.Error.Type != wantType || body.Error.Code != wantCode || body.Error.Message != wantMessage {
		t.Fatalf("error=%+v want type=%s code=%s message=%q", body.Error, wantType, wantCode, wantMessage)
	}
}

func TestLocalRequestsReportPreservedProtocolMismatch(t *testing.T) {
	for path, body := range guidedBodies(guidedTestSchema) {
		t.Run("guided "+strings.TrimPrefix(path, "/v1/"), func(t *testing.T) {
			fake := newGuidedFake()
			res := post(t, localStatusServer(fake, probeProtocolMismatch()).Handler(), path, body)
			if res.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertErrorBody(t, res, "server_error", "native_protocol", wantMismatchMessage)
			if schemaCalls, completeCalls, _ := fake.snapshot(); schemaCalls != 0 || completeCalls != 0 {
				t.Fatalf("runner calls schema=%d complete=%d", schemaCalls, completeCalls)
			}
		})
	}
	for _, endpoint := range nativeEndpoints {
		for _, stream := range []bool{false, true} {
			name := "plain "
			if stream {
				name = "stream "
			}
			t.Run(name+strings.TrimPrefix(endpoint.path, "/v1/"), func(t *testing.T) {
				fake := &nativeFake{}
				res := post(t, localStatusServer(fake, probeProtocolMismatch()).Handler(), endpoint.path, nativeBody(endpoint.body, stream))
				if res.Code != http.StatusBadGateway {
					t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
				}
				assertErrorBody(t, res, "server_error", "native_protocol", wantMismatchMessage)
				if calls := fake.calls.Load(); calls != 0 {
					t.Fatalf("runner calls = %d", calls)
				}
			})
		}
	}
}

func TestLocalUnavailabilityWithoutMismatchStaysModelUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		probeErr error
	}{
		{"missing helper", probeMissingHelper()},
		{"probe failed", &runner.Error{Kind: runner.KindNativeFailed, ExitCode: 4, Err: errors.New("native helper could not report its supported protocol")}},
		{"no probe", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for path, body := range guidedBodies(guidedTestSchema) {
				fake := newGuidedFake()
				res := post(t, localStatusServer(fake, tc.probeErr).Handler(), path, body)
				if res.Code != http.StatusBadRequest {
					t.Fatalf("guided %s status=%d body=%s", path, res.Code, res.Body.String())
				}
				assertAPIError(t, res, "invalid_request_error", "model_unavailable")
				if schemaCalls, _, _ := fake.snapshot(); schemaCalls != 0 {
					t.Fatalf("guided %s reached the runner", path)
				}
			}
			for _, endpoint := range nativeEndpoints {
				fake := &nativeFake{}
				res := post(t, localStatusServer(fake, tc.probeErr).Handler(), endpoint.path, nativeBody(endpoint.body, false))
				if res.Code != http.StatusBadRequest {
					t.Fatalf("plain %s status=%d body=%s", endpoint.path, res.Code, res.Body.String())
				}
				assertAPIError(t, res, "invalid_request_error", "model_unavailable")
				if calls := fake.calls.Load(); calls != 0 {
					t.Fatalf("plain %s reached the runner", endpoint.path)
				}
			}
		})
	}
}

// A preserved mismatch is the helper's diagnosis, so it wins over the
// construction-time 503 for a runner that has no schema interface. In
// production the routed runner always has it; the ordering is pinned so a
// future embedding cannot report the wrong failure first.
func TestPreservedMismatchWinsOverTheMissingSchemaInterface(t *testing.T) {
	plain := &echoRunner{}
	for path, body := range guidedBodies(guidedTestSchema) {
		res := post(t, localStatusServer(plain, probeProtocolMismatch()).Handler(), path, body)
		if res.Code != http.StatusBadGateway {
			t.Fatalf("%s status=%d body=%s", path, res.Code, res.Body.String())
		}
		assertErrorBody(t, res, "server_error", "native_protocol", wantMismatchMessage)
	}
	// Without a preserved mismatch the same runner is the 503 configuration
	// failure it always was.
	for path, body := range guidedBodies(guidedTestSchema) {
		res := post(t, localStatusServer(plain, probeMissingHelper()).Handler(), path, body)
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status=%d body=%s", path, res.Code, res.Body.String())
		}
		assertAPIError(t, res, "server_error", "model_unavailable")
	}
}

// A preserved local mismatch must not leak onto other routes: their
// availability contract is unchanged.
func TestOtherRoutesIgnoreTheLocalProbeError(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		fake := &nativeFake{}
		body := strings.Replace(nativeBody(endpoint.body, false), `"model":"local"`, `"model":"cloud"`, 1)
		res := post(t, localStatusServer(fake, probeProtocolMismatch()).Handler(), endpoint.path, body)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", endpoint.path, res.Code, res.Body.String())
		}
		assertAPIError(t, res, "invalid_request_error", "model_unavailable")
		if calls := fake.calls.Load(); calls != 0 {
			t.Fatalf("%s reached the runner", endpoint.path)
		}
	}
}
