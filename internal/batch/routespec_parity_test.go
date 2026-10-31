// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

import (
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// TestRouteSpecSpacingParity pins the batch spacing switch to the canonical
// route table. When the switch migrates to Spec().BatchSpacing (roadmap
// step 2) this test keeps guarding that a table edit and engine behavior
// cannot drift apart.
func TestRouteSpecSpacingParity(t *testing.T) {
	for _, m := range runner.Models {
		spec, ok := runner.Spec(m)
		if !ok {
			t.Fatalf("%s missing from route table", m)
		}
		if got := spacingFor(m); got != spec.BatchSpacing {
			t.Errorf("spacingFor(%s) = %s, table says %s", m, got, spec.BatchSpacing)
		}
	}
	// auto and local take no spacing today; neither has a table row whose
	// spacing could contradict that.
	for _, m := range []runner.Model{runner.ModelAuto, runner.ModelLocal} {
		if got := spacingFor(m); got != 0 {
			t.Errorf("spacingFor(%s) = %s, want 0", m, got)
		}
	}
}
