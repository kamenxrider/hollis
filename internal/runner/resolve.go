// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import "fmt"

// MeasuredOSMajor is the macOS major version where hollis's bridge UUIDs,
// WFLLMModel strings, and transport rules were measured (results/
// macos-26-compat.md). Compiled UUIDs are artifacts of that install: they
// are only trusted as availability evidence on this OS generation.
const MeasuredOSMajor = 27

// BridgeNameCandidates maps each concrete tier to the display names its
// imported bridge is expected to carry, most specific first. make-bridge.py
// writes unsigned files as "AFM Bridge - X"; the signed imports inherit a
// ".signed" suffix from the filename, so both spellings are matched.
var BridgeNameCandidates = map[Model][]string{
	ModelCloud:    {"AFM Bridge - Cloud.signed", "AFM Bridge - Cloud"},
	ModelCloudPro: {"AFM Bridge - Cloud Pro.signed", "AFM Bridge - Cloud Pro"},
	ModelOnDevice: {"AFM Bridge - On-Device.signed", "AFM Bridge - On-Device"},
	ModelChatGPT:  {"AFM Bridge - ChatGPT.signed", "AFM Bridge - ChatGPT"},
}

// CompiledUUID returns the compile-time fallback UUID for a concrete tier
// (results/transport-and-persistence-2026-09-01.md). This is a private
// artifact of the macOS 27 development machine: any other Mac gets new
// UUIDs at import time and must not rely on these.
func CompiledUUID(m Model) string {
	switch m {
	case ModelCloud:
		return BridgeUUIDCloud
	case ModelCloudPro:
		return BridgeUUIDCloudPro
	case ModelOnDevice:
		return BridgeUUIDOnDevice
	case ModelChatGPT:
		return BridgeUUIDChatGPT
	}
	return ""
}

// ResolutionSource explains how a bridge reference was determined.
type ResolutionSource string

const (
	SourceConfig   ResolutionSource = "config"         // bridges.<tier> override
	SourceList     ResolutionSource = "shortcuts-list" // stable name match
	SourceCompiled ResolutionSource = "compiled-uuid"  // last resort
)

// ResolvedBridge is the runtime resolution outcome for one tier.
type ResolvedBridge struct {
	// Model is the tier this resolution belongs to.
	Model Model
	// Ref is the identifier to pass to `shortcuts run`: a name or a UUID.
	Ref string
	// Source records which resolution step produced Ref.
	Source ResolutionSource
	// ListedName is the bridge display name seen in `shortcuts list`,
	// empty when none matched. Name presence is a hint, not a verdict:
	// `shortcuts run <UUID>` works even after a rename.
	ListedName string
	// Available reports whether the tier should be offered and invoked.
	// It is the catalog gate for models/serve and the explicit-tier
	// usage check in respond/chat/config.
	Available bool
}

// ResolveBridges computes per-tier bridge resolution (results/
// macos-26-compat.md step 1). Precedence per tier:
//
//  1. overrides[tier] from config — a UUID is trusted as-is (`shortcuts
//     run <UUID>` is measured on 27); a name is verified against the
//     installed list when that list is available, so a stale or fake
//     name marks the tier unavailable.
//  2. first BridgeNameCandidates match in installed — the ref is the
//     NAME, so macOS 26 does not depend on UUID-run (unmeasured there).
//  3. CompiledUUID last resort — keeps the measured 27 machine working
//     with no config and even after renames (UUIDs survive renames).
//
// listOK reports whether `shortcuts list` could run at all. When it could
// not, verification is impossible and the compiled refs are trusted
// as-is (fail-open): behavior is unchanged from the pre-resolution
// builds rather than bricked by a listing failure.
//
// osMajor gates the compiled-UUID fallback on the measured OS generation:
// on macOS < 27 a compiled UUID is known to be a foreign artifact (26
// imports get new UUIDs), so tiers that resolve only to it are not
// available — cloud-pro surfaces as unavailable instead of failing later
// with a generic missing-shortcut error.
func ResolveBridges(installed []string, listOK bool, osMajor int, overrides map[Model]string) map[Model]ResolvedBridge {
	have := map[string]bool{}
	for _, n := range installed {
		have[n] = true
	}
	out := make(map[Model]ResolvedBridge, len(Models))
	for _, m := range Models {
		res := ResolvedBridge{Model: m, Ref: CompiledUUID(m), Source: SourceCompiled}
		switch {
		case !listOK:
			// Fail-open: nothing verifiable, keep the measured behavior.
			res.Available = true
		case osMajor >= MeasuredOSMajor:
			// The compiled UUIDs are measured-good on this OS generation.
			res.Available = true
		default:
			// macOS < 27: the compiled refs belong to a 27 install.
			res.Available = false
		}

		if override, ok := overrides[m]; ok && override != "" {
			res.Ref = override
			res.Source = SourceConfig
			switch {
			case !listOK || looksLikeUUID(override):
				res.Available = true
			default:
				res.Available = have[override]
				if have[override] {
					res.ListedName = override
				}
			}
		} else if listOK {
			for _, cand := range BridgeNameCandidates[m] {
				if have[cand] {
					res.Ref = cand
					res.Source = SourceList
					res.ListedName = cand
					res.Available = true
					break
				}
			}
		}
		// Cloud Pro did not exist on macOS 26 (Use Model had no Pro
		// location), so no imported or configured bridge can make it work
		// there — even a matching name is a hand-made artifact. The OS gate
		// wins for Pro regardless of how it resolved (results/
		// macos-26-compat.md: doctor shows UNSUPPORTED, catalog omits it).
		if m == ModelCloudPro && osMajor < MeasuredOSMajor {
			res.Available = false
		}
		out[m] = res
	}
	return out
}

// looksLikeUUID reports whether s has the RFC 4122 8-4-4-4-12 shape.
// A UUID override cannot be checked against `shortcuts list` (which
// shows names only), so it is trusted: UUID invocation is the measured
// 27 path.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			hex := r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
			if !hex {
				return false
			}
		}
	}
	return true
}

// UnavailableErr builds the stable usage error for an explicit tier whose
// bridge did not resolve (results/macos-26-compat.md step 2).
func UnavailableErr(m Model) error {
	if m == ModelCloudPro {
		return fmt.Errorf("%s is not available (bridge not installed; Cloud Pro requires macOS 27)", m)
	}
	return fmt.Errorf("%s is not available (bridge not installed)", m)
}
