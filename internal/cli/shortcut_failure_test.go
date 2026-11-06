// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/runner"
)

type shortcutErrorRunner struct {
	err   error
	calls int
}

func (r *shortcutErrorRunner) Run(_ context.Context, model runner.Model, _ string) (string, runner.Model, error) {
	r.calls++
	return "", model, r.err
}

func TestShortcutTypedExitsAndDetails(t *testing.T) {
	for _, kind := range []runner.Kind{runner.KindRateLimited, runner.KindContentRefused, runner.KindAccessRequired} {
		err := toCLIError(&runner.Error{Kind: kind})
		if ExitCode(err) != 5 || errorCode(err) != string(kind) {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "shortcuts")
	diagnostic := "Error: You have reached the usage limit for this model. Please try again later."
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '"+diagnostic+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := runner.New()
	r.ShortcutsPath = path
	_, _, failure := r.Run(t.Context(), runner.ModelCloud, "request-canary")
	var stdout bytes.Buffer
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &shortcutErrorRunner{err: failure} })
	cmd.SetOut(&stdout)
	if err := reportCommandError(cmd, &rootFlags{asJSON: true}, toCLIError(failure)); !ErrorReported(err) {
		t.Fatal(err)
	}
	_ = flags
	var payload struct {
		Error struct {
			Code, Message string
			Details       map[string]any
		}
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "rate_limited" || !strings.Contains(payload.Error.Message, diagnostic) || payload.Error.Details["apple_stdout"] != diagnostic || strings.Contains(stdout.String(), "request-canary") {
		t.Fatal(stdout.String())
	}
}

func TestBatchStopsImmediatelyOnLimit(t *testing.T) {
	stubConfigPath(t)
	job := batchTestPlan(t, runner.ModelCloud, "a.txt", "b.txt")
	st := newBatchTestStore()
	if err := st.Create(t.Context(), job.JobPath, job); err != nil {
		t.Fatal(err)
	}
	r := &shortcutErrorRunner{err: &runner.Error{Kind: runner.KindRateLimited}}
	result, err := batch.Run(t.Context(), job.JobPath, batch.InvocationOptions{MaxCalls: 2}, &hollisBatchExecutor{newRunner: func() runner.Runner { return r }}, st, realBatchClock{})
	var failure *runner.Error
	if !errors.As(err, &failure) || failure.Kind != runner.KindRateLimited || result.Attempted != 1 || r.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, r.calls, err)
	}
}
