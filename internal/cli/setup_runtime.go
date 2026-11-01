// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
	"github.com/kamenxrider/hollis/internal/subproc"
)

const runtimeRecordLimit = 256 << 10

var runtimeRecordTimeout = 20 * time.Second

type recordOutput struct {
	bytes.Buffer
	overflow bool
	cancel   context.CancelFunc
}

func (w *recordOutput) Write(p []byte) (int, error) {
	if len(p) > runtimeRecordLimit-w.Len() {
		w.overflow = true
		w.cancel()
		return 0, errors.New("runtime record exceeds limit")
	}
	return w.Buffer.Write(p)
}

// The local caller selects the trusted producer. This binds a fresh result to
// this executable; it does not defend against a malicious local producer/user.
// decodeRuntimeRecord is the strict record decoder shared by every consumer
// path. Producer output must type-match RuntimeRecord exactly: unknown keys,
// string-encoded numbers or trailing JSON fail closed.
func decodeRuntimeRecord(data []byte) (setup.RuntimeRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record setup.RuntimeRecord
	if err := decoder.Decode(&record); err != nil {
		return setup.RuntimeRecord{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return setup.RuntimeRecord{}, errors.New("runtime record must contain one JSON object")
	}
	return record, nil
}

func readRuntimeRecord(ctx context.Context, producer string) (setup.RuntimeRecord, error) {
	unknown := setup.RuntimeRecord{ContractVersion: 1, State: "unknown", ReasonCode: "runtime_unverified",
		Runtime: setup.RuntimeIdentity{Owner: "external"}, Artifact: setup.Artifact{Verification: "unknown"}}
	fail := func() (setup.RuntimeRecord, error) {
		return unknown, configErr(errors.New("runtime verification record could not be validated; no setup actions were executed"))
	}
	if !filepath.IsAbs(producer) {
		return fail()
	}
	info, err := os.Stat(producer)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, runtimeRecordTimeout)
	defer cancel()
	output := &recordOutput{cancel: cancel}
	// The shared lifecycle owns the producer's pipes, kills its process group on
	// every exit path, and closes the read ends at the deadline so a descendant
	// holding stdout cannot keep verification open past its budget. Overflowing
	// the record cap cancels the run, which fails closed below.
	res, runErr := subproc.Run(ctx, subproc.Spec{
		Name:       producer,
		Args:       []string{"runtime-record"},
		StdoutSink: output,
		StderrCap:  -1,
	})
	if !res.Started {
		return fail()
	}
	exit := res.ExitCode
	if runErr != nil {
		return fail()
	}
	if ctx.Err() != nil || output.overflow || (exit != 0 && exit != 10) {
		return fail()
	}
	record, err := decodeRuntimeRecord(output.Bytes())
	if err != nil {
		return fail()
	}
	if record.ContractVersion != 1 ||
		(record.State != "verified" && record.State != "action_required" && record.State != "unknown") ||
		(exit == 0) != (record.State == "verified") {
		return fail()
	}
	if record.State != "verified" {
		if record.Artifact.Verification == "verified" || record.ReasonCode == "" {
			return fail()
		}
		return record, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return fail()
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(record.Runtime.ExecutablePath) {
		return fail()
	}
	recordExecutable, err := filepath.EvalSymlinks(record.Runtime.ExecutablePath)
	if err != nil || recordExecutable != executable || record.Runtime.ExecutablePath != recordExecutable ||
		record.Runtime.Version != version || record.Artifact.Verification != "verified" ||
		len(record.Artifact.SHA256) != 64 || strings.Trim(record.Artifact.SHA256, "0123456789abcdef") != "" ||
		len(record.Runtime.SourceCommit) != 40 || strings.Trim(record.Runtime.SourceCommit, "0123456789abcdef") != "" ||
		record.Receipt.SchemaVersion != 1 || record.Runtime.Owner != record.Receipt.Owner {
		return fail()
	}
	switch record.Runtime.Owner {
	case "plugin", "homebrew", "direct", "external":
	default:
		return fail()
	}
	switch record.Helper.Verification {
	case "verified", "missing", "mismatch", "failed", "unknown":
	default:
		return fail()
	}
	// The receipt-pinned protocol is a positive integer no greater than 99 on
	// the shell side, and the record interpolates it as an unquoted integer. A
	// pin outside that bound is an invalid receipt here too, not a protocol
	// era to guess at. An omitted pin (0) stays valid only for a helper that
	// was not verified.
	if record.Helper.Protocol != 0 && !runner.ValidProtocol(record.Helper.Protocol) {
		return fail()
	}
	if record.Helper.Verification == "verified" && !runner.ValidProtocol(record.Helper.Protocol) {
		return fail()
	}
	switch record.Bridges.Verification {
	case "verified", "missing", "mismatch", "failed", "unknown":
	default:
		return fail()
	}
	return record, nil
}
