// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type limitedRunner struct{ calls int }

func (r *limitedRunner) Run(_ context.Context, model runner.Model, _ string) (string, runner.Model, error) {
	r.calls++
	return "", model, &runner.Error{Kind: runner.KindRateLimited, Err: errors.New("PRIVATE CANARY"), Stderr: "PRIVATE CANARY"}
}

func TestProviderRateLimitHasNoInventedReset(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		t.Run(endpoint.path, func(t *testing.T) {
			fake := &limitedRunner{}
			body := strings.ReplaceAll(nativeBody(endpoint.body, false), `"local"`, `"cloud"`)
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
			if res.Code != 429 || res.Header().Get("Retry-After") != "" || fake.calls != 1 {
				t.Fatalf("status=%d retry=%q calls=%d", res.Code, res.Header().Get("Retry-After"), fake.calls)
			}
			assertAPIError(t, res, "rate_limit_error", "rate_limited")
			if strings.Contains(res.Body.String(), "CANARY") {
				t.Fatal("private failure leaked")
			}
		})
	}
}

func TestNativeRateLimitBeforeAndAfterStreamStart(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		for _, phase := range []string{"complete", "before stream", "after delta"} {
			t.Run(endpoint.path+phase, func(t *testing.T) {
				failure := &runner.Error{Kind: runner.KindRateLimited, Err: errors.New("PRIVATE CANARY"), Stderr: "PRIVATE CANARY"}
				fake := &nativeFake{
					complete: func(context.Context, string) (runner.Completion, error) { return runner.Completion{}, failure },
					stream: func(_ context.Context, _ string, emit func(string) error) (runner.Completion, error) {
						if phase == "after delta" {
							if err := emit("partial"); err != nil {
								return runner.Completion{}, err
							}
						}
						return runner.Completion{}, failure
					},
				}
				srv := NewUnauthenticated(fake)
				res := post(t, srv.Handler(), endpoint.path, nativeBody(endpoint.body, phase != "complete"))
				wantStatus := 429
				if phase == "after delta" {
					wantStatus = 200
				}
				body := res.Body.String()
				if res.Code != wantStatus || res.Header().Get("Retry-After") != "" || !strings.Contains(body, `"code":"rate_limited"`) || strings.Contains(body, "CANARY") {
					t.Fatalf("status=%d retry=%q body=%s", res.Code, res.Header().Get("Retry-After"), body)
				}
				if phase == "after delta" && (strings.Contains(body, "response.completed") || strings.Contains(body, "[DONE]") || strings.Contains(body, `"finish_reason":"stop"`)) {
					t.Fatalf("rate-limited stream claimed success: %s", body)
				}
				if fake.calls.Load() != 1 || len(srv.slots()) != 0 {
					t.Fatal("retried or leaked capacity")
				}
			})
		}
	}
}

func TestLocalBusyRetainsHintWithoutProviderDispatch(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		for _, native := range []bool{false, true} {
			t.Run(endpoint.path+map[bool]string{true: "native", false: "shortcuts"}[native], func(t *testing.T) {
				local := &nativeFake{}
				shortcuts := &limitedRunner{}
				var backend runner.Runner = shortcuts
				body := strings.ReplaceAll(nativeBody(endpoint.body, false), `"local"`, `"cloud"`)
				if native {
					backend = local
					body = nativeBody(endpoint.body, true)
				}
				srv := NewUnauthenticated(backend)
				srv.slots() <- struct{}{}
				res := post(t, srv.Handler(), endpoint.path, body)
				<-srv.slots()
				if res.Code != 429 || res.Header().Get("Retry-After") != "1" {
					t.Fatalf("status=%d retry=%q", res.Code, res.Header().Get("Retry-After"))
				}
				assertAPIError(t, res, "rate_limit_error", "server_busy")
				if local.calls.Load() != 0 || shortcuts.calls != 0 {
					t.Fatal("busy request dispatched")
				}
			})
		}
	}
}

func TestClientStopsOnUnknownProviderReset(t *testing.T) {
	fake := &limitedRunner{}
	server := httptest.NewServer(NewUnauthenticated(fake).Handler())
	defer server.Close()
	// Simulate a client that retries only explicitly timed local contention.
	// Unknown provider resets must not enter this bounded retry path.
	for attempt := 0; attempt < 3; attempt++ {
		res, err := server.Client().Post(server.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"cloud","input":"fixture"}`))
		if err != nil {
			t.Fatal(err)
		}
		var payload struct{ Error struct{ Code string } }
		err = json.NewDecoder(res.Body).Decode(&payload)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if payload.Error.Code == "rate_limited" && res.Header.Get("Retry-After") == "" {
			break
		}
	}
	if fake.calls != 1 {
		t.Fatalf("client tight-looped %d provider calls", fake.calls)
	}
}
