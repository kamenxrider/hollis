// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

// Route output goldens lock the /v1/models body that the RouteSpec table
// must preserve byte-for-byte (roadmap §13.2, issues #18/#20/#30): the id
// list, ordering, owned_by values, and capability flags. Fixtures were
// generated from unmodified main (b8b7c3a) before any route-table code
// existed; regenerating after a refactor proves nothing, so treat
// regeneration as a deliberate contract change.

import (
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
)

var updateRouteGoldens = flag.Bool("update-route-goldens", false, "rewrite route output golden fixtures from current behavior")

// goldenCompleteRunner satisfies both Runner and CompleteRunner so the
// native local tier passes the CompleteRunner availability gate.
type goldenCompleteRunner struct{}

func (goldenCompleteRunner) Run(_ context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	return prompt, m, nil
}

func (goldenCompleteRunner) RunComplete(_ context.Context, m runner.Model, prompt string) (runner.Completion, error) {
	return runner.Completion{Text: prompt, Model: m}, nil
}

type goldenImageGenerator struct{}

func (goldenImageGenerator) Generate(context.Context, imagegen.Request) (imagegen.Result, error) {
	return imagegen.Result{}, nil
}

func goldenModelsBody(t *testing.T, srv *Server) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/v1/models status = %d, want 200", res.Code)
	}
	return res.Body.Bytes()
}

func compareRouteGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "goldens", name)
	if *updateRouteGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden fixture missing for %s (regenerate deliberately with -update-route-goldens): %v", name, err)
	}
	if want == nil || string(want) != string(got) {
		t.Fatalf("route output drift in %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func TestRouteModelsGolden(t *testing.T) {
	t.Run("all_available", func(t *testing.T) {
		// Every tier available, local backed by a CompleteRunner, image
		// route configured: the maximal /v1/models body.
		srv := NewUnauthenticated(&goldenCompleteRunner{})
		srv.ImageGenerator = goldenImageGenerator{}
		srv.ImageBridge = "Hollis Image Unified"
		compareRouteGolden(t, "server_models_all.json", goldenModelsBody(t, srv))
	})
	t.Run("gated", func(t *testing.T) {
		// Fail-closed availability: only cloud and on-device offered, no
		// image route, runner without native completion (no local row).
		srv := NewUnauthenticated(&echoRunner{})
		srv.Available = map[string]bool{"cloud": true, "on-device": true}
		compareRouteGolden(t, "server_models_gated.json", goldenModelsBody(t, srv))
	})
}
