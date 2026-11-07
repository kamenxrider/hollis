// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

// Capabilities is the shared CLI and HTTP description of a concrete route.
// Static capabilities do not establish availability or successful inference.
type Capabilities struct {
	Backend       TransportKind `json:"backend"`
	Streaming     bool          `json:"streaming"`
	CompleteUsage bool          `json:"complete_usage"`
	StreamUsage   bool          `json:"stream_usage"`
	Images        bool          `json:"images"`
	MaxImages     int           `json:"max_images"`
	GuidedOutput  bool          `json:"guided_output"`
	Tools         bool          `json:"tools"`
}

// RouteCapabilities combines implementation facts with the helper's measured
// executable turn-handoff advertisement. A failed probe cannot advertise tools.
func RouteCapabilities(spec RouteSpec, status LocalStatus, statusErr error) Capabilities {
	c := spec.Capabilities
	return Capabilities{
		Backend: spec.Transport, Streaming: c.Streaming, CompleteUsage: c.Usage,
		StreamUsage: c.StreamingUsage, Images: c.Images, MaxImages: c.MaxImages,
		GuidedOutput: c.Schema,
		Tools:        spec.ID == ModelLocal && statusErr == nil && status.Available && status.ExecutableTools(),
	}
}
