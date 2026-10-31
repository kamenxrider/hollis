// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"fmt"
	"time"
)

// TransportKind classifies how a concrete route executes model calls.
type TransportKind string

const (
	// TransportShortcuts routes through a bridge shortcut invoked with
	// /usr/bin/shortcuts.
	TransportShortcuts TransportKind = "shortcuts"
	// TransportNative routes through the local Foundation Models helper.
	TransportNative TransportKind = "native"
)

// NameSet carries the OS-conditional display names for one route. Measured
// applies on macOS MeasuredOSMajor and later, Legacy below it, and Unknown
// when the host version could not be detected. Three variants keep `hollis
// models` honest about what was measured versus inferred.
type NameSet struct {
	Measured string
	Legacy   string
	Unknown  string
}

// CapabilitySet is the static implementation truth about one route: what
// the transport can do. It makes no claim about runtime availability or
// prior successful inference, and must never be presented as either.
// StreamingUsage is a measured NO on every route today.
type CapabilitySet struct {
	Images         bool
	MaxImages      int
	Streaming      bool
	Usage          bool
	StreamingUsage bool
}

// RouteSpec is the single description of one concrete route: its wire
// identifiers, bridge artifacts, display names, static capabilities, and
// batch behavior. auto is deliberately absent: it is a routing policy
// (cloud first, on-device only on a confirmed missing bridge or recognized
// rate limit), not a route, and must not gain a fake backend row.
type RouteSpec struct {
	ID        Model
	Transport TransportKind
	// Selection is the WFLLMModel Shortcuts action parameter the bridge
	// shortcuts carry — a Shortcuts-layer identifier, never an Apple
	// backend model ID (Apple publishes none). Empty for the native route.
	Selection string
	// BridgeNames are the expected import display names, most specific
	// first (the ".signed" spelling before the bare name). nil for native.
	BridgeNames []string
	// CompiledUUID is the compile-time fallback reference: a private
	// artifact of the measured macOS 27 machine, never availability
	// evidence on another host. Empty for native.
	CompiledUUID string
	// MinOSMajor is the measured minimum macOS major version, or 0 when
	// no such constraint was measured (only cloud-pro carries 27 today).
	MinOSMajor int
	// Names are the tier display names. The native route keeps its zero
	// value: its display row comes from native status, not this catalog.
	Names        NameSet
	Capabilities CapabilitySet
	// BatchSpacing is the minimum wall-clock gap between batch calls on
	// this route; 0 means no measured spacing requirement.
	BatchSpacing time.Duration
	// OwnedBy is the /v1/models owner string.
	OwnedBy string
}

// routeOrder is the canonical iteration order for the Shortcut-backed
// tiers. Every ordered consumer output (models rows, doctor bridges,
// config show, /v1/models) derives its order from this slice.
var routeOrder = []Model{ModelCloud, ModelCloudPro, ModelOnDevice, ModelChatGPT}

// routeSpecs is the canonical route table. Adding a route means adding a
// row here and extending routeOrder; consumers never grow parallel facts.
var routeSpecs = map[Model]RouteSpec{
	ModelCloud: {
		ID:           ModelCloud,
		Transport:    TransportShortcuts,
		Selection:    "Apple Intelligence",
		BridgeNames:  []string{"AFM Bridge - Cloud.signed", "AFM Bridge - Cloud"},
		CompiledUUID: BridgeUUIDCloud,
		Names: NameSet{
			Measured: "AFM 3 Cloud (Private Cloud Compute)",
			Legacy:   "Apple Intelligence cloud on Private Cloud Compute (pre-27; not AFM 3)",
			Unknown:  "Apple Intelligence cloud (macOS version unknown)",
		},
		Capabilities: CapabilitySet{Images: true, MaxImages: 3},
		BatchSpacing: 15 * time.Second,
		OwnedBy:      "Apple",
	},
	ModelCloudPro: {
		ID:           ModelCloudPro,
		Transport:    TransportShortcuts,
		Selection:    "Apple Intelligence Pro",
		BridgeNames:  []string{"AFM Bridge - Cloud Pro.signed", "AFM Bridge - Cloud Pro"},
		CompiledUUID: BridgeUUIDCloudPro,
		MinOSMajor:   MeasuredOSMajor,
		Names: NameSet{
			Measured: "AFM 3 Cloud Pro (Private Cloud Compute; macOS 27+)",
			Legacy:   "Cloud Pro is not offered on macOS 26",
			Unknown:  "Apple Intelligence Cloud Pro (requires macOS 27; macOS version unknown)",
		},
		Capabilities: CapabilitySet{Images: true, MaxImages: 3},
		BatchSpacing: 45 * time.Second,
		OwnedBy:      "Apple",
	},
	ModelOnDevice: {
		ID:           ModelOnDevice,
		Transport:    TransportShortcuts,
		Selection:    "Apple Intelligence on Device",
		BridgeNames:  []string{"AFM Bridge - On-Device.signed", "AFM Bridge - On-Device"},
		CompiledUUID: BridgeUUIDOnDevice,
		Names: NameSet{
			Measured: "AFM 3 Core or AFM 3 Core Advanced (by hardware)",
			Legacy:   "Apple Intelligence on-device model (pre-27)",
			Unknown:  "Apple Intelligence on-device model (macOS version unknown)",
		},
		Capabilities: CapabilitySet{},
		BatchSpacing: 0,
		OwnedBy:      "Apple",
	},
	ModelChatGPT: {
		ID:           ModelChatGPT,
		Transport:    TransportShortcuts,
		Selection:    "ChatGPT",
		BridgeNames:  []string{"AFM Bridge - ChatGPT.signed", "AFM Bridge - ChatGPT"},
		CompiledUUID: BridgeUUIDChatGPT,
		Names: NameSet{
			Measured: "OpenAI ChatGPT extension for Apple Intelligence",
			Legacy:   "OpenAI ChatGPT extension for Apple Intelligence",
			Unknown:  "OpenAI ChatGPT extension for Apple Intelligence",
		},
		Capabilities: CapabilitySet{Images: true, MaxImages: 1},
		BatchSpacing: 15 * time.Second,
		OwnedBy:      "OpenAI",
	},
	ModelLocal: {
		ID:        ModelLocal,
		Transport: TransportNative,
		// Native local has no bridge, no Shortcuts parameter, and no
		// compiled UUID; its display row is rendered from native status.
		Names: NameSet{},
		// Measured capabilities of the Foundation Models helper: streaming
		// and complete usage work; stream usage and image input do not.
		Capabilities: CapabilitySet{Streaming: true, Usage: true},
		BatchSpacing: 0,
		OwnedBy:      "Apple",
	},
}

