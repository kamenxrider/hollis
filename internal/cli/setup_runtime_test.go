// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
)

func verifiedRecordFixture(t *testing.T) setup.RuntimeRecord {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return setup.RuntimeRecord{ContractVersion: 1, State: "verified",
		Runtime:  setup.RuntimeIdentity{Version: version, SourceCommit: strings.Repeat("a", 40), ExecutablePath: executable, Owner: "plugin"},
		Artifact: setup.Artifact{Verification: "verified", SHA256: strings.Repeat("b", 64)},
		Helper:   setup.Helper{Verification: "verified", Version: runner.NativeVersion, Protocol: runner.NativeProtocol},
		Bridges:  setup.BridgeArtifacts{Verification: "verified"},
		Receipt:  setup.Receipt{SchemaVersion: 1, Owner: "plugin"}}
}

func producerFixture(t *testing.T, record setup.RuntimeRecord, exit int) string {
	t.Helper()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return producerScriptFixture(t, "printf '%s\\n' '"+string(raw)+"'\nexit "+strconv.Itoa(exit))
}

func producerScriptFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "producer with spaces")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$1\" = runtime-record ] || exit 2\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRuntimeRecordProducer(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*setup.RuntimeRecord)
		exit int
		ok   bool
	}{
		{"verified", nil, 0, true},
		{"action needed", func(r *setup.RuntimeRecord) {
			r.State, r.ReasonCode, r.Artifact.Verification = "action_required", "runtime_integrity_failed", "failed"
		}, 10, true},
		{"action needed with message", func(r *setup.RuntimeRecord) {
			r.State, r.ReasonCode, r.Artifact.Verification = "action_required", "runtime_integrity_failed", "failed"
			r.Message = "Integrity check failed for hollis; nothing will be installed or run."
		}, 10, true},
		{"helper missing does not invalidate CLI", func(r *setup.RuntimeRecord) { r.Helper.Verification = "missing" }, 0, true},
		{"path mismatch", func(r *setup.RuntimeRecord) { r.Runtime.ExecutablePath = "/bin/sh" }, 0, false},
		{"version mismatch", func(r *setup.RuntimeRecord) { r.Runtime.Version = "future" }, 0, false},
		{"contract mismatch", func(r *setup.RuntimeRecord) { r.ContractVersion = 2 }, 0, false},
		{"exit state mismatch", nil, 10, false},
		{"unverified CLI", func(r *setup.RuntimeRecord) { r.Artifact.Verification = "unknown" }, 0, false},
		{"owner mismatch", func(r *setup.RuntimeRecord) { r.Receipt.Owner = "external" }, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := verifiedRecordFixture(t)
			if tc.edit != nil {
				tc.edit(&record)
			}
			got, err := readRuntimeRecord(context.Background(), producerFixture(t, record, tc.exit))
			if (err == nil) != tc.ok {
				t.Fatalf("record=%+v err=%v", got, err)
			}
			if !tc.ok && (got.State == "verified" || got.ReasonCode != "runtime_unverified") {
				t.Fatalf("invalid record became trusted: %+v", got)
			}
		})
	}
}

