// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package setup turns observed, non-generative readiness facts into explicit
// next actions. It never acquires artifacts or executes those actions.
package setup

import "github.com/kamenxrider/hollis/internal/runner"

const SchemaVersion = 1

type RuntimeIdentity struct {
	Version        string `json:"version,omitempty"`
	SourceCommit   string `json:"source_commit,omitempty"`
	ExecutablePath string `json:"executable_path,omitempty"`
	InstallRoot    string `json:"install_root,omitempty"`
	Owner          string `json:"owner,omitempty"`
}

type Artifact struct {
	Name         string `json:"name,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Verification string `json:"verification"`
}

type Helper struct {
	Path         string `json:"path,omitempty"`
	Version      string `json:"version,omitempty"`
	Protocol     int    `json:"protocol,omitempty"`
	Verification string `json:"verification"`
	ReasonCode   string `json:"reason_code,omitempty"`
}

type Platform struct {
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	MinimumMacOS int    `json:"minimum_macos,omitempty"`
}

type BridgeMember struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type BridgeArtifacts struct {
	ArchivePath   string         `json:"archive_path,omitempty"`
	ArchiveSHA256 string         `json:"archive_sha256,omitempty"`
	Verification  string         `json:"verification"`
	Members       []BridgeMember `json:"members,omitempty"`
}

type Receipt struct {
	SchemaVersion   int    `json:"schema_version"`
	CurrentVersion  string `json:"current_version,omitempty"`
	PreviousVersion string `json:"previous_version,omitempty"`
	Owner           string `json:"owner,omitempty"`
}

// RuntimeRecord is an adapter hand-off, not authentication. The caller must
// authenticate identity and bytes before supplying state:verified.
type RuntimeRecord struct {
	ContractVersion int             `json:"contract_version"`
	State           string          `json:"state"`
	ReasonCode      string          `json:"reason_code,omitempty"`
	Message         string          `json:"message,omitempty"`
	Runtime         RuntimeIdentity `json:"runtime"`
	Artifact        Artifact        `json:"artifact"`
	Helper          Helper          `json:"helper"`
	Platform        Platform        `json:"platform"`
	Bridges         BridgeArtifacts `json:"bridges"`
	Receipt         Receipt         `json:"receipt"`
}

type BridgeFacts struct {
	Discovery  string `json:"discovery"` // completed, failed, or not_attempted
	Configured bool   `json:"configured"`
	Reference  string `json:"reference"`
	Discovered bool   `json:"discovered"`
}

type NativeFacts struct {
	Checked   bool
	Available bool
	Failed    bool
	Version   string
	Protocol  int
}

type Observations struct {
	Route         string
	Runtime       RuntimeRecord
	Platform      string // supported, unsupported, or unknown
	ConfigKnown   bool
	Transport     string // available, missing, or unknown
	Bridge        BridgeFacts
	Native        NativeFacts
	PendingImport bool
}

type Action struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Plan struct {
	SchemaVersion   int         `json:"schema_version"`
	Source          string      `json:"source"`
	Route           string      `json:"route"`
	Status          string      `json:"status"`
	ReasonCode      string      `json:"reason_code"`
	Message         string      `json:"message"`
	Actions         []Action    `json:"actions"`
	InferenceTested bool        `json:"inference_tested"`
	Bridge          BridgeFacts `json:"bridge"`
}

func ValidRoute(route string) bool {
	return route == "image" || (runner.Model(route).Valid() && route != string(runner.ModelAuto))
}

// Build does not trust a record's existence as proof of authentication. A
// non-verified record is returned as an action-needed plan without observations.
func Build(o Observations) Plan {
	return evaluate(o, true)
}

// Diagnose applies the same route policy to availability observations without
// claiming artifact authentication. It is not a verified-runtime setup plan.
func Diagnose(o Observations) Plan {
	return evaluate(o, false)
}

func evaluate(o Observations, requireVerified bool) Plan {
	source := "observation-v1"
	if requireVerified {
		source = "planner-v1"
	}
	p := Plan{SchemaVersion: SchemaVersion, Source: source, Route: o.Route,
		Actions: []Action{}, Bridge: o.Bridge}
	fail := func(status, reason, message, action string) Plan {
		p.Status, p.ReasonCode, p.Message = status, reason, message
		if action != "" {
			p.Actions = []Action{{Code: action, Message: message}}
		}
		return p
	}
	if !ValidRoute(o.Route) {
		return fail("unknown", "invalid_route", "Select local, cloud, cloud-pro, on-device, chatgpt or image; auto is a policy, not a setup route.", "select_route")
	}
	if requireVerified && o.Runtime.ContractVersion != 1 {
		return fail("unknown", "runtime_contract_unsupported", "The verified-runtime contract is unsupported.", "verify_runtime")
	}
	if requireVerified && (o.Runtime.State != "verified" || o.Runtime.Artifact.Verification != "verified") {
		return fail("unknown", "runtime_unverified", "Verify the installed runtime with its installation owner before selected-route setup.", "verify_runtime")
	}
	if o.Platform == "unsupported" {
		return fail("unknown", "platform_unsupported", "The selected route is unsupported on this platform.", "check_platform")
	}
	if o.Platform != "supported" {
		return fail("unknown", "platform_unknown", "The selected route's platform support could not be established.", "check_platform")
	}
	if o.Route == string(runner.ModelLocal) {
		if requireVerified && o.Runtime.Helper.Verification != "verified" {
			return fail("unknown", "helper_unverified", "Install or repair the matching native helper through the installation owner.", "verify_helper")
		}
		if !o.Native.Checked || o.Native.Failed {
			return fail("unknown", "native_status_unknown", "Native availability could not be checked; no inference was attempted.", "check_native_status")
		}
		if requireVerified && (o.Runtime.Helper.Version == "" || !runner.ValidProtocol(o.Runtime.Helper.Protocol) ||
			!runner.ValidProtocol(o.Native.Protocol) ||
			o.Native.Version != o.Runtime.Helper.Version || o.Native.Protocol != o.Runtime.Helper.Protocol) {
			return fail("unknown", "helper_mismatch", "The observed native helper does not match its independently pinned version and protocol.", "verify_helper")
		}
		if !o.Native.Available {
			return fail("model_not_ready", "model_unavailable", "Check Apple Intelligence is enabled and model downloads have completed. Check Siri and macOS language consistency; other causes remain possible. No settings were changed.", "check_model_settings")
		}
	} else {
		if !o.ConfigKnown {
			return fail("unknown", "config_unknown", "Configuration could not be read; preserve existing mappings and resolve local state access.", "check_config")
		}
		if o.Transport == "missing" {
			return fail("unknown", "transport_missing", "The Shortcuts transport is missing.", "check_transport")
		}
		if o.Transport != "available" || o.Bridge.Discovery != "completed" {
			return fail("unknown", "discovery_unknown", "Shortcuts discovery could not establish bridge presence. No bridge is proven missing.", "check_discovery")
		}
		if !o.Bridge.Discovered {
			if o.Bridge.Configured {
				return fail("unknown", "configured_reference_unverified", "Preserve the configured reference and verify it before replacing or importing a bridge.", "verify_bridge")
			}
			if o.PendingImport {
				return fail("approval_required", "import_pending", "Complete the pending Add Shortcut approval, then check this route again. Do not reopen it automatically.", "approve_import")
			}
			return fail("import_required", "bridge_missing", "Explicitly import only the selected bridge, then recheck discovery.", "import_bridge")
		}
		if o.Route == "image" && !o.Bridge.Configured {
			return fail("approval_required", "image_configuration_required", "Explicitly connect the discovered image bridge without replacing fixed-style overrides. Image generation also requires an unlocked session.", "configure_image_bridge")
		}
	}
	return fail("ready", "prerequisites_ready", "Observed setup prerequisites are ready; inference has not been tested.", "")
}
