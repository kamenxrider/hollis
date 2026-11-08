// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestModelsShareCapabilitiesAndLegacyAliases(t *testing.T) {
	executable := runner.LocalStatus{Available: true, Capabilities: &runner.HelperCapabilities{
		Tools: &runner.ToolCapability{Supported: true, Executable: true, TurnHandoff: true},
	}}
	for _, tc := range []struct {
		name   string
		status runner.LocalStatus
		err    error
	}{
		{"available", executable, nil},
		{"no advertisement", runner.LocalStatus{Available: true}, nil},
		{"failed probe", executable, errors.New("probe failed")},
		{"unavailable", runner.LocalStatus{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewUnauthenticated(&goldenCompleteRunner{})
			srv.LocalStatus, srv.LocalStatusError = tc.status, tc.err
			var body struct {
				Data []struct {
					ID           string         `json:"id"`
					Capabilities map[string]any `json:"capabilities"`
				} `json:"data"`
			}
			if err := json.Unmarshal(goldenModelsBody(t, srv), &body); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, row := range body.Data {
				spec, concrete := runner.Spec(runner.Model(row.ID))
				if !concrete {
					continue
				}
				count++
				got := row.Capabilities
				for alias, canonical := range map[string]string{
					"usage": "complete_usage", "streaming_usage": "stream_usage", "image_input": "images",
				} {
					if got[alias] != got[canonical] {
						t.Fatalf("%s alias %s differs from %s", row.ID, alias, canonical)
					}
					delete(got, alias)
				}
				encoded, err := json.Marshal(runner.RouteCapabilities(spec, tc.status, tc.err))
				if err != nil {
					t.Fatal(err)
				}
				var want map[string]any
				if err := json.Unmarshal(encoded, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s HTTP capabilities = %v; shared capabilities = %v", row.ID, got, want)
				}
			}
			if wantCount := len(runner.Models()) + 1; count != wantCount {
				t.Fatalf("checked %d concrete models; want %d", count, wantCount)
			}
		})
	}
}

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
		got := modelCapabilities(id, runner.LocalStatus{}, nil)
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
		if got.ImageInput != wantImages {
			t.Errorf("%s image_input = %v, want %v", id, got.ImageInput, wantImages)
		}
		if string(got.Backend) != wantBackend {
			t.Errorf("%s backend = %v, want %v", id, got.Backend, wantBackend)
		}
		if got.Streaming != wantStreaming {
			t.Errorf("%s streaming = %v, want %v", id, got.Streaming, wantStreaming)
		}
		if got.Usage != wantUsage {
			t.Errorf("%s usage = %v, want %v", id, got.Usage, wantUsage)
		}
		if got.StreamingUsage != wantUsage || got.StreamUsage != wantUsage {
			t.Errorf("%s streaming_usage/stream_usage = %v/%v, want %v", id, got.StreamingUsage, got.StreamUsage, wantUsage)
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
