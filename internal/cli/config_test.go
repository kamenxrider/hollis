// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestConfigWritesPrivateFilesAtomically(t *testing.T) {
	stubConfigPath(t)
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(config{DefaultModel: "auto", Bridges: map[string]string{"cloud": "custom"}}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Dir(path)
	for _, tc := range []struct {
		name string
		path string
		want os.FileMode
	}{
		{name: "state directory", path: dir, want: 0o700},
		{name: "config", path: path, want: 0o600},
		{name: "lock", path: path + ".lock", want: 0o600},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.name, err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Errorf("%s mode = %o, want %o", tc.name, got, tc.want)
		}
	}

	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "auto" || got.Bridges["cloud"] != "custom" {
		t.Fatalf("config = %+v, want the saved values", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config.json-") {
			t.Fatalf("unexpected JSON temporary file left behind: %s", entry.Name())
		}
	}
}

func TestConfigReadTightensExistingFileAndRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfigAt(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode after read = %o, want 600", got)
	}

	link := filepath.Join(dir, "linked-config.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfigAt(link); err == nil {
		t.Fatal("symlinked config should be rejected")
	}
}

func TestConfigStateDirectoryRejectsSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfigDir(link); err == nil {
		t.Fatal("symlinked state directory should be rejected")
	}
}

func TestInterruptedConfigTemporaryFileCannotReplaceCanonicalState(t *testing.T) {
	stubConfigPath(t)
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(config{DefaultModel: "cloud"}); err != nil {
		t.Fatal(err)
	}
	interrupted := filepath.Join(filepath.Dir(path), ".config.json-interrupted")
	if err := os.WriteFile(interrupted, []byte(`{"default_model":`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "cloud" {
		t.Fatalf("canonical config changed by interrupted temporary file: %+v", got)
	}
	if err := updateConfig(func(c *config) error {
		c.DefaultModel = "auto"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err = loadConfig()
	if err != nil || got.DefaultModel != "auto" {
		t.Fatalf("config did not recover after interrupted temp: %+v err=%v", got, err)
	}
}

func TestConfigReadRejectsUnknownOrInvalidState(t *testing.T) {
	for _, raw := range []string{
		`{"unexpected":true}`,
		`{"default_model":"invented"}`,
		`{"bridges":{"auto":"not-a-tier"}}`,
		`{"default_model":"auto"} {}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfigAt(path); err == nil {
			t.Fatalf("invalid config accepted: %s", raw)
		}
	}
}

func TestConfigUpdatesSerializeAcrossGoroutines(t *testing.T) {
	stubConfigPath(t)
	tiers := []string{"cloud", "cloud-pro", "on-device", "chatgpt"}
	count := len(tiers)
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i, tier := range tiers {
		tier := tier
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- updateConfig(func(c *config) error {
				if c.Bridges == nil {
					c.Bridges = map[string]string{}
				}
				c.Bridges[tier] = "ref-" + string(rune('a'+i))
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bridges) != count {
		t.Fatalf("serialized updates preserved %d/%d keys: %+v", len(got.Bridges), count, got.Bridges)
	}
}

func TestConfigSetJSONIsStructured(t *testing.T) {
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"config", "set", "model", "auto", "--json"})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("config set: %v", execErr)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("config set JSON: %v (%q)", err, out.String())
	}
	if got["ok"] != true || got["key"] != "default_model" || got["value"] != "auto" {
		t.Fatalf("config set JSON = %+v", got)
	}
}

func TestConfigShowAndChatsListRejectExtraArguments(t *testing.T) {
	stubConfigPath(t)
	for _, args := range [][]string{
		{"config", "show", "extra"},
		{"chats", "list", "extra"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("args %v unexpectedly succeeded", args)
		}
	}
}
