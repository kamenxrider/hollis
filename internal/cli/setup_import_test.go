// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type importFixture struct {
	root, bundle, log string
}

func newImportFixture(t *testing.T) importFixture {
	t.Helper()
	root := t.TempDir()
	f := importFixture{root: root, bundle: filepath.Join(root, "libexec", "fixture"), log: filepath.Join(root, "calls")}
	for _, dir := range []string{f.bundle, filepath.Join(root, "bin"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+":/usr/bin:/bin")
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	t.Setenv("IMPORT_CALLS", f.log)
	t.Setenv("IMPORT_INSTALLED", "")
	t.Setenv("IMPORT_LIST_EXIT", "0")
	t.Setenv("IMPORT_SIGN_EXIT", "0")
	t.Setenv("HOLLIS_STATE_DIR", filepath.Join(root, "state"))
	for name, script := range map[string]string{
		"shortcuts": `#!/bin/bash
set -eu
printf 'shortcuts %s\n' "$*" >> "$IMPORT_CALLS"
case "$1" in
list) printf '%s\n' "$IMPORT_INSTALLED"; exit "$IMPORT_LIST_EXIT";;
sign) [[ "$2" == --mode && "$3" == anyone && "$4" == --input && "$6" == --output ]] || exit 12
      [[ "$IMPORT_SIGN_EXIT" == 0 ]] || exit "$IMPORT_SIGN_EXIT"
      cp "$5" "$7"; chmod 644 "$7";;
*) exit 99;;
esac
`,
		"open": `#!/bin/bash
set -eu
[[ $# == 1 && -f "$1" && ! -L "$1" ]] || exit 12
printf 'open %s\n' "$1" >> "$IMPORT_CALLS"
`,
	} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(f.bundle, "hollis")
	if err := os.WriteFile(exe, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin", "hollis")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	oldExe, oldConfig, oldMajor := setupImportExecutable, configPath, macosMajorVersion
	setupImportExecutable = func() (string, error) { return link, nil }
	configPath = func() (string, error) { return filepath.Join(root, "state", "config.json"), nil }
	macosMajorVersion = func() int { return 27 }
	t.Cleanup(func() { setupImportExecutable, configPath, macosMajorVersion = oldExe, oldConfig, oldMajor })
	f.archive(t, "", false, false)
	return f
}

func (f importFixture) archive(t *testing.T, extra string, symlink, duplicate bool) {
	t.Helper()
	var content bytes.Buffer
	w := zip.NewWriter(&content)
	for _, route := range importRoutes {
		h := &zip.FileHeader{Name: importNames[route] + ".shortcut", Method: zip.Deflate}
		h.SetMode(0o644)
		if symlink && route == "image" {
			h.SetMode(os.ModeSymlink | 0o777)
		}
		entry, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("synthetic shortcut")); err != nil {
			t.Fatal(err)
		}
	}
	if duplicate {
		extra = importNames["cloud"] + ".shortcut"
	}
	if extra != "" {
		entry, err := w.Create(extra)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("unexpected")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content.Bytes())
	metadata, err := json.Marshal(map[string]any{"version": version, "sha256": map[string]string{
		"hollis-bridges.zip": hex.EncodeToString(sum[:]),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"hollis-bridges.zip": content.Bytes(), "manifest.json": metadata} {
		if err := os.WriteFile(filepath.Join(f.bundle, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (f importFixture) run(t *testing.T, input string, args ...string) (string, string, error) {
	t.Helper()
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { panic("imports must not request a model") })
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(append([]string{"setup"}, args...))
	err := executeCommand(cmd, flags)
	return out.String(), stderr.String(), err
}

func (f importFixture) calls(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetupImportAllFromResolvedExecutable(t *testing.T) {
	f := newImportFixture(t)
	out, stderr, err := f.run(t, "", "--import", "--yes", "--json")
	if err != nil {
		t.Fatalf("err=%v out=%s stderr=%s", err, out, stderr)
	}
	var result setupImportResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Opened, importRoutes) || result.Status != "approval_required" || result.InferenceTested ||
		result.Source != "bundle-import-v1" || result.Message != importNextStep || len(result.Skipped) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.HasPrefix(result.ImportFolder, filepath.Join(f.root, "state", "bridge-imports")+string(os.PathSeparator)) {
		t.Fatalf("unsafe import folder: %s", result.ImportFolder)
	}
	info, err := os.Stat(result.ImportFolder)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("private folder: info=%v err=%v", info, err)
	}
	for _, route := range importRoutes {
		path := filepath.Join(result.ImportFolder, importNames[route]+".shortcut")
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 || !strings.Contains(stderr, route+": "+importNames[route]) ||
			!strings.Contains(f.calls(t), "open "+path+"\n") {
			t.Fatalf("route=%s info=%v err=%v output=%s", route, info, err, stderr)
		}
	}
	tmp, err := os.ReadDir(filepath.Join(f.root, "tmp"))
	if err != nil || len(tmp) != 0 {
		t.Fatalf("signing temp folder leaked: %v %v", tmp, err)
	}
	c, err := readSetupConfig()
	if err != nil || c.ImageBridge != imageSetupBridge {
		t.Fatalf("image config: %+v %v", c, err)
	}
}

func TestSetupImportSelectionSkipAndForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "skip", true: "force"}[force], func(t *testing.T) {
			f := newImportFixture(t)
			t.Setenv("IMPORT_INSTALLED", "AFM Bridge - Cloud\nCustom Chat")
			if err := saveConfig(config{Bridges: map[string]string{"chatgpt": "Custom Chat"}}); err != nil {
				t.Fatal(err)
			}
			args := []string{"--import", "cloud", "chatgpt", "on-device", "cloud", "--yes", "--agent"}
			if force {
				args = append(args, "--force")
			}
			out, _, err := f.run(t, "", args...)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Results setupImportResult `json:"results"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err)
			}
			want := []string{"on-device"}
			if force {
				want = []string{"cloud", "chatgpt", "on-device"}
			}
			if !reflect.DeepEqual(envelope.Results.Opened, want) || strings.Contains(f.calls(t), imageSetupBridge) {
				t.Fatalf("out=%s calls=%s", out, f.calls(t))
			}
			c, err := readSetupConfig()
			if err != nil || c.ImageBridge != "" {
				t.Fatalf("unselected image config changed: %+v %v", c, err)
			}
		})
	}
}

func TestSetupImportConsent(t *testing.T) {
	cases := []struct {
		name     string
		terminal bool
		input    string
		args     []string
		wantOpen bool
	}{
		{"terminal yes", true, "y\n", nil, true},
		{"terminal no", true, "n\n", nil, false},
		{"terminal default no", true, "\n", nil, false},
		{"pipe", false, "yes\n", nil, false},
		{"no input", true, "yes\n", []string{"--no-input"}, false},
		{"agent", true, "yes\n", []string{"--agent"}, false},
		{"json", true, "yes\n", []string{"--json"}, false},
		{"explicit approval", false, "", []string{"--yes", "--no-input"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			old := interactiveStdin
			interactiveStdin = func() bool { return tc.terminal }
			t.Cleanup(func() { interactiveStdin = old })
			out, stderr, err := f.run(t, tc.input, append([]string{"--import", "cloud"}, tc.args...)...)
			if (err == nil) != tc.wantOpen || strings.Contains(f.calls(t), "open ") != tc.wantOpen ||
				!strings.Contains(stderr, "cloud: AFM Bridge - Cloud") {
				t.Fatalf("err=%v out=%s stderr=%s calls=%s", err, out, stderr, f.calls(t))
			}
			if !tc.wantOpen && strings.Contains(f.calls(t), "shortcuts sign") {
				t.Fatal("signed before consent")
			}
		})
	}
}

func TestSetupImportRejectsUnsafeArchiveBeforeAnyTools(t *testing.T) {
	for _, tc := range []struct {
		name, extra        string
		symlink, duplicate bool
	}{
		{"zip slip", "../escape.shortcut", false, false},
		{"absolute", "/tmp/escape.shortcut", false, false},
		{"subdirectory", "extra/AFM Bridge - Cloud.shortcut", false, false},
		{"symlink", "", true, false},
		{"duplicate", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			f.archive(t, tc.extra, tc.symlink, tc.duplicate)
			_, _, err := f.run(t, "", "--import", "cloud", "--yes")
			if err == nil || f.calls(t) != "" {
				t.Fatalf("unsafe archive accepted: err=%v calls=%s", err, f.calls(t))
			}
		})
	}
}

func TestSetupImportHashMismatchAndSymlinkInputs(t *testing.T) {
	for _, name := range []string{"corrupt zip", "symlink zip", "symlink manifest"} {
		t.Run(name, func(t *testing.T) {
			f := newImportFixture(t)
			path := filepath.Join(f.bundle, "hollis-bridges.zip")
			if name == "symlink manifest" {
				path = filepath.Join(f.bundle, "manifest.json")
			}
			if name == "corrupt zip" {
				if err := os.WriteFile(path, []byte("corruption"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := f.run(t, "", "--import", "--yes")
			if err == nil || f.calls(t) != "" {
				t.Fatalf("unsafe input accepted: err=%v calls=%s", err, f.calls(t))
			}
		})
	}
}

func TestSetupImportPreservesImageConfiguration(t *testing.T) {
	f := newImportFixture(t)
	if err := saveConfig(config{ImageBridge: "Custom Image"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := f.run(t, "", "--import", "image", "--yes", "--json", "--select", "opened,status,inference_tested")
	if err != nil || !strings.Contains(out, `"image"`) || strings.Contains(out, "import_folder") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	c, err := readSetupConfig()
	if err != nil || c.ImageBridge != "Custom Image" {
		t.Fatalf("config=%+v err=%v", c, err)
	}
}

func TestSetupImportDiscoveryAndSigningFailuresNeverOpen(t *testing.T) {
	for _, key := range []string{"IMPORT_LIST_EXIT", "IMPORT_SIGN_EXIT"} {
		t.Run(key, func(t *testing.T) {
			f := newImportFixture(t)
			t.Setenv(key, "1")
			_, _, err := f.run(t, "", "--import", "--yes", "--force")
			if err == nil || strings.Contains(f.calls(t), "open ") {
				t.Fatalf("err=%v calls=%s", err, f.calls(t))
			}
		})
	}
}

func TestSetupImportAllInstalledDoesNotSignOrOpen(t *testing.T) {
	f := newImportFixture(t)
	t.Setenv("IMPORT_INSTALLED", "AFM Bridge - Cloud")
	out, _, err := f.run(t, "", "--import", "cloud", "--yes", "--json")
	if err != nil || strings.Contains(f.calls(t), "shortcuts sign") || strings.Contains(f.calls(t), "open ") ||
		!strings.Contains(out, `"unchanged"`) || !strings.Contains(out, `"cloud"`) ||
		strings.Contains(out, importNextStep) || !strings.Contains(out, "All selected bridges are already installed; nothing to import. Run hollis doctor to check them.") {
		t.Fatalf("out=%s err=%v calls=%s", out, err, f.calls(t))
	}
}

func TestSetupImportAlreadyInstalledImageCanSelectUnsetDefault(t *testing.T) {
	f := newImportFixture(t)
	t.Setenv("IMPORT_INSTALLED", imageSetupBridge)
	out, _, err := f.run(t, "", "--import", "image", "--yes", "--json")
	if err != nil || strings.Contains(f.calls(t), "shortcuts sign") || strings.Contains(f.calls(t), "open ") ||
		!strings.Contains(out, `"configured"`) || strings.Contains(out, importNextStep) ||
		!strings.Contains(out, "nothing to import") {
		t.Fatalf("out=%s err=%v calls=%s", out, err, f.calls(t))
	}
	c, err := readSetupConfig()
	if err != nil || c.ImageBridge != imageSetupBridge {
		t.Fatalf("config=%+v err=%v", c, err)
	}
}

func TestSetupImportRejectsInvalidCombinationsWithoutTools(t *testing.T) {
	f := newImportFixture(t)
	for _, args := range [][]string{
		{"--import", "local", "--yes"}, {"--import", "auto", "--yes"},
		{"--import", "--model", "cloud"}, {"--yes"}, {"--force"},
		{"cloud"}, {"--import", "--pending-import", "image"},
	} {
		if _, _, err := f.run(t, "", args...); ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
	if f.calls(t) != "" {
		t.Fatalf("unexpected calls=%s", f.calls(t))
	}
}
