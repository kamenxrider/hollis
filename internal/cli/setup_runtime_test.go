// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"encoding/json"
	"os"
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