// Spec returns the table row for a concrete route. ok is false for auto
// (a policy, not a route) and for unknown IDs.
func Spec(m Model) (RouteSpec, bool) {
	spec, ok := routeSpecs[m]
	return spec, ok
}

// Specs returns the Shortcut-backed routes in canonical order.
func Specs() []RouteSpec {
	out := make([]RouteSpec, 0, len(routeOrder))
	for _, m := range routeOrder {
		out = append(out, routeSpecs[m])
	}
	return out
}

func routeOrderSnapshot() []Model { return append([]Model(nil), routeOrder...) }

// SupportsImages reports the static image-input capability of the route.
// auto is false because it can fall back to on-device, whose tested
// Shortcut ignored pixels; on-device is false; unknown IDs are false.
func (m Model) SupportsImages() bool {
	spec, ok := routeSpecs[m]
	return ok && spec.Capabilities.Images
}

// validateRouteTable fails on a malformed row or an incomplete order so a
// bad table addition is caught by unit tests instead of by production
// output. It is called from the package tests on every run.
func validateRouteTable() error {
	if len(routeSpecs) != len(routeOrder)+1 {
		return fmt.Errorf("route table has %d rows, want %d (shortcut tiers plus native local)", len(routeSpecs), len(routeOrder)+1)
	}
	if _, auto := routeSpecs[ModelAuto]; auto {
		return fmt.Errorf("auto is a routing policy and must not have a route row")
	}
	seen := make(map[Model]bool, len(routeOrder))
	for _, m := range routeOrder {
		if seen[m] {
			return fmt.Errorf("duplicate route order entry %q", m)
		}
		seen[m] = true
		spec, ok := routeSpecs[m]
		if !ok {
			return fmt.Errorf("route order lists %q without a table row", m)
		}
		if spec.Transport != TransportShortcuts {
			return fmt.Errorf("%s: transport %q, want %q", m, spec.Transport, TransportShortcuts)
		}
		if spec.ID != m {
			return fmt.Errorf("row keyed %q carries ID %q", m, spec.ID)
		}
		if spec.Selection == "" {
			return fmt.Errorf("%s: missing WFLLMModel selection", m)
		}
		if len(spec.BridgeNames) == 0 {
			return fmt.Errorf("%s: missing bridge name candidates", m)
		}
		if spec.CompiledUUID == "" {
			return fmt.Errorf("%s: missing compiled UUID", m)
		}
		if spec.Names.Measured == "" || spec.Names.Legacy == "" || spec.Names.Unknown == "" {
			return fmt.Errorf("%s: incomplete display names %+v", m, spec.Names)
		}
		if spec.Capabilities.Streaming || spec.Capabilities.Usage || spec.Capabilities.StreamingUsage {
			return fmt.Errorf("%s: shortcut routes declare no streaming or usage capability today", m)
		}
	}
	local, ok := routeSpecs[ModelLocal]
	if !ok {
		return fmt.Errorf("native route %q missing from table", ModelLocal)
	}
	if local.Transport != TransportNative {
		return fmt.Errorf("%s: transport %q, want %q", ModelLocal, local.Transport, TransportNative)
	}
	if local.Selection != "" || len(local.BridgeNames) != 0 || local.CompiledUUID != "" || local.Names.Measured != "" {
		return fmt.Errorf("%s: native route must not declare bridge artifacts", ModelLocal)
	}
	if !local.Capabilities.Streaming || !local.Capabilities.Usage || local.Capabilities.StreamingUsage || local.Capabilities.Images {
		return fmt.Errorf("%s: native capabilities drifted from the measured surface", ModelLocal)
	}
	for id, spec := range routeSpecs {
		if spec.Capabilities.Images != (spec.Capabilities.MaxImages > 0) {
			return fmt.Errorf("%s: Images=%v with MaxImages=%d", id, spec.Capabilities.Images, spec.Capabilities.MaxImages)
		}
		if spec.BatchSpacing < 0 {
			return fmt.Errorf("%s: negative batch spacing %s", id, spec.BatchSpacing)
		}
		if spec.OwnedBy == "" {
			return fmt.Errorf("%s: missing /v1/models owner", id)
		}
	}
	return nil
}
