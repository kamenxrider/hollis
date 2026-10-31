// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"reflect"
	"testing"
	"time"
)

// TestRouteSpecParityWithCurrentConstants pins every table row to the
// values the current consumers hardcode, so the table can only drift by
// failing these tests, never silently.
func TestRouteSpecParityWithCurrentConstants(t *testing.T) {
	wantSelection := map[Model]string{
		ModelCloud:    "Apple Intelligence",
		ModelCloudPro: "Apple Intelligence Pro",
		ModelOnDevice: "Apple Intelligence on Device",
		ModelChatGPT:  "ChatGPT",
	}
	wantNames := map[Model]NameSet{
		ModelCloud: {
			Measured: "AFM 3 Cloud (Private Cloud Compute)",
			Legacy:   "Apple Intelligence cloud on Private Cloud Compute (pre-27; not AFM 3)",
			Unknown:  "Apple Intelligence cloud (macOS version unknown)",
		},
		ModelCloudPro: {
			Measured: "AFM 3 Cloud Pro (Private Cloud Compute; macOS 27+)",
			Legacy:   "Cloud Pro is not offered on macOS 26",
			Unknown:  "Apple Intelligence Cloud Pro (requires macOS 27; macOS version unknown)",
		},
		ModelOnDevice: {
			Measured: "AFM 3 Core or AFM 3 Core Advanced (by hardware)",
			Legacy:   "Apple Intelligence on-device model (pre-27)",
			Unknown:  "Apple Intelligence on-device model (macOS version unknown)",
		},
		ModelChatGPT: {
			Measured: "OpenAI ChatGPT extension for Apple Intelligence",
			Legacy:   "OpenAI ChatGPT extension for Apple Intelligence",
			Unknown:  "OpenAI ChatGPT extension for Apple Intelligence",
		},
	}
	for _, m := range routeOrder {
		spec, ok := Spec(m)
		if !ok {
			t.Fatalf("%s missing from table", m)
		}
		if spec.Selection != wantSelection[m] {
			t.Errorf("%s selection = %q, want %q", m, spec.Selection, wantSelection[m])
		}
		if !reflect.DeepEqual(spec.Names, wantNames[m]) {
			t.Errorf("%s names = %+v, want %+v", m, spec.Names, wantNames[m])
		}
		if want := BridgeNameCandidates[m]; !reflect.DeepEqual(spec.BridgeNames, want) {
			t.Errorf("%s bridge names = %v, want %v", m, spec.BridgeNames, want)
		}
		if want := CompiledUUID(m); spec.CompiledUUID != want {
			t.Errorf("%s compiled UUID = %q, want %q", m, spec.CompiledUUID, want)
		}
		if m == ModelCloudPro {
			if spec.MinOSMajor != MeasuredOSMajor {
				t.Errorf("cloud-pro MinOSMajor = %d, want %d", spec.MinOSMajor, MeasuredOSMajor)
			}
		} else if spec.MinOSMajor != 0 {
			t.Errorf("%s MinOSMajor = %d, want 0", m, spec.MinOSMajor)
		}
		wantOwned := "Apple"
		if m == ModelChatGPT {
			wantOwned = "OpenAI"
		}
		if spec.OwnedBy != wantOwned {
			t.Errorf("%s owned_by = %q, want %q", m, spec.OwnedBy, wantOwned)
		}
		// Spacing parity with batch spacingFor: 45s cloud-pro, 15s cloud
		// and chatgpt, 0 otherwise. Enforced cross-package in
		// internal/batch; asserted here on the table values.
		wantSpacing := map[Model]time.Duration{
			ModelCloud:    15 * time.Second,
			ModelCloudPro: 45 * time.Second,
			ModelOnDevice: 0,
			ModelChatGPT:  15 * time.Second,
		}
		if spec.BatchSpacing != wantSpacing[m] {
			t.Errorf("%s batch spacing = %s, want %s", m, spec.BatchSpacing, wantSpacing[m])
		}
	}
	local, ok := Spec(ModelLocal)
	if !ok {
		t.Fatal("local missing from table")
	}
	if local.Transport != TransportNative || local.Selection != "" || len(local.BridgeNames) != 0 || local.CompiledUUID != "" {
		t.Fatalf("native local row drifted: %+v", local)
	}
	wantLocalCap := CapabilitySet{Streaming: true, Usage: true}
	if local.Capabilities != wantLocalCap {
		t.Errorf("local capabilities = %+v, want %+v", local.Capabilities, wantLocalCap)
	}
	wantImageCap := map[Model]CapabilitySet{
		ModelCloud:    {Images: true, MaxImages: 3},
		ModelCloudPro: {Images: true, MaxImages: 3},
		ModelOnDevice: {},
		ModelChatGPT:  {Images: true, MaxImages: 1},
	}
	for m, want := range wantImageCap {
		spec, _ := Spec(m)
		if spec.Capabilities != want {
			t.Errorf("%s capabilities = %+v, want %+v", m, spec.Capabilities, want)
		}
	}
}

