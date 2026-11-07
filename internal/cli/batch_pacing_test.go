// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/runner"
)

const cloudPacingReasonWant = "Apple rate-limits cloud requests per Mac without publishing the limit; a temporary block can last hours. Pacing reduces risk but cannot guarantee access."

func TestBatchPlanCloudPacingOutput(t *testing.T) {
	stubConfigPath(t)
	for _, mode := range []string{"human", "json", "agent", "selected"} {
		t.Run(mode, func(t *testing.T) {
			job := batchTestPlan(t, runner.ModelCloud, "a.txt", "b.txt", "c.txt")
			withBatchTestStore(t, newBatchTestStore())
			cmd := NewRootCmd(func() runner.Runner {
				t.Fatal("plan must not construct a model runner")
				return nil
			})
			args := []string{"batch", "plan", "--input-dir", job.InputDir, "--prompt-file", job.InstructionSource,
				"--model", "cloud", "--output-dir", job.OutputDir, "--job", job.JobPath}
			switch mode {
			case "json":
				args = append(args, "--json")
			case "agent", "selected":
				args = append(args, "--agent")
				if mode == "selected" {
					args = append(args, "--select", "spacing_seconds,spacing_reason,minimum_pacing_seconds")
				}
			}
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if mode == "human" {
				for _, want := range []string{"Cloud calls wait 30 s after completion. " + cloudPacingReasonWant,
					"minimum pacing time: 1m0s (plus model runtime)"} {
					if !strings.Contains(out.String(), want) {
						t.Fatalf("plan missing %q: %s", want, out.String())
					}
				}
				return
			}
			var data map[string]any
			if err := json.Unmarshal(out.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if mode == "agent" || mode == "selected" {
				if data["meta"].(map[string]any)["schema_version"] != "2" {
					t.Fatalf("agent schema changed: %s", out.String())
				}
				data = data["results"].(map[string]any)
			}
			if data["spacing_seconds"] != float64(30) || data["spacing_reason"] != cloudPacingReasonWant ||
				data["minimum_pacing_seconds"] != float64(60) {
				t.Fatalf("pacing fields = %s", out.String())
			}
			if mode == "selected" && len(data) != 3 {
				t.Fatalf("selection leaked fields: %s", out.String())
			}
		})
	}
}

func TestBatchPacingHelp(t *testing.T) {
	for _, args := range [][]string{{"batch", "--help"}, {"batch", "run", "--help"}, {"batch", "resume", "--help"}, {"batch", "plan", "--help"}} {
		cmd := NewRootCmd(func() runner.Runner {
			t.Fatal("help must not construct a model runner")
			return nil
		})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Cloud 30 s; Cloud Pro 45 s; ChatGPT 15 s.", "On-Device and Local have no pacing.", cloudPacingReasonWant} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%v missing %q: %s", args, want, out.String())
			}
		}
	}
}

type batchPacingTestClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *batchPacingTestClock) Now() time.Time { return c.now }
func (c *batchPacingTestClock) Wait(ctx context.Context, wait time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.waits = append(c.waits, wait)
	c.now = c.now.Add(wait)
	return nil
}

// Hide discovery to keep this fixture wholly offline. Shortcut execution still
// uses the real transport, explicitly pointed at a synthetic shell program.
type batchOfflineShortcut struct {
	transport *runner.ShortcutRunner
	calls     int
}

func (r *batchOfflineShortcut) Run(ctx context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	return r.transport.Run(ctx, model, prompt)
}

func TestBatchOfflinePacingAndOneNoticePerInvocation(t *testing.T) {
	stubConfigPath(t)
	for _, tc := range []struct {
		model runner.Model
		gap   time.Duration
	}{
		{runner.ModelCloud, 30 * time.Second},
		{runner.ModelCloudPro, 45 * time.Second},
		{runner.ModelChatGPT, 15 * time.Second},
		{runner.ModelLocal, 0},
		{runner.ModelOnDevice, 0},
	} {
		t.Run(string(tc.model), func(t *testing.T) {
			job := batchTestPlan(t, tc.model, "a.txt", "b.txt", "c.txt")
			store := newBatchTestStore()
			if err := store.Create(t.Context(), job.JobPath, job); err != nil {
				t.Fatal(err)
			}
			withBatchTestStore(t, store)
			clock := &batchPacingTestClock{now: time.Unix(10_000, 0).UTC()}
			oldClock := newBatchClock
			newBatchClock = func() batch.Clock { return clock }
			t.Cleanup(func() { newBatchClock = oldClock })
			local := &batchTestRunner{}
			var fake runner.Runner = local
			var shortcut *batchOfflineShortcut
			countPath := filepath.Join(t.TempDir(), "calls")
			if tc.model != runner.ModelLocal {
				path := filepath.Join(t.TempDir(), "shortcuts")
				script := "#!/bin/sh\nprintf 'call\\n' >> '" + countPath + "'\nprintf 'fixture result'\n"
				if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				transport := runner.New()
				transport.ShortcutsPath = path
				shortcut = &batchOfflineShortcut{transport: transport}
				fake = shortcut
			}
			for index, invocation := range []string{"run", "resume"} {
				budget := "1"
				if index == 1 {
					budget = "2"
				}
				cmd := newBatchCmd(&rootFlags{asJSON: true}, func() runner.Runner { return fake })
				var out, stderr bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&stderr)
				cmd.SetArgs([]string{invocation, "--job", job.JobPath, "--max-calls", budget})
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				wantNotices := 0
				if index == 1 && tc.gap > 0 {
					wantNotices = 1
				}
				if got := strings.Count(stderr.String(), "hollis: batch pacing:"); got != wantNotices {
					t.Fatalf("notices=%d want=%d: %s", got, wantNotices, stderr.String())
				}
				if wantNotices == 1 && tc.model == runner.ModelCloud &&
					!strings.Contains(stderr.String(), "Cloud calls wait 30 s after completion. "+cloudPacingReasonWant) {
					t.Fatalf("Cloud notice lost reason: %s", stderr.String())
				}
				if !json.Valid(out.Bytes()) {
					t.Fatalf("stderr note corrupted JSON: %s", out.String())
				}
			}
			wantWaits := 2
			if tc.gap == 0 {
				wantWaits = 0
			}
			if len(clock.waits) != wantWaits {
				t.Fatalf("waits=%v, want %d", clock.waits, wantWaits)
			}
			for _, wait := range clock.waits {
				if wait != tc.gap {
					t.Fatalf("wait=%s want=%s", wait, tc.gap)
				}
			}
			if shortcut != nil {
				calls, err := os.ReadFile(countPath)
				if err != nil || strings.Count(string(calls), "call\n") != 3 || shortcut.calls != 3 {
					t.Fatalf("fake shortcuts calls=%q err=%v", calls, err)
				}
			} else if local.calls != 3 {
				t.Fatalf("Local calls=%d", local.calls)
			}
			t.Logf("offline %s: waits=%v; one notice per waiting invocation", tc.model, clock.waits)
		})
	}
}
