// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"reflect"
	"testing"
)

// allNames returns the display names the measured 27 machine shows after
// the standard signed import.
func allNames() []string {
	return []string{
		"AFM Bridge - Cloud.signed",
		"AFM Bridge - Cloud Pro.signed",
		"AFM Bridge - On-Device.signed",
		"AFM Bridge - ChatGPT.signed",
	}
}

func refsByModel(res map[Model]ResolvedBridge, m Model) ResolvedBridge {
	return res[m]
}

func TestResolveBridgesMeasuredMachine(t *testing.T) {
	// This machine, no config: every tier matches a stable name and the
	// invoked ref is that name (26-safe), not the compiled UUID.
	res := ResolveBridges(allNames(), true, 27, nil)
	for _, m := range Models {
		rb := refsByModel(res, m)
		if !rb.Available {
			t.Fatalf("%s: want available", m)
		}
		if rb.Source != SourceList {
			t.Fatalf("%s: source = %q, want %q", m, rb.Source, SourceList)
		}
		if rb.ListedName == "" {
			t.Fatalf("%s: want a listed name", m)
		}
	}
	if got := res[ModelCloud].Ref; got != "AFM Bridge - Cloud.signed" {
		t.Fatalf("cloud ref = %q, want the .signed name", got)
	}
}

func TestResolveBridgesRenamedBridgeReportsMissing(t *testing.T) {
	// Renamed away on 27. `shortcuts run <UUID>` would still work, but a
	// rename is indistinguishable from an absent bridge when all we have is
	// the name listing — and the far more common cause of "no name match"
	// is a machine that never imported the bridges at all. Report missing
	// and let `hollis config set bridge` be the remedy, rather than
	// reporting OK on every fresh Mac. The compiled UUID stays as the Ref
	// so that remedy has something to point at.
	names := []string{"AFM Bridge - On-Device.signed", "AFM Bridge - ChatGPT.signed"} // cloud/pro renamed away
	res := ResolveBridges(names, true, 27, nil)
	cloud := refsByModel(res, ModelCloud)
	if cloud.Available {
		t.Fatalf("cloud after rename = %+v, want unavailable", cloud)
	}
	if cloud.Source != SourceCompiled || cloud.Ref != CompiledUUID(ModelCloud) {
		t.Fatalf("cloud after rename = %+v, want the compiled UUID as Ref", cloud)
	}
	if cloud.ListedName != "" {
		t.Fatalf("cloud ListedName = %q, want empty", cloud.ListedName)
	}
	// The documented remedy restores it only when the listed name proves it.
	res = ResolveBridges(names, true, 27, map[Model]string{ModelCloud: CompiledUUID(ModelCloud)})
	if rb := refsByModel(res, ModelCloud); !rb.Available || rb.Verified || rb.Source != SourceConfiguredUnverified {
		t.Fatalf("cloud with config UUID override = %+v, want callable but configured-unverified", rb)
	}
	// On-device was never renamed, so it still resolves by name.
	if rb := refsByModel(res, ModelOnDevice); !rb.Available || rb.Source != SourceList {
		t.Fatalf("on-device = %+v, want available via list", rb)
	}
}

func TestResolveBridgesFreshMacWithNoBridgesIsUnavailable(t *testing.T) {
	// The case every non-measured machine is in, and the one the pre-fix
	// code got wrong: `shortcuts list` works fine, none of our bridges are
	// installed, and the compiled UUIDs belong to someone else's Mac.
	// Doctor must say MISSING here, not OK.
	res := ResolveBridges([]string{"Some Unrelated Shortcut", "Make PDF"}, true, 27, nil)
	for _, m := range Models {
		rb := refsByModel(res, m)
		if rb.Available {
			t.Fatalf("%s on a fresh 27 Mac = %+v, want unavailable", m, rb)
		}
		if rb.ListedName != "" {
			t.Fatalf("%s: ListedName = %q, want empty", m, rb.ListedName)
		}
	}
}

func TestResolveBridgesPartialInstallGatesPerTier(t *testing.T) {
	// Half-finished import: only Cloud was signed and opened. That tier
	// works and the other three must not claim to.
	res := ResolveBridges([]string{"AFM Bridge - Cloud.signed"}, true, 27, nil)
	if rb := refsByModel(res, ModelCloud); !rb.Available || rb.Source != SourceList {
		t.Fatalf("cloud = %+v, want available via list", rb)
	}
	for _, m := range []Model{ModelCloudPro, ModelOnDevice, ModelChatGPT} {
		if rb := refsByModel(res, m); rb.Available {
			t.Fatalf("%s = %+v, want unavailable", m, rb)
		}
	}
}

func TestResolveBridgesConfigUUIDOverrideTrusted(t *testing.T) {
	res := ResolveBridges(nil, true, 27, map[Model]string{ModelChatGPT: "ABCDE123-0000-4000-8000-000000000000"})
	chatgpt := refsByModel(res, ModelChatGPT)
	if chatgpt.Ref != "ABCDE123-0000-4000-8000-000000000000" || chatgpt.Source != SourceConfiguredUnverified || !chatgpt.Available || chatgpt.Verified {
		t.Fatalf("chatgpt = %+v, want callable but configured-unverified", chatgpt)
	}
}

