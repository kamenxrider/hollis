// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package setup

import (
	"encoding/json"
	"reflect"
	"testing"
)

func readyObservation(route string) Observations {
	return Observations{
		Route: route, Platform: "supported", ConfigKnown: true, Transport: "available",
		Runtime: RuntimeRecord{ContractVersion: 1, State: "verified",
			Artifact: Artifact{Verification: "verified"}, Helper: Helper{Verification: "verified", Version: "0.5.0", Protocol: 2}},
		Bridge: BridgeFacts{Discovery: "completed", Discovered: true, Configured: true, Reference: "Custom bridge"},
		Native: NativeFacts{Checked: true, Available: true, Version: "0.5.0", Protocol: 2},
	}
}

func TestPlanner(t *testing.T) {
	for _, tc := range []struct {
		name, route, status, reason string
		change                      func(*Observations)
	}{
		{"cloud pro", "cloud-pro", "ready", "prerequisites_ready", nil},
		{"native zero bridges", "local", "ready", "prerequisites_ready", func(o *Observations) {
			o.ConfigKnown, o.Transport, o.Bridge = false, "missing", BridgeFacts{}
		}},
		{"cloud ignores native", "cloud-pro", "ready", "prerequisites_ready", func(o *Observations) {
			o.Runtime.Helper.Verification, o.Native = "missing", NativeFacts{Failed: true}
		}},
		{"runtime absent", "cloud", "unknown", "runtime_unverified", func(o *Observations) { o.Runtime.State = "action_required" }},
		{"runtime unknown", "cloud", "unknown", "runtime_unverified", func(o *Observations) { o.Runtime.Artifact.Verification = "unknown" }},
		{"new contract", "cloud", "unknown", "runtime_contract_unsupported", func(o *Observations) { o.Runtime.ContractVersion = 2 }},
		{"helper mismatch", "local", "unknown", "helper_unverified", func(o *Observations) { o.Runtime.Helper.Verification = "mismatch" }},
		{"helper version independent of runtime", "local", "ready", "prerequisites_ready", func(o *Observations) { o.Runtime.Runtime.Version = "0.5.1" }},
		{"observed helper mismatch", "local", "unknown", "helper_mismatch", func(o *Observations) { o.Native.Version = "0.5.1" }},
		{"observed protocol mismatch", "local", "unknown", "helper_mismatch", func(o *Observations) { o.Native.Protocol = 3 }},
		// The pin bound is staged: a receipt outside 1..99 is an invalid
		// receipt, not a protocol era, even when the observed helper agrees.
		{"out of range pin", "local", "unknown", "helper_mismatch", func(o *Observations) {
			o.Runtime.Helper.Protocol, o.Native.Protocol = 100, 100
		}},
		{"negative pin", "local", "unknown", "helper_mismatch", func(o *Observations) {
			o.Runtime.Helper.Protocol, o.Native.Protocol = -1, -1
		}},
		{"native failed", "local", "unknown", "native_status_unknown", func(o *Observations) { o.Native.Failed = true }},
		{"native not probed", "local", "unknown", "native_status_unknown", func(o *Observations) { o.Native.Checked = false }},
		{"model not ready", "local", "model_not_ready", "model_unavailable", func(o *Observations) { o.Native.Available = false }},
		{"platform old", "cloud-pro", "unknown", "platform_unsupported", func(o *Observations) { o.Platform = "unsupported" }},
		{"platform unknown", "cloud", "unknown", "platform_unknown", func(o *Observations) { o.Platform = "unknown" }},
		{"config failure", "cloud", "unknown", "config_unknown", func(o *Observations) { o.ConfigKnown = false }},
		{"transport missing", "cloud", "unknown", "transport_missing", func(o *Observations) { o.Transport = "missing" }},
		{"discovery failed", "cloud", "unknown", "discovery_unknown", func(o *Observations) {
			o.Bridge.Discovery, o.Bridge.Discovered, o.Bridge.Configured = "failed", false, false
		}},
		{"unverified custom", "cloud", "unknown", "configured_reference_unverified", func(o *Observations) {
			o.Bridge.Discovered, o.PendingImport = false, true
		}},
		{"missing", "cloud", "import_required", "bridge_missing", func(o *Observations) {
			o.Bridge.Discovered, o.Bridge.Configured = false, false
		}},
		{"pending approval", "cloud", "approval_required", "import_pending", func(o *Observations) {
			o.Bridge.Discovered, o.Bridge.Configured, o.PendingImport = false, false, true
		}},
		{"image connect", "image", "approval_required", "image_configuration_required", func(o *Observations) { o.Bridge.Configured = false }},
		{"auto policy", "auto", "unknown", "invalid_route", nil},
		{"unknown route", "future", "unknown", "invalid_route", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := readyObservation(tc.route)
			if tc.change != nil {
				tc.change(&o)
			}
			before := o
			p := Build(o)
			if p.Status != tc.status || p.ReasonCode != tc.reason {
				t.Fatalf("plan=%+v want %s/%s", p, tc.status, tc.reason)
			}
			if p.SchemaVersion != 1 || p.Source != "planner-v1" || p.InferenceTested || p.Actions == nil {
				t.Fatalf("invalid evidence/schema: %+v", p)
			}
			if !reflect.DeepEqual(p, Build(o)) || !reflect.DeepEqual(before, o) {
				t.Fatal("planner is not repeatable or mutated observations")
			}
			if p.Status != "ready" && len(p.Actions) == 0 {
				t.Fatal("action-needed plan lacks an explicit action")
			}
		})
	}
}

func TestRuntimeContractSerialization(t *testing.T) {
	var record RuntimeRecord
	raw := `{"contract_version":1,"state":"verified","runtime":{"version":"0.4.0","owner":"plugin"},"artifact":{"verification":"verified"},"helper":{"protocol":1,"verification":"missing"},"platform":{"minimum_macos":27},"bridges":{"verification":"verified","members":[{"name":"a.shortcut","sha256":"fixture"}]},"receipt":{"schema_version":1,"owner":"plugin"}}`
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	if record.ContractVersion != 1 || record.Runtime.Owner != record.Receipt.Owner || record.Platform.MinimumMacOS != 27 || record.Helper.Protocol != 1 || len(record.Bridges.Members) != 1 {
		t.Fatalf("record=%+v", record)
	}
}
