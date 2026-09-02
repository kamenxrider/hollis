// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kamenxrider/hollis/internal/runner"
)

// listInstalledShortcuts lists installed shortcut display names via the
// transport. Package var so tests stub resolution without spawning
// /usr/bin/shortcuts.
var listInstalledShortcuts = func(ctx context.Context) ([]string, error) {
	return runner.New().ListShortcuts(ctx)
}

// macosVersion returns sw_vers -productVersion (e.g. "27.0"), or "" when
// unreadable. ProductVersion, not uname: this 27 machine's build string
// starts at 26.
var macosVersion = func() string {
	out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// macosBuild returns sw_vers -buildVersion (e.g. "26A5421a"), or "" when
// unreadable. Everything hollis measures is build-specific — the fm/PCC
// surface, the Use Model tier list, the WFLLMModel strings — so a bug report
// that names only "27.0" is missing the discriminating field.
var macosBuild = func() string {
	out, err := exec.Command("/usr/bin/sw_vers", "-buildVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// macosMajorVersion reports the macOS major version. The measured
// development environment (27) is the fail-open default when sw_vers is
// unreadable. Package var so tests can simulate a macOS 26 machine.
var macosMajorVersion = func() int {
	v := macosVersion()
	if v == "" {
		return runner.MeasuredOSMajor
	}
	major, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	if err != nil || major == 0 {
		return runner.MeasuredOSMajor
	}
	return major
}

// bridgeOverrides extracts configured bridge overrides (tier -> name or
// UUID). Keys that are not concrete tiers are ignored so a hand-edited
// config cannot inject an "auto" bridge.
func bridgeOverrides(c config) map[runner.Model]string {
	if len(c.Bridges) == 0 {
		return nil
	}
	overrides := make(map[runner.Model]string, len(c.Bridges))
	for tier, ref := range c.Bridges {
		m := runner.Model(tier)
		if m.Valid() && m != runner.ModelAuto {
			overrides[m] = ref
		}
	}
	return overrides
}

// resolveBridges computes the runtime bridge resolution used across the
// CLI: config override > `shortcuts
// list` name match > compiled UUID. The list subprocess is skipped only
// when every tier has a config override.
func resolveBridges(ctx context.Context) (map[runner.Model]runner.ResolvedBridge, error) {
	c, err := loadConfig()
	if err != nil {
		return nil, err
	}
	overrides := bridgeOverrides(c)
	if len(overrides) >= len(runner.Models) {
		return runner.ResolveBridges(nil, false, macosMajorVersion(), overrides), nil
	}
	names, listErr := listInstalledShortcuts(ctx)
	if listErr != nil {
		// Fail-open: keep the measured refs rather than brick every
		// command because the listing failed.
		return runner.ResolveBridges(nil, false, macosMajorVersion(), overrides), nil
	}
	return runner.ResolveBridges(names, true, macosMajorVersion(), overrides), nil
}

// applyResolvedRefs points a real ShortcutRunner at the resolved bridge
// refs. Test fakes are left untouched: they intentionally abstract the
// transport away, and resolution is a transport-presence concern.
func applyResolvedRefs(r runner.Runner, resolved map[runner.Model]runner.ResolvedBridge) {
	sr, ok := r.(*runner.ShortcutRunner)
	if !ok {
		return
	}
	refs := make(map[runner.Model]string, len(resolved))
	for m, rb := range resolved {
		refs[m] = rb.Ref
	}
	sr.BridgeRefs = refs
}

// checkModelAvailable refuses explicit tiers whose bridge did not resolve.
// auto is never gated: it runs and falls back on its own.
func checkModelAvailable(resolved map[runner.Model]runner.ResolvedBridge, m runner.Model) error {
	if m == runner.ModelAuto {
		return nil
	}
	if rb, ok := resolved[m]; ok && !rb.Available {
		return usageErr(runner.UnavailableErr(m))
	}
	return nil
}

// availabilityMap flattens a resolution into the server's per-tier gate.
func availabilityMap(resolved map[runner.Model]runner.ResolvedBridge) map[string]bool {
	avail := make(map[string]bool, len(resolved))
	for m, rb := range resolved {
		avail[string(m)] = rb.Available
	}
	return avail
}

// requireRealRunner reports whether r is the real transport, i.e. whether
// resolution should gate and retarget it. Fake runners in tests skip all
// of it.
func requireRealRunner(r runner.Runner) bool {
	_, ok := r.(*runner.ShortcutRunner)
	return ok
}

// supportNote is the honest compatibility line shared by doctor output.
const supportNote = "macOS 27 measured; macOS 26 untested"

// resolveForRunner resolves bridges only when the factory hands out the
// real transport; fake runners in tests resolve to nil (no gate, no refs).
func resolveForRunner(ctx context.Context, newRunner newRunnerFunc) (map[runner.Model]runner.ResolvedBridge, error) {
	if !requireRealRunner(newRunner()) {
		return nil, nil
	}
	return resolveBridges(ctx)
}

// resolvedNewRunnerFunc wraps a runner factory so every runner it hands
// out carries the resolved bridge refs. A nil resolution is a no-op
// (fake runners in tests pass through untouched).
func resolvedNewRunnerFunc(newRunner newRunnerFunc, resolved map[runner.Model]runner.ResolvedBridge) newRunnerFunc {
	if resolved == nil {
		return newRunner
	}
	return func() runner.Runner {
		r := newRunner()
		applyResolvedRefs(r, resolved)
		return r
	}
}

// resolvedStatus renders a doctor status for one resolution.
func resolvedStatus(rb runner.ResolvedBridge, osMajor int) string {
	if rb.Available {
		return "ok"
	}
	if rb.Model == runner.ModelCloudPro && osMajor < runner.MeasuredOSMajor {
		return "unsupported"
	}
	return "missing"
}

// missingBridgeRemedy is the action a MISSING row calls for. Kept next to
// unresolvedProNote so doctor's wording lives in one place.
const missingBridgeRemedy = "MISSING: install that bridge (README \u201cQuickstart\u201d step 2), " +
	"or point config at one you already have:\n  hollis config set bridge <tier> <name-or-uuid>"

// unresolvedProNote is the doctor hint for Pro on pre-27 macOS.
const unresolvedProNote = "cloud-pro: unsupported on macOS 26 (untested; Use Model had no Cloud Pro)"
