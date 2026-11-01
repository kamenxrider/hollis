// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"context"
	"fmt"

	"github.com/kamenxrider/hollis/internal/runner"
)

func probeNativeStatus(ctx context.Context, r runner.Runner) (runner.LocalStatus, error) {
	if probe, ok := r.(runner.NativeStatusRunner); ok {
		return probe.NativeStatus(ctx)
	}
	return runner.LocalStatus{Reason: "native helper availability has not been checked"}, nil
}
func localStatusLabel(status runner.LocalStatus, err error) string {
	if err != nil {
		return "unavailable: " + err.Error()
	}
	if status.Available {
		return "available"
	}
	if status.Reason != "" {
		return "unavailable: " + status.Reason
	}
	return "unavailable"
}
func localModelRow(status runner.LocalStatus, err error) map[string]any {
	// A failed status read cannot report the model available, whatever the
	// partial value holds: the row would otherwise pair available:true with an
	// "unavailable: …" label. An unavailable model has not been measured
	// either, so it reports no context window.
	available := err == nil && status.Available
	spec, _ := runner.Spec(runner.ModelLocal)
	row := map[string]any{
		"model": "local", "backend": "native", "apple_model": "Apple Foundation Models system model",
		"available": available, "status": localStatusLabel(status, err), "inference_verified": false,
		"helper_protocol": status.Protocol, "helper_version": status.Version,
		"capabilities": localCapabilities(spec),
	}
	// The context window is a measured signal: report it where a successful
	// status read states it and omit it otherwise rather than printing an
	// invented zero, or a leftover value from an event we refused.
	if available && status.ContextSize > 0 {
		row["context_size"] = status.ContextSize
	}
	return row
}

// localCapabilities renders one route's static capabilities for diagnostics.
// Every value comes from the route table: a literal here would let the
// advertised surface drift from what the transport implements, and there is no
// second place to correct when a capability is measured differently.
func localCapabilities(spec runner.RouteSpec) map[string]bool {
	return map[string]bool{
		"streaming":      spec.Capabilities.Streaming,
		"complete_usage": spec.Capabilities.Usage,
		"stream_usage":   spec.Capabilities.StreamingUsage,
		"images":         spec.Capabilities.Images,
		"guided_output":  spec.Capabilities.Schema,
		// Tools remain unimplemented on every route.
		"tools": false,
	}
}

// localStatusDetail appends measured signals to a human availability label.
// The sentence is appended to "available" or "unavailable: …", so it needs the
// same two conditions the JSON row applies: a successful read and an available
// model. Appending a measured window to an unavailable row would claim a
// measurement that the JSON deliberately omits, and the two surfaces would
// describe the same status differently.
func localStatusDetail(status runner.LocalStatus, err error) string {
	if err != nil || !status.Available {
		return ""
	}
	if status.ContextSize > 0 {
		return fmt.Sprintf("; context window %d tokens", status.ContextSize)
	}
	return ""
}