func TestRuntimeRecordProducerBoundaries(t *testing.T) {
	old := runtimeRecordTimeout
	runtimeRecordTimeout = 100 * time.Millisecond
	t.Cleanup(func() { runtimeRecordTimeout = old })
	for _, tc := range []struct{ name, body string }{
		{"malformed", "echo 'not JSON'"},
		{"extra JSON", "echo '{} {}'"},
		{"oversize", "dd if=/dev/zero bs=262145 count=1 2>/dev/null"},
		{"timeout", "sleep 30"},
		{"unexpected exit", "echo '{}'; exit 3"},
		{"stderr private", "echo 'private fixture diagnostic' >&2; echo 'bad'"},
		{"unknown record key", `printf '{"contract_version":1,"state":"unknown","reason_code":"x","detail_message":"x"}'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readRuntimeRecord(context.Background(), producerScriptFixture(t, tc.body))
			if err == nil || strings.Contains(err.Error(), "private fixture diagnostic") {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if _, err := readRuntimeRecord(context.Background(), "relative-producer"); err == nil {
		t.Fatal("accepted relative producer")
	}
}

func TestVerifiedSetupUsesFreshRecordAndNoInference(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, []string{"AFM Bridge - Cloud Pro"}, true, 27)
	record := verifiedRecordFixture(t)
	record.Helper.Verification = "missing"
	path := producerFixture(t, record, 0)
	for range 2 {
		got, err := runSetupFixture(t, &setupStatusRunner{}, "setup", "--model", "cloud-pro", "--runtime-record-command", path, "--json")
		if err != nil || got["status"] != "ready" || got["source"] != "planner-v1" || got["inference_tested"] != false {
			t.Fatalf("got=%v err=%v", got, err)
		}
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho malformed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := runSetupFixture(t, &setupStatusRunner{}, "setup", "--model", "cloud-pro", "--runtime-record-command", path, "--json")
	if ExitCode(err) != 10 || got["reason_code"] != "runtime_unverified" {
		t.Fatalf("reused stale runtime record: got=%v err=%v", got, err)
	}
}

func TestVerifiedLocalSetupRequiresVerifiedHelper(t *testing.T) {
	record := verifiedRecordFixture(t)
	record.Helper.Verification = "mismatch"
	r := &setupStatusRunner{status: runner.LocalStatus{Available: true}}
	got, err := runSetupFixture(t, r, "setup", "--model", "local", "--runtime-record-command", producerFixture(t, record, 0), "--json")
	if ExitCode(err) != 10 || got["reason_code"] != "helper_unverified" || r.probes != 0 {
		t.Fatalf("got=%v err=%v probes=%d", got, err, r.probes)
	}
}

func TestPendingImportRequiresSuccessfulMissingBridgeObservation(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		listOK       bool
		exit         int
	}{
		{"observed missing", "approval_required", true, 10},
		{"discovery failed", "unknown", false, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, nil, tc.listOK, 27)
			got, err := runSetupFixture(t, &setupStatusRunner{}, "setup", "--model", "cloud",
				"--pending-import", "cloud", "--runtime-record-command", producerFixture(t, verifiedRecordFixture(t), 0), "--json")
			if ExitCode(err) != tc.exit || got["status"] != tc.status {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}

// Real-producer conformance: the plugin's shell producer output must type-match
// the Go record exactly. Existing producer tests only decode Go-marshaled
// records, so a shell-side wire-type drift (a string-encoded number) silently
// degraded verified runtimes to the fail-closed unknown record. These fixtures
// build the actual install tree and run the actual setup.sh producer.
func realProducerWrapper(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "producer.sh")
	script := "#!/bin/sh\nexec /bin/bash " + strconv.Quote(filepath.Join(root, "plugins/hollis/scripts/setup.sh")) + " runtime-record\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeProducerFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func producerDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func realProducerVerifiedHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "runtime")
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(home)); err == nil {
		// The shell producer reports canonical paths (/var prefix aliases).
		home = filepath.Join(resolved, "runtime")
	}
	dir := filepath.Join(home, "versions", "0.3.0")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(dir, "hollis")
	helper := filepath.Join(dir, "hollis-native")
	writeProducerFile(t, cli, "#!/bin/sh\n[ \"$1\" = --version ] && printf 'hollis 0.3.0\\n'\n", 0o700)
	writeProducerFile(t, helper,
		"#!/bin/sh\ncase \"$1\" in --version) printf 'hollis-native 0.3.0\\n';; --protocol-version) printf '1\\n';; esac\n", 0o700)
	members := []string{
		"AFM Bridge - Cloud.shortcut", "AFM Bridge - Cloud Pro.shortcut",
		"AFM Bridge - On Device.shortcut", "AFM Bridge - ChatGPT.shortcut",
		"Hollis Image - Reference Input v2.shortcut",
	}
	zipPath := filepath.Join(dir, "hollis-bridges.zip")
	archive, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for _, name := range members {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("test-only shortcut bytes")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{
		"schema_version": 2, "version": "0.3.0", "repository": "kamenxrider/hollis",
		"source_ref": "refs/tags/v0.3.0", "source_commit": strings.Repeat("a", 40),
		"binary": map[string]any{"name": "hollis-darwin-arm64", "sha256": producerDigest(t, cli)},
		"native": map[string]any{"name": "hollis-native-darwin-arm64", "sha256": producerDigest(t, helper),
			"version": "0.3.0", "protocol_version": 1},
		"bridges":      map[string]any{"name": "hollis-bridges.zip", "sha256": producerDigest(t, zipPath)},
		"bridge_files": members,
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	writeProducerFile(t, filepath.Join(dir, "runtime.lock.json"), string(raw), 0o600)
	writeProducerFile(t, filepath.Join(home, "current"), "0.3.0\n", 0o600)
	writeProducerFile(t, filepath.Join(home, "previous"), "0.2.0\n", 0o600)
	return home
}

func TestRealProducerRecordConformance(t *testing.T) {
	wrapper := realProducerWrapper(t)
	t.Setenv("HOLLIS_CALLER_PATH", "/usr/bin:/bin")

	t.Run("verified plugin install decodes every emitted field", func(t *testing.T) {
		home := realProducerVerifiedHome(t)
		t.Setenv("HOLLIS_PLUGIN_HOME", home)
		dir := filepath.Join(home, "versions", "0.3.0")
		result, err := exec.Command(wrapper).Output()
		if err != nil {
			t.Fatalf("producer failed: %v", err)
		}
		record, err := decodeRuntimeRecord(result)
		if err != nil {
			t.Fatalf("real producer output rejected: %v output=%s", err, result)
		}
		want := setup.RuntimeRecord{
			ContractVersion: 1, State: "verified", ReasonCode: "runtime_verified",
			Runtime: setup.RuntimeIdentity{Version: "0.3.0", SourceCommit: strings.Repeat("a", 40),
				ExecutablePath: filepath.Join(dir, "hollis"), InstallRoot: dir, Owner: "plugin"},
			Artifact: setup.Artifact{Name: "hollis-darwin-arm64",
				SHA256: producerDigest(t, filepath.Join(dir, "hollis")), Verification: "verified"},
			Helper: setup.Helper{Path: filepath.Join(dir, "hollis-native"), Version: "0.3.0",
				Protocol: 1, Verification: "verified", ReasonCode: "helper_verified"},
			Platform: setup.Platform{OS: "darwin", Architecture: "arm64", MinimumMacOS: 27},
			Bridges: setup.BridgeArtifacts{ArchivePath: filepath.Join(dir, "hollis-bridges.zip"),
				ArchiveSHA256: producerDigest(t, filepath.Join(dir, "hollis-bridges.zip")),
				Verification:  "verified", Members: []setup.BridgeMember{}},
			Receipt: setup.Receipt{SchemaVersion: 1, CurrentVersion: "0.3.0", PreviousVersion: "0.2.0", Owner: "plugin"},
		}
		// Field-group comparison keeps the table exhaustive over every
		// emitted key; member hashes are checked below.
		gotBridges := record.Bridges
		if record.Runtime != want.Runtime || record.Artifact != want.Artifact ||
			record.Helper != want.Helper || record.Platform != want.Platform ||
			record.Receipt != want.Receipt ||
			record.ContractVersion != want.ContractVersion || record.State != want.State ||
			record.ReasonCode != want.ReasonCode || record.Message != want.Message ||
			gotBridges.Verification != want.Bridges.Verification ||
			gotBridges.ArchivePath != want.Bridges.ArchivePath ||
			gotBridges.ArchiveSHA256 != want.Bridges.ArchiveSHA256 {
			t.Fatalf("record mismatch:\n got %+v\nwant %+v", record, want)
		}
		if len(gotBridges.Members) != 5 {
			t.Fatalf("members=%+v", gotBridges.Members)
		}
		seen := map[string]bool{}
		for _, m := range gotBridges.Members {
			if len(m.SHA256) != 64 || seen[m.Name] {
				t.Fatalf("member=%+v", m)
			}
			seen[m.Name] = true
		}
	})

	t.Run("absent runtime keeps reason code and integer platform", func(t *testing.T) {
		t.Setenv("HOLLIS_PLUGIN_HOME", filepath.Join(t.TempDir(), "runtime"))
		record, err := readRuntimeRecord(context.Background(), wrapper)
		if err != nil {
			t.Fatalf("producer record rejected: %v", err)
		}
		if record.State != "action_required" || record.ReasonCode != "runtime_not_installed" ||
			record.Runtime.Owner != "plugin" || record.Platform.OS != "darwin" ||
			record.Platform.MinimumMacOS != 27 {
			t.Fatalf("record=%+v", record)
		}
	})

	t.Run("receipt mismatch keeps its message and reason code", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "runtime")
		dir := filepath.Join(home, "versions", "0.3.0")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeProducerFile(t, filepath.Join(dir, "runtime.lock.json"), `{"schema_version":1,"version":"0.4.0"}`, 0o600)
		writeProducerFile(t, filepath.Join(home, "current"), "0.3.0\n", 0o600)
		t.Setenv("HOLLIS_PLUGIN_HOME", home)
		record, err := readRuntimeRecord(context.Background(), wrapper)
		if err != nil {
			t.Fatalf("producer record rejected: %v", err)
		}
		if record.State != "action_required" || record.ReasonCode != "runtime_integrity_failed" ||
			record.Message != "Installed runtime receipt does not match its version." ||
			record.Platform.MinimumMacOS != 27 {
			t.Fatalf("record=%+v", record)
		}
	})

	t.Run("external runtimes stay unverified and platformless", func(t *testing.T) {
		for _, tc := range []struct{ name, version string }{
			{"newer parseable", "9.0.0"},
			{"unparseable token", "dev-build"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				base := t.TempDir()
				binary := filepath.Join(base, "hollis")
				writeProducerFile(t, binary,
					"#!/bin/sh\n[ \"$1\" = --version ] && printf 'hollis "+tc.version+"\\n'\n", 0o700)
				t.Setenv("HOLLIS_CALLER_PATH", base)
				t.Setenv("HOLLIS_PLUGIN_HOME", filepath.Join(base, "runtime"))
				record, err := readRuntimeRecord(context.Background(), wrapper)
				if err != nil {
					t.Fatalf("producer record rejected: %v", err)
				}
				if record.State != "unknown" || record.ReasonCode != "external_runtime_unverified" ||
					record.Runtime.Owner != "external" || record.Runtime.Version != tc.version ||
					record.Platform != (setup.Platform{}) {
					t.Fatalf("record=%+v", record)
				}
			})
		}
	})
}
