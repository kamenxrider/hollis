// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"strings"
	"testing"
)

func TestDoctorDuplicateBridgeNames(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, []string{"AFM Bridge - Cloud", "AFM Bridge - Cloud Pro", "AFM Bridge - On-Device", "AFM Bridge - ChatGPT", "AFM Bridge - ChatGPT", "AFM Bridge - ChatGPT copy"}, true, 27)
	const message = `two shortcuts are named "AFM Bridge - ChatGPT"; Shortcuts may run either one. Delete the duplicates in the Shortcuts app, keeping one.`
	for _, args := range [][]string{{"doctor"}, {"doctor", "--model", "chatgpt"}} {
		out, _ := runSetupHumanFixture(t, &setupStatusRunner{}, args...)
		if !strings.Contains(out, "Warning (duplicate_bridge_name): "+message) {
			t.Fatalf("args=%v out=%q", args, out)
		}
		got, _ := runSetupFixture(t, &setupStatusRunner{}, append(args, "--json")...)
		warnings, ok := got["warnings"].([]any)
		if !ok || len(warnings) != 1 {
			t.Fatalf("args=%v warnings=%v", args, got["warnings"])
		}
		warning := warnings[0].(map[string]any)
		if warning["reason_code"] != "duplicate_bridge_name" || warning["name"] != "AFM Bridge - ChatGPT" || warning["count"] != float64(2) || warning["message"] != message {
			t.Fatalf("warning=%v", warning)
		}
	}
}

func TestDuplicateBridgeNamesAreExactAndDoNotChangeRouting(t *testing.T) {
	if got := duplicateBridgeWarnings([]string{"Bridge", "Bridge copy", "bridge"}, []string{"Bridge"}); len(got) != 0 {
		t.Fatal(got)
	}
	if got := duplicateBridgeWarnings([]string{"Bridge", "Bridge", "Bridge"}, []string{"Bridge", "Bridge"}); len(got) != 1 || got[0].Count != 3 {
		t.Fatal(got)
	}
}

func TestSetupImportForceWarnsBeforeOpening(t *testing.T) {
	f := newImportFixture(t)
	t.Setenv("IMPORT_INSTALLED", "AFM Bridge - ChatGPT")
	_, notes, err := f.run(t, "", "--import", "chatgpt", "--force", "--yes")
	if err != nil || !strings.Contains(notes, "Warning (duplicate_bridge_name): Replacing a Shortcut can leave duplicate names.") || !strings.Contains(f.calls(t), "open ") {
		t.Fatalf("err=%v notes=%s calls=%s", err, notes, f.calls(t))
	}
}
