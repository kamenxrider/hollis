// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// TestRouteSpecDisplayParity pins the CLI display catalog and the config
// show tier order to the canonical route table. When modelCatalog folds
// into the table (roadmap step 2) this test keeps guarding that a table
// edit and CLI output cannot drift apart.
func TestRouteSpecDisplayParity(t *testing.T) {
	if len(modelCatalog) != len(runner.Specs()) {
		t.Fatalf("catalog length = %d, table has %d shortcut tiers", len(modelCatalog), len(runner.Specs()))
	}
	for i, info := range modelCatalog {
		spec, ok := runner.Spec(info.tier)
		if !ok {
			t.Fatalf("catalog tier %s missing from route table", info.tier)
		}
		if runner.Specs()[i].ID != info.tier {
			t.Errorf("catalog position %d = %s, table order says %s", i, info.tier, runner.Specs()[i].ID)
		}
		if info.wfllm != spec.Selection {
			t.Errorf("%s wfllm = %q, table says %q", info.tier, info.wfllm, spec.Selection)
		}
		if info.appleName != spec.Names.Measured {
			t.Errorf("%s appleName = %q, table says %q", info.tier, info.appleName, spec.Names.Measured)
		}
		if info.legacyName != spec.Names.Legacy {
			t.Errorf("%s legacyName = %q, table says %q", info.tier, info.legacyName, spec.Names.Legacy)
		}
		if info.unknownName != spec.Names.Unknown {
			t.Errorf("%s unknownName = %q, table says %q", info.tier, info.unknownName, spec.Names.Unknown)
		}
	}
	// The config show tier list must render in canonical table order.
	wantOrder := []string{"cloud", "cloud-pro", "on-device", "chatgpt"}
	for i, tier := range wantOrder {
		if runner.Specs()[i].ID != runner.Model(tier) {
			t.Fatalf("config show order position %d = %s, table says %s", i, tier, runner.Specs()[i].ID)
		}
	}
	// Doctor iterates runner.Models: it must equal the table order.
	for i, m := range runner.Models {
		if runner.Specs()[i].ID != m {
			t.Fatalf("runner.Models position %d = %s, table says %s", i, m, runner.Specs()[i].ID)
		}
	}
}
