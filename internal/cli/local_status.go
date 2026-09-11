// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"context"
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
	return map[string]any{
		"model": "local", "backend": "native", "apple_model": "Apple Foundation Models system model",
		"available": status.Available, "status": localStatusLabel(status, err), "inference_verified": false,
		"helper_protocol": status.Protocol, "helper_version": status.Version,
		"capabilities": map[string]bool{"streaming": true, "complete_usage": true, "stream_usage": false, "images": false, "tools": false},
	}
}
