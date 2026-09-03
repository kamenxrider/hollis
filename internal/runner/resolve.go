// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import "fmt"

// MeasuredOSMajor is the macOS major version where hollis's bridge UUIDs,
// WFLLMModel strings, and transport rules were measured. Compiled UUIDs are
// artifacts of that install and are never availability evidence on a host.
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

// CompiledUUID returns the compile-time fallback UUID for a concrete tier.
// This is a private artifact of the macOS 27 development machine: any other
// Mac gets new UUIDs at import time and must not rely on these.
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
	SourceConfig               ResolutionSource = "config"
	SourceList                 ResolutionSource = "shortcuts-list"
	SourceCompiled             ResolutionSource = "compiled-uuid"
	SourceConfiguredUnverified ResolutionSource = "configured-unverified"
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
	// Verified means discovery positively matched the configured or standard
	// bridge name. An explicit UUID remains callable but unverified.
	Verified bool
	// OSKnown is false only when the host macOS version could not be
	// measured. Compiled artifacts never become evidence about the host.
	OSKnown bool
}

// ResolveBridges computes per-tier bridge resolution. Precedence per tier:
//
//  1. overrides[tier] from config — a UUID remains an explicit callable but
//     unverified candidate (`shortcuts run <UUID>` is measured on 27); a name is verified against the
//     installed list when that list is available, so a stale or fake
//     name marks the tier unavailable.
//  2. first BridgeNameCandidates match in installed — the ref is the
//     NAME, so macOS 26 does not depend on UUID-run (unmeasured there).
//  3. CompiledUUID last resort — still the Ref, so a config override or a
//     later fix has something to point at, but NOT evidence of presence.
//
// listOK reports whether `shortcuts list` could run at all. When it could
// not, verification is impossible and every tier is unavailable. The caller
// must preserve the list failure rather than treat this as a harmless local
// configuration state.
//
// When the listing DID work and no name matched, the tier is unavailable.
// The compiled UUIDs are private artifacts of the machine they were
// measured on; on any other Mac they name shortcuts that do not exist.
// Trusting them because the OS generation matched made every fresh macOS
// 27 install report four healthy bridges and then fail at the first
// prompt with a bare exit 3 — doctor's own JSON carried the contradiction
// ("installed": false beside "status": "ok").
//
// The cost of this is a renamed-away bridge on the measured machine: it
// now reports missing even though `shortcuts run <UUID>` would still
// work, because a rename and a missing bridge are indistinguishable from
// the listing alone. That case has a one-command remedy
// (`hollis config set bridge <tier> <uuid-or-new-name>`) and affects one
// machine; the false-OK affected every other machine.
//
// osMajor now gates only Cloud Pro, which cannot work below the measured
// generation no matter how it resolved (see the check below). Zero means
// "unknown": hollis does not trust the compiled development environment as
// evidence about the host.
func ResolveBridges(installed []string, listOK bool, osMajor int, overrides map[Model]string) map[Model]ResolvedBridge {
	have := map[string]bool{}
	for _, n := range installed {
		have[n] = true
	}
	out := make(map[Model]ResolvedBridge, len(Models))
	for _, m := range Models {
		res := ResolvedBridge{
			Model:   m,
			Ref:     CompiledUUID(m),
			Source:  SourceCompiled,
			OSKnown: osMajor != 0,
		}
		// Discovery is fail-closed. A failed `shortcuts list` is a transport
		// problem, not evidence that bridges are installed; the caller keeps
		// the list error and classifies it separately.
		res.Available = false

		if override, ok := overrides[m]; ok && override != "" {
			res.Ref = override
			switch {
			case !listOK:
				// The override is user intent, but discovery cannot verify
				// it. It is therefore never reported healthy.
				res.Source = SourceConfiguredUnverified
				res.Available = true
			case looksLikeUUID(override):
				// `shortcuts list` reports names, not UUIDs. A UUID override
				// cannot be proven present or absent by listing alone.
				res.Source = SourceConfiguredUnverified
				res.Available = true
			default:
				res.Source = SourceConfig
				res.Available = have[override]
				res.Verified = res.Available
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
					res.Verified = true
					break
				}
			}
		}
		// Cloud Pro did not exist on macOS 26 (Use Model had no Pro
		// location), so no imported or configured bridge can make it work
		// there — even a matching name is a hand-made artifact. The OS gate
		// wins for Pro regardless of how it resolved.
		if m == ModelCloudPro && (osMajor == 0 || osMajor < MeasuredOSMajor) {
			res.Available = false
			res.Verified = false
		}
		out[m] = res
	}
	return out
}

// looksLikeUUID reports whether s has the RFC 4122 8-4-4-4-12 shape.
// A UUID override cannot be checked against `shortcuts list` (which
// shows names only), so it remains an explicit, callable but unverified
// candidate. UUID invocation itself is measured on the development Mac.
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
// bridge did not resolve.
func UnavailableErr(m Model) error {
	if m == ModelCloudPro {
		return fmt.Errorf("%s is not available (bridge not installed; Cloud Pro requires macOS 27)", m)
	}
	return fmt.Errorf("%s is not available (bridge not installed)", m)
}
