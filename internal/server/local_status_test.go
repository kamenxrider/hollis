// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

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

func TestLocalRequestsReportPreservedProtocolMismatch(t *testing.T) {
	const nativeProtocolMessage = "the native local helper returned an invalid or incomplete response"
	for path, body := range guidedBodies(guidedTestSchema) {
		t.Run("guided "+strings.TrimPrefix(path, "/v1/"), func(t *testing.T) {
			fake := newGuidedFake()
			res := post(t, localStatusServer(fake, probeProtocolMismatch()).Handler(), path, body)
			if res.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertAPIError(t, res, "server_error", "native_protocol")
			if !strings.Contains(res.Body.String(), nativeProtocolMessage) {
				t.Fatalf("body=%s; want the documented native_protocol sentence", res.Body.String())
			}
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
				assertAPIError(t, res, "server_error", "native_protocol")
				if !strings.Contains(res.Body.String(), nativeProtocolMessage) {
					t.Fatalf("body=%s; want the documented native_protocol sentence", res.Body.String())
				}
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
