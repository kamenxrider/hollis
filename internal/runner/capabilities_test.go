// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestCapabilityJSONNames(t *testing.T) {
	encoded, err := json.Marshal(Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	want := []string{"backend", "complete_usage", "guided_output", "images", "max_images", "stream_usage", "streaming", "tools"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("JSON names = %v; want %v", names, want)
	}
}

func TestRouteStreamUsageLocalOnly(t *testing.T) {
	local, _ := Spec(ModelLocal)
	for _, status := range []LocalStatus{{}, {Available: true}} {
		for _, spec := range append(Specs(), local) {
			got := RouteCapabilities(spec, status, nil)
			if got.StreamUsage != (spec.ID == ModelLocal) {
				t.Fatalf("%s stream_usage=%v", spec.ID, got.StreamUsage)
			}
			if got.StreamUsage && (!got.Streaming || !got.CompleteUsage) {
				t.Fatalf("%s stream usage without streaming and measured usage: %+v", spec.ID, got)
			}
		}
	}
}

func TestRouteToolsRequireExecutableHandoff(t *testing.T) {
	local, _ := Spec(ModelLocal)
	for _, tc := range []struct {
		name   string
		status LocalStatus
		err    error
		want   bool
	}{
		{"absent", LocalStatus{Available: true}, nil, false},
		{"unsupported", LocalStatus{Available: true, Capabilities: &HelperCapabilities{Tools: &ToolCapability{Executable: true, TurnHandoff: true}}}, nil, false},
		{"no execution", LocalStatus{Available: true, Capabilities: &HelperCapabilities{Tools: &ToolCapability{Supported: true, TurnHandoff: true}}}, nil, false},
		{"no handoff", LocalStatus{Available: true, Capabilities: &HelperCapabilities{Tools: &ToolCapability{Supported: true, Executable: true}}}, nil, false},
		{"complete", LocalStatus{Available: true, Capabilities: &HelperCapabilities{Tools: &ToolCapability{Supported: true, Executable: true, TurnHandoff: true}}}, nil, true},
		{"unavailable", LocalStatus{Capabilities: &HelperCapabilities{Tools: &ToolCapability{Supported: true, Executable: true, TurnHandoff: true}}}, nil, false},
		{"failed probe", LocalStatus{Available: true, Capabilities: &HelperCapabilities{Tools: &ToolCapability{Supported: true, Executable: true, TurnHandoff: true}}}, errors.New("failed probe"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RouteCapabilities(local, tc.status, tc.err); got.Tools != tc.want {
				t.Fatalf("tools = %v; want %v", got.Tools, tc.want)
			}
			for _, spec := range Specs() {
				if got := RouteCapabilities(spec, tc.status, tc.err); got.Tools {
					t.Fatalf("%s must never advertise tools", spec.ID)
				}
			}
		})
	}
}
