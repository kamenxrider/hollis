// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"io"

	"github.com/kamenxrider/hollis/internal/runner"
)

type bridgeWarning struct {
	ReasonCode string `json:"reason_code"`
	Name       string `json:"name"`
	Count      int    `json:"count"`
	Message    string `json:"message"`
}

func missingJSONBridgeWarnings(resolved map[runner.Model]runner.ResolvedBridge, models []runner.Model, osMajor int) []bridgeWarning {
	var warnings []bridgeWarning
	for _, model := range models {
		bridge := resolved[model]
		if bridge.JSONDiscoveryKnown && bridge.JSONRef == "" {
			spec, ok := runner.Spec(model)
			if !ok || (spec.MinOSMajor > 0 && osMajor < spec.MinOSMajor) {
				continue
			}
			warnings = append(warnings, bridgeWarning{ReasonCode: "json_bridge_missing",
				Name:    spec.JSONBridge.Names[len(spec.JSONBridge.Names)-1],
				Message: runner.MissingJSONBridgeHint(model)})
		}
	}
	return warnings
}

func duplicateBridgeWarnings(names, references []string) []bridgeWarning {
	counts := map[string]int{}
	for _, name := range names {
		counts[name]++
	}
	var warnings []bridgeWarning
	seen := map[string]bool{}
	for _, name := range references {
		count := counts[name]
		if count < 2 || seen[name] {
			continue
		}
		seen[name] = true
		number := fmt.Sprint(count)
		if count == 2 {
			number = "two"
		}
		warnings = append(warnings, bridgeWarning{
			ReasonCode: "duplicate_bridge_name", Name: name, Count: count,
			Message: fmt.Sprintf("%s shortcuts are named %q; Shortcuts may run either one. Delete the duplicates in the Shortcuts app, keeping one.", number, name),
		})
	}
	return warnings
}

func printBridgeWarnings(out io.Writer, warnings []bridgeWarning) {
	for _, warning := range warnings {
		fmt.Fprintf(out, "Warning (%s): %s\n", warning.ReasonCode, warning.Message)
	}
}