func TestResolveBridgesConfigNameOverrideVerified(t *testing.T) {
	// A config NAME is verified against the installed list: the md's
	// fake-name test must mark the tier unavailable.
	res := ResolveBridges(allNames(), true, 27, map[Model]string{ModelCloudPro: "Fake Bridge"})
	if rb := refsByModel(res, ModelCloudPro); rb.Available {
		t.Fatalf("cloud-pro with fake config name = %+v, want unavailable", rb)
	}
	// A renamed bridge that the user pointed config at resolves fine.
	installed := append(allNames(), "My Renamed Pro")
	res = ResolveBridges(installed, true, 27, map[Model]string{ModelCloudPro: "My Renamed Pro"})
	if rb := refsByModel(res, ModelCloudPro); !rb.Available || rb.Ref != "My Renamed Pro" || rb.ListedName != "My Renamed Pro" {
		t.Fatalf("cloud-pro with matching config name = %+v, want available", rb)
	}
}

func TestResolveBridgesProUnavailableOn26(t *testing.T) {
	// macOS 26 without a Pro bridge: cloud-pro must not silently ride the
	// compiled 27 UUID; the other tiers come from name matches.
	res := ResolveBridges(allNames(), true, 26, nil)
	if rb := refsByModel(res, ModelCloudPro); rb.Available {
		t.Fatalf("cloud-pro on 26 = %+v, want unavailable", rb)
	}
	if rb := refsByModel(res, ModelCloud); !rb.Available || rb.Source != SourceList {
		t.Fatalf("cloud on 26 = %+v, want available via list", rb)
	}
}

func TestResolveBridges26WithImportedBridgesUsesNames(t *testing.T) {
	// A 26 user who imports the (26-profile) bridges works through names
	// only; no compiled UUID is trusted.
	res := ResolveBridges(allNames(), true, 26, nil)
	for _, m := range []Model{ModelCloud, ModelOnDevice, ModelChatGPT} {
		if rb := refsByModel(res, m); !rb.Available || rb.Source != SourceList {
			t.Fatalf("%s on 26 = %+v, want available via list", m, rb)
		}
	}
}

func TestResolveBridgesListFailureFailsClosed(t *testing.T) {
	// `shortcuts list` broken: compiled UUIDs are candidates, never proof of
	// installation. No tier may claim availability without positive evidence.
	res := ResolveBridges(nil, false, 27, nil)
	for _, m := range Models {
		if rb := refsByModel(res, m); rb.Available || rb.Source != SourceCompiled || !rb.OSKnown {
			t.Fatalf("%s with failed list = %+v, want unavailable compiled candidate", m, rb)
		}
	}
}

func TestResolveBridgesUnknownOSIsNotMeasuredOS(t *testing.T) {
	res := ResolveBridges(allNames(), true, 0, nil)
	if refsByModel(res, ModelCloudPro).Available {
		t.Fatalf("cloud-pro with unknown OS = %+v, want unavailable", refsByModel(res, ModelCloudPro))
	}
	for _, m := range []Model{ModelCloud, ModelOnDevice, ModelChatGPT} {
		if rb := refsByModel(res, m); !rb.Available || rb.OSKnown {
			t.Fatalf("%s with unknown OS = %+v, want name-verified and OSKnown=false", m, rb)
		}
	}
}

func TestResolveBridgesProNameMatchStillUnavailableOn26(t *testing.T) {
	// Even a hand-made Pro bridge imported under the expected name cannot
	// work on 26 — Use Model has no Cloud Pro location there. The OS gate
	// wins over bridge presence for Pro.
	res := ResolveBridges(allNames(), true, 26, map[Model]string{ModelCloudPro: "AFM Bridge - Cloud Pro.signed"})
	if rb := refsByModel(res, ModelCloudPro); rb.Available {
		t.Fatalf("cloud-pro on 26 with matching name = %+v, want unavailable", rb)
	}
}

func TestResolveBridgesBareNamesMatch(t *testing.T) {
	// Unsigned/manual imports may drop the .signed suffix.
	res := ResolveBridges([]string{"AFM Bridge - Cloud"}, true, 27, nil)
	if rb := refsByModel(res, ModelCloud); rb.Ref != "AFM Bridge - Cloud" || !rb.Available {
		t.Fatalf("cloud with bare name = %+v", rb)
	}
}

func TestLooksLikeUUID(t *testing.T) {
	cases := map[string]bool{
		"BD8CDC56-7CB8-418D-9B02-9D33AB911BF0": true,
		"bd8cdc56-7cb8-418d-9b02-9d33ab911bf0": true,
		"AFM Bridge - Cloud":                   false,
		"":                                     false,
		"BBBBBBBB-CCCC-DDDD-EEEE":              false,
		"ZZZZZZZZ-7CB8-418D-9B02-9D33AB911BF0": false,
	}
	for in, want := range cases {
		if got := looksLikeUUID(in); got != want {
			t.Fatalf("looksLikeUUID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestUnavailableErrMentionsProRequirement(t *testing.T) {
	if got := UnavailableErr(ModelCloudPro).Error(); got != "cloud-pro is not available (bridge not installed; Cloud Pro requires macOS 27)" {
		t.Fatalf("pro message = %q", got)
	}
	if got := UnavailableErr(ModelCloud).Error(); got != "cloud is not available (bridge not installed)" {
		t.Fatalf("cloud message = %q", got)
	}
}

func TestBridgeNameCandidatesCoverAllModels(t *testing.T) {
	if !reflect.DeepEqual(sortedModels(), Models) {
		t.Fatalf("Models order changed; keep BridgeNameCandidates in sync: %v", Models)
	}
	for _, m := range Models {
		if len(BridgeNameCandidates[m]) == 0 {
			t.Fatalf("%s: no name candidates", m)
		}
	}
}

func sortedModels() []Model { return Models }
