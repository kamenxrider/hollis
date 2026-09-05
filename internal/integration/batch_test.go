// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/runner"
)

func TestBatchRealStoreRecoversResultCommittedBeforeManifestSuccess(t *testing.T) {
	t.Parallel()
	store, job := integrationBatchJob(t)
	faults := &saveFaultStore{Store: store, failAt: 2}
	executor := &countingBatchExecutor{}
	clock := &integrationBatchClock{now: time.Unix(10_000, 0).UTC()}

	first, err := batch.Run(t.Context(), job.JobPath, batch.InvocationOptions{MaxCalls: 1}, executor, faults, clock)
	if err == nil || !strings.Contains(err.Error(), "persist successful item") {
		t.Fatalf("first Run error = %v, want final manifest save failure", err)
	}
	if first.Attempted != 1 || executor.calls != 1 {
		t.Fatalf("first Run = %+v, provider calls = %d", first, executor.calls)
	}
	durable, err := store.Load(t.Context(), job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Items[0].Status != batch.StatusRunning || durable.Items[0].Result != nil {
		t.Fatalf("durable crash checkpoint = %+v, want running without manifest result", durable.Items[0])
	}

	second, err := batch.Run(t.Context(), job.JobPath, batch.InvocationOptions{MaxCalls: 1}, executor, store, clock)
	if err != nil {
		t.Fatalf("recovery Run: %v", err)
	}
	if second.Succeeded != 1 || !second.Complete || second.Attempted != 0 || executor.calls != 1 {
		t.Fatalf("recovery Run = %+v, provider calls = %d; want recovered success without recall", second, executor.calls)
	}
	final, err := store.Load(t.Context(), job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	if final.Items[0].Status != batch.StatusSucceeded || final.Items[0].Result == nil {
		t.Fatalf("final item = %+v, want durable success", final.Items[0])
	}
}

func TestBatchRealStoreRejectsInsecureOutputBeforeProviderCall(t *testing.T) {
	t.Parallel()
	store, job := integrationBatchJob(t)
	if err := os.Chmod(job.OutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executor := &countingBatchExecutor{}
	clock := &integrationBatchClock{now: time.Unix(10_000, 0).UTC()}

	result, err := batch.Run(t.Context(), job.JobPath, batch.InvocationOptions{MaxCalls: 1}, executor, store, clock)
	if err == nil || !strings.Contains(err.Error(), "not private") {
		t.Fatalf("Run error = %v, want output privacy failure", err)
	}
	if result.Attempted != 0 || executor.calls != 0 {
		t.Fatalf("Run = %+v, provider calls = %d; insecure output spent a call", result, executor.calls)
	}
}

func TestBatchRealStoreRefusesPreexistingOutputWithoutOverwrite(t *testing.T) {
	t.Parallel()
	store, job := integrationBatchJob(t)
	destination := filepath.Join(job.OutputDir, job.Items[0].ResultName)
	original := []byte("unrelated user output")
	if err := os.WriteFile(destination, original, 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &countingBatchExecutor{}
	clock := &integrationBatchClock{now: time.Unix(10_000, 0).UTC()}

	result, err := batch.Run(t.Context(), job.JobPath, batch.InvocationOptions{MaxCalls: 1}, executor, store, clock)
	if err == nil || !strings.Contains(err.Error(), "result destination") {
		t.Fatalf("Run error = %v, want destination collision", err)
	}
	if result.Attempted != 0 || executor.calls != 0 {
		t.Fatalf("Run = %+v, provider calls = %d; collision spent a call", result, executor.calls)
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("collision handling overwrote unrelated output")
	}
}

type saveFaultStore struct {
	batch.Store
	saves  int
	failAt int
}

func (store *saveFaultStore) Save(ctx context.Context, jobPath string, job *batch.Job) error {
	store.saves++
	if store.saves == store.failAt {
		return errors.New("injected manifest save failure")
	}
	return store.Store.Save(ctx, jobPath, job)
}

type countingBatchExecutor struct {
	calls int
}

func (executor *countingBatchExecutor) Execute(_ context.Context, request batch.ExecutionRequest) (batch.ExecutionOutput, error) {
	executor.calls++
	return batch.ExecutionOutput{Content: []byte("fixture result"), ModelUsed: request.Model}, nil
}

type integrationBatchClock struct {
	now time.Time
}

func (clock *integrationBatchClock) Now() time.Time { return clock.now }

func (clock *integrationBatchClock) Wait(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		clock.now = clock.now.Add(duration)
		return nil
	}
}

func integrationBatchJob(t *testing.T) (*batch.LocalStore, *batch.Job) {
	t.Helper()
	root := t.TempDir()
	inputDir := filepath.Join(root, "input")
	if err := os.Mkdir(inputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(inputDir, "a.txt")
	if err := os.WriteFile(input, []byte("fixture input"), 0o600); err != nil {
		t.Fatal(err)
	}
	instructions := filepath.Join(root, "instructions.txt")
	if err := os.WriteFile(instructions, []byte("summarize"), 0o600); err != nil {
		t.Fatal(err)
	}
	jobPath := filepath.Join(root, "state", "job.json")
	job, err := batch.Plan(batch.PlanOptions{
		InputDir: inputDir, OutputDir: filepath.Join(root, "output"), JobPath: jobPath,
		InstructionSource: instructions, Model: runner.ModelCloud, CreatedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := batch.NewLocalStore()
	if err := store.Create(t.Context(), jobPath, job); err != nil {
		t.Fatal(err)
	}
	return store, job
}