// TestValidSingleLookupParity checks Valid() against the selectable
// vocabulary: auto, every table tier, and rejection of unknowns.
func TestValidSingleLookupParity(t *testing.T) {
	if !ModelAuto.Valid() {
		t.Error("auto must stay valid")
	}
	for _, m := range Models {
		if !m.Valid() {
			t.Errorf("%s must stay valid", m)
		}
	}
	if !ModelLocal.Valid() {
		t.Error("local must stay valid")
	}
	for _, m := range []Model{"bogus", "", "Cloud", "cloud ", "fm"} {
		if m.Valid() {
			t.Errorf("%q must be invalid", m)
		}
	}
}

func TestModelsDerivedFromTable(t *testing.T) {
	want := []Model{ModelCloud, ModelCloudPro, ModelOnDevice, ModelChatGPT}
	if !reflect.DeepEqual(Models, want) {
		t.Fatalf("Models = %v, want %v", Models, want)
	}
	specs := Specs()
	if len(specs) != len(want) {
		t.Fatalf("Specs length = %d, want %d", len(specs), len(want))
	}
	for i, spec := range specs {
		if spec.ID != want[i] {
			t.Errorf("Specs[%d] = %s, want %s", i, spec.ID, want[i])
		}
	}
	// routeOrderSnapshot hands out an independent copy each call: mutating
	// one caller's slice must not corrupt the canonical table order that
	// every other reader (Models, Specs) sees.
	snapshot := routeOrderSnapshot()
	snapshot[0] = ModelLocal
	if Specs()[0].ID != ModelCloud || Models[0] != ModelCloud {
		t.Fatal("table order mutated through a snapshot")
	}
}

func TestSupportsImagesRules(t *testing.T) {
	for _, tc := range []struct {
		m    Model
		want bool
	}{
		{ModelCloud, true}, {ModelCloudPro, true}, {ModelChatGPT, true},
		{ModelOnDevice, false}, {ModelAuto, false}, {ModelLocal, false},
		{Model("bogus"), false}, {Model(""), false},
	} {
		if got := tc.m.SupportsImages(); got != tc.want {
			t.Errorf("%s.SupportsImages() = %v, want %v", tc.m, got, tc.want)
		}
	}
}

func TestSpecRejectsAutoAndUnknown(t *testing.T) {
	if _, ok := Spec(ModelAuto); ok {
		t.Fatal("auto must not have a route row")
	}
	if _, ok := Spec(Model("bogus")); ok {
		t.Fatal("unknown model must not have a route row")
	}
}

func TestValidateRouteTable(t *testing.T) {
	if err := validateRouteTable(); err != nil {
		t.Fatalf("canonical table invalid: %v", err)
	}
	restore := func() { /* set after each corruption */ }
	_ = restore
	orig, had := routeSpecs[ModelChatGPT]
	if !had {
		t.Fatal("chatgpt row missing")
	}
	corrupt := func(mutate func(*RouteSpec)) error {
		spec := orig
		mutate(&spec)
		routeSpecs[ModelChatGPT] = spec
		err := validateRouteTable()
		routeSpecs[ModelChatGPT] = orig
		return err
	}
	if err := corrupt(func(s *RouteSpec) { s.Selection = "" }); err == nil {
		t.Error("empty Selection accepted")
	}
	if err := corrupt(func(s *RouteSpec) { s.Capabilities.MaxImages = 0 }); err == nil {
		t.Error("Images with MaxImages=0 accepted")
	}
	if err := corrupt(func(s *RouteSpec) { s.CompiledUUID = "" }); err == nil {
		t.Error("missing compiled UUID accepted")
	}
	if err := corrupt(func(s *RouteSpec) { s.Names.Measured = "" }); err == nil {
		t.Error("incomplete display names accepted")
	}
	if err := corrupt(func(s *RouteSpec) { s.OwnedBy = "" }); err == nil {
		t.Error("missing owner accepted")
	}
	if err := validateRouteTable(); err != nil {
		t.Fatalf("table did not restore cleanly: %v", err)
	}
}
