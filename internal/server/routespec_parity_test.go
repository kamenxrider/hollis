// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// TestRouteSpecCapabilitiesParity pins modelCapabilities, the /v1/models
// tier list and owner strings, and the image limits to the canonical route
// table. When the literal lists migrate to the table (roadmap step 2) this
// test keeps guarding that a table edit and the HTTP contract cannot drift.
func TestRouteSpecCapabilitiesParity(t *testing.T) {
	// The literal tier list at the /v1/models handler must equal the table
	// order with native local appended last.
	wantOrder := []string{"cloud", "cloud-pro", "on-device", "chatgpt", "local"}
	for _, id := range wantOrder {
		if _, ok := runner.Spec(runner.Model(id)); !ok {
			t.Fatalf("%s missing from route table", id)
		}
	}
	specs := runner.Specs()
	if len(specs) != 4 || string(specs[0].ID) != "cloud" || string(specs[3].ID) != "chatgpt" {
		t.Fatalf("table order drifted: %v", specs)
	}
	for _, id := range wantOrder {
		spec, _ := runner.Spec(runner.Model(id))
		if spec.OwnedBy == "" {
			t.Errorf("%s: table owner empty", id)
		}
	}
	if spec, _ := runner.Spec(runner.ModelChatGPT); spec.OwnedBy != "OpenAI" {
		t.Fatalf("chatgpt owner = %q, want %q", spec.OwnedBy, "OpenAI")
	}

	// Capability flags must match the table exactly.
	for _, id := range wantOrder {
		got := modelCapabilities(id)
		m := runner.Model(id)
		wantImages := m.SupportsImages()
		wantBackend := "shortcuts"
		wantStreaming := false
		wantUsage := false
		if id == "local" {
			wantBackend = "native"
			wantStreaming = true
			wantUsage = true
		}
		if got["image_input"] != wantImages {
			t.Errorf("%s image_input = %v, want %v", id, got["image_input"], wantImages)
		}
		if got["backend"] != wantBackend {
			t.Errorf("%s backend = %v, want %v", id, got["backend"], wantBackend)
		}
		if got["streaming"] != wantStreaming {
			t.Errorf("%s streaming = %v, want %v", id, got["streaming"], wantStreaming)
		}
		if got["usage"] != wantUsage {
			t.Errorf("%s usage = %v, want %v", id, got["usage"], wantUsage)
		}
		if got["streaming_usage"] != false {
			t.Errorf("%s streaming_usage = %v, want false", id, got["streaming_usage"])
		}
	}

	// Per-route image limits must match the table rows.
	if MaxCloudImages != 3 || MaxChatGPTImages != 1 {
		t.Fatalf("image limits drifted: cloud=%d chatgpt=%d", MaxCloudImages, MaxChatGPTImages)
	}
	for _, tc := range []struct {
		m    runner.Model
		want int
	}{
		{runner.ModelCloud, MaxCloudImages},
		{runner.ModelCloudPro, MaxCloudImages},
		{runner.ModelChatGPT, MaxChatGPTImages},
	} {
		spec, _ := runner.Spec(tc.m)
		if spec.Capabilities.MaxImages != tc.want {
			t.Errorf("%s MaxImages = %d, want %d", tc.m, spec.Capabilities.MaxImages, tc.want)
		}
	}
}
