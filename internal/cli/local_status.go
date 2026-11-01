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
	row := map[string]any{
		"model": "local", "backend": "native", "apple_model": "Apple Foundation Models system model",
		"available": status.Available, "status": localStatusLabel(status, err), "inference_verified": false,
		"helper_protocol": status.Protocol, "helper_version": status.Version,
		"capabilities": localCapabilities(),
	}
	// The context window is a measured signal: report it where the helper
	// states it and omit it otherwise rather than printing an invented zero.
	if status.ContextSize > 0 {
		row["context_size"] = status.ContextSize
	}
	return row
}

// localCapabilities reads the native route's static capabilities from the
// route table. A second copy here would let the advertised surface drift from
// what the transport actually implements.
func localCapabilities() map[string]bool {
	spec, _ := runner.Spec(runner.ModelLocal)
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
func localStatusDetail(status runner.LocalStatus) string {
	if status.ContextSize > 0 {
		return fmt.Sprintf("; context window %d tokens", status.ContextSize)
	}
	return ""
}
