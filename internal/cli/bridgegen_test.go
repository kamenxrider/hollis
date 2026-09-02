// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runMakeBridge(t *testing.T, args ...string) string {
	t.Helper()
	script := filepath.Join("..", "..", "scripts", "make-bridge.py")
	out, err := exec.Command("python3", append([]string{script}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("make-bridge.py %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func bridgeFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".shortcut") {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files[e.Name()] = raw
		}
	}
	return files
}

func TestMakeBridgeDefaultProfileIs27(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	runMakeBridge(t, dir)
	files := bridgeFiles(t, dir)
	if len(files) != 4 {
		t.Fatalf("default profile wrote %d bridges, want 4: %v", len(files), files)
	}
	for name, raw := range files {
		if strings.Contains(name, "Pro") && !strings.Contains(string(raw), "Apple Intelligence Pro") {
			t.Fatalf("%s: missing Pro WFLLMModel string", name)
		}
		if !strings.Contains(string(raw), "3100.0.2.3") {
			t.Fatalf("%s: missing measured client version", name)
		}
	}
}

func TestMakeBridgeOS26Profile(t *testing.T) {
	// results/macos-26-compat.md step 4: the 26 profile writes exactly
	// three bridges, none carrying Cloud Pro, with the pre-27 client
	// version. The strings themselves remain untested guesses on 26.
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	runMakeBridge(t, "--os", "26", dir)
	files := bridgeFiles(t, dir)
	if len(files) != 3 {
		t.Fatalf("26 profile wrote %d bridges, want 3: %v", len(files), files)
	}
	wantModel := map[string]string{
		"AFM Bridge - Cloud.shortcut":     "Apple Intelligence",
		"AFM Bridge - On-Device.shortcut": "Apple Intelligence on Device",
		"AFM Bridge - ChatGPT.shortcut":   "ChatGPT",
	}
	for name, raw := range files {
		if strings.Contains(name, "Pro") {
			t.Fatalf("26 profile must not write a Pro bridge: %s", name)
		}
		if strings.Contains(string(raw), "Apple Intelligence Pro") {
			t.Fatalf("%s: 26 profile must not carry the Pro WFLLMModel string", name)
		}
		if !strings.Contains(string(raw), "2700.0.4") {
			t.Fatalf("%s: missing pre-27 client version", name)
		}
		want, ok := wantModel[name]
		if !ok {
			t.Fatalf("unexpected bridge file: %s", name)
		}
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s: missing WFLLMModel %q", name, want)
		}
	}
}
