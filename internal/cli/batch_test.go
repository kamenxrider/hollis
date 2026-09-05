// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/runner"
)

type batchTestRunner struct {
	mu          sync.Mutex
	calls       int
	textPrompts []string
	imagePaths  []string
	imagePrompt string
	imageMode   os.FileMode
	dirMode     os.FileMode
	imageBytes  []byte
}

func (r *batchTestRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.textPrompts = append(r.textPrompts, prompt)
	return "text result", model, nil
}

func (r *batchTestRunner) RunWithImages(_ context.Context, model runner.Model, prompt string, imagePaths []string) (string, runner.Model, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.imagePrompt = prompt
	r.imagePaths = append([]string(nil), imagePaths...)
	if len(imagePaths) == 1 {
		if info, err := os.Stat(imagePaths[0]); err == nil {
			r.imageMode = info.Mode().Perm()
		}
		r.dirMode = func() os.FileMode {
			if info, err := os.Stat(filepath.Dir(imagePaths[0])); err == nil {
				return info.Mode().Perm()
			}
			return 0
		}()
		r.imageBytes, _ = os.ReadFile(imagePaths[0])
	}
	return "image result", model, nil
}

type batchTestLock struct{}

func (batchTestLock) Unlock() error { return nil }

type batchTestStore struct {
	mu      sync.Mutex
	job     *batch.Job
	results map[string]batch.ResultRef
	content map[string][]byte
}

func newBatchTestStore() *batchTestStore {
	return &batchTestStore{results: make(map[string]batch.ResultRef), content: make(map[string][]byte)}
}

func (s *batchTestStore) Create(_ context.Context, path string, job *batch.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil || path != job.JobPath {
		return errors.New("job already exists")
	}
	s.job = cloneBatchTestJob(job)
	return nil
}

func (s *batchTestStore) Lock(_ context.Context, _ string) (batch.Lock, error) {
	return batchTestLock{}, nil
}

func (s *batchTestStore) Load(_ context.Context, path string) (*batch.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil || s.job.JobPath != path {
		return nil, os.ErrNotExist
	}
	return cloneBatchTestJob(s.job), nil
}

func (s *batchTestStore) Save(_ context.Context, path string, job *batch.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil || s.job.JobPath != path {
		return os.ErrNotExist
	}
	s.job = cloneBatchTestJob(job)
	return nil
}

func (s *batchTestStore) CommitResult(_ context.Context, path string, request batch.ExecutionRequest, output batch.ExecutionOutput) (batch.ResultRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil || s.job.JobPath != path {
		return batch.ResultRef{}, os.ErrNotExist
	}
	var item batch.Item
	for _, candidate := range s.job.Items {
		if candidate.ID == request.ItemID {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		return batch.ResultRef{}, errors.New("unknown item")
	}
	checksum := digestBytes(output.Content)
	ref := batch.ResultRef{
		ID:        batch.ResultID(request.RequestID, checksum),
		RequestID: request.RequestID,
		Path:      filepath.Join(s.job.OutputDir, item.ResultName),
		SHA256:    checksum,
		Bytes:     int64(len(output.Content)),
		ModelUsed: output.ModelUsed,
	}
	s.results[request.RequestID] = ref
	s.content[request.RequestID] = append([]byte(nil), output.Content...)
	return ref, nil
}

func (s *batchTestStore) VerifyResult(_ context.Context, _ string, item batch.Item) (batch.ResultRef, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.results[item.CurrentRequestID]
	return ref, ok, nil
}

func cloneBatchTestJob(job *batch.Job) *batch.Job {
	if job == nil {
		return nil
	}
	copyJob := *job
	copyJob.Items = append([]batch.Item(nil), job.Items...)
	for index := range copyJob.Items {
		if job.Items[index].Result != nil {
			result := *job.Items[index].Result
			copyJob.Items[index].Result = &result
		}
	}
	copyJob.Skipped = append([]batch.Skipped(nil), job.Skipped...)
	return &copyJob
}

func withBatchTestStore(t *testing.T, store *batchTestStore) {
	t.Helper()
	old := newBatchStore
	newBatchStore = func() batchJobStore { return store }
	t.Cleanup(func() { newBatchStore = old })
}

func batchTestPlan(t *testing.T, model runner.Model, names ...string) *batch.Job {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	output := filepath.Join(dir, "output")
	if err := os.Mkdir(input, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(input, name), []byte("source "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	instructions := filepath.Join(dir, "instructions.txt")
	if err := os.WriteFile(instructions, []byte("Follow the instructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := batch.Plan(batch.PlanOptions{
		InputDir: input, OutputDir: output, JobPath: filepath.Join(dir, "job.json"),
		InstructionSource: instructions, Model: model, CreatedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestBatchPlanMakesZeroRunnerCallsAndRecordsSkippedEntries(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input"), filepath.Join(dir, "output")
	if err := os.Mkdir(input, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "note.txt"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "skip.pdf"), []byte("unsupported"), 0o600); err != nil {
		t.Fatal(err)
	}
	instructions := filepath.Join(dir, "instructions.txt")
	if err := os.WriteFile(instructions, []byte("Follow the instructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newBatchTestStore()
	withBatchTestStore(t, store)
	runnerCalls := 0
	cmd := newBatchCmd(&rootFlags{asJSON: true}, func() runner.Runner {
		runnerCalls++
		return &batchTestRunner{}
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"plan", "--input-dir", input, "--prompt-file", instructions, "--model", "cloud", "--output-dir", output, "--job", filepath.Join(dir, "job.json")})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if runnerCalls != 0 {
		t.Fatalf("runner factory calls = %d, want zero for plan", runnerCalls)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("plan JSON: %v (%q)", err, out.String())
	}
	if got["item_count"] != float64(1) || got["skipped_count"] != float64(1) {
		t.Fatalf("plan summary = %s", out.String())
	}
}

func TestBatchExecutorDispatchesPreparedTextAndCleansStagedImage(t *testing.T) {
	dir := t.TempDir()
	instructions := filepath.Join(dir, "instructions.txt")
	textPath := filepath.Join(dir, "source.txt")
	imagePath := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(instructions, []byte("Describe the source."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(textPath, []byte("A small document."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("PNG fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &batchTestRunner{}
	executor := &hollisBatchExecutor{newRunner: func() runner.Runner { return r }}
	textOut, err := executor.Execute(context.Background(), batch.ExecutionRequest{
		Model: runner.ModelCloud, Kind: batch.ItemText, SourcePath: textPath, InstructionSource: instructions,
		InputSHA256: digestBytes([]byte("A small document.")), InstructionSHA256: digestBytes([]byte("Describe the source.")),
	})
	if err != nil {
		t.Fatalf("text execute: %v", err)
	}
	if string(textOut.Content) != "text result" || len(r.textPrompts) != 1 || !strings.Contains(r.textPrompts[0], "A small document.") {
		t.Fatalf("text dispatch: output=%q prompts=%q", textOut.Content, r.textPrompts)
	}
	imageOut, err := executor.Execute(context.Background(), batch.ExecutionRequest{
		Model: runner.ModelCloud, Kind: batch.ItemImage, SourcePath: imagePath, InstructionSource: instructions,
		InputSHA256: digestBytes([]byte("PNG fixture bytes")), InstructionSHA256: digestBytes([]byte("Describe the source.")),
	})
	if err != nil {
		t.Fatalf("image execute: %v", err)
	}
	if string(imageOut.Content) != "image result" || len(r.imagePaths) != 1 {
		t.Fatalf("image dispatch: output=%q paths=%q", imageOut.Content, r.imagePaths)
	}
	if r.imageMode != 0o600 || r.dirMode != 0o700 || string(r.imageBytes) != "PNG fixture bytes" {
		t.Fatalf("staged image permissions/content = file %#o dir %#o bytes %q", r.imageMode, r.dirMode, r.imageBytes)
	}
	if _, err := os.Stat(r.imagePaths[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged image still exists after dispatch: path=%q err=%v", r.imagePaths[0], err)
	}
}

func TestBatchRunPreflightsEveryInputBeforeCallingRunner(t *testing.T) {
	job := batchTestPlan(t, runner.ModelOnDevice, "a.txt", "b.txt")
	if err := os.WriteFile(job.Items[1].SourcePath, []byte("changed after planning"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newBatchTestStore()
	if err := store.Create(context.Background(), job.JobPath, job); err != nil {
		t.Fatal(err)
	}
	r := &batchTestRunner{}
	_, err := batch.Run(context.Background(), job.JobPath, batch.InvocationOptions{MaxCalls: 2}, &hollisBatchExecutor{newRunner: func() runner.Runner { return r }}, store, realBatchClock{})
	if err == nil || !strings.Contains(err.Error(), `input "b.txt" changed`) {
		t.Fatalf("Run error = %v, want later-input preflight error", err)
	}
	if r.calls != 0 {
		t.Fatalf("runner calls = %d, want zero after full preflight failure", r.calls)
	}
}

func TestBatchResumeHonorsNewBudgetAndSkipsSucceededItems(t *testing.T) {
	job := batchTestPlan(t, runner.ModelOnDevice, "a.txt", "b.txt")
	store := newBatchTestStore()
	if err := store.Create(context.Background(), job.JobPath, job); err != nil {
		t.Fatal(err)
	}
	r := &batchTestRunner{}
	withBatchTestStore(t, store)
	first := newBatchCmd(&rootFlags{asJSON: true}, func() runner.Runner { return r })
	first.SetOut(&bytes.Buffer{})
	first.SetErr(&bytes.Buffer{})
	first.SetArgs([]string{"run", "--job", job.JobPath, "--max-calls", "1"})
	if err := first.Execute(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("first calls = %d, want 1", r.calls)
	}
	second := newBatchCmd(&rootFlags{asJSON: true}, func() runner.Runner { return r })
	var secondOut bytes.Buffer
	second.SetOut(&secondOut)
	second.SetErr(&bytes.Buffer{})
	second.SetArgs([]string{"resume", "--job", job.JobPath, "--max-calls", "1"})
	if err := second.Execute(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if r.calls != 2 {
		t.Fatalf("resume calls = %d, want one new provider call", r.calls)
	}
	stored, err := store.Load(context.Background(), job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Items[0].Status != batch.StatusSucceeded || stored.Items[1].Status != batch.StatusSucceeded {
		t.Fatalf("statuses after resume = %q, %q; want both succeeded", stored.Items[0].Status, stored.Items[1].Status)
	}
	if stored.Items[0].Attempts+stored.Items[1].Attempts != 2 {
		t.Fatalf("lifetime attempts = %d, want 2", stored.Items[0].Attempts+stored.Items[1].Attempts)
	}
	var summary map[string]any
	if err := json.Unmarshal(secondOut.Bytes(), &summary); err != nil {
		t.Fatalf("resume JSON: %v (%q)", err, secondOut.String())
	}
	if summary["total_attempts"] != float64(2) {
		t.Fatalf("resume total_attempts = %v, want 2", summary["total_attempts"])
	}
}

func TestBatchRunRequiresExplicitBoundedBudget(t *testing.T) {
	r := &batchTestRunner{}
	cmd := newBatchCmd(&rootFlags{asJSON: true}, func() runner.Runner { return r })
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "--job", "job.json"})
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--max-calls is required") {
		t.Fatalf("error = %v exit=%d, want usage for missing budget", err, ExitCode(err))
	}
	if r.calls != 0 {
		t.Fatalf("runner calls = %d, want zero", r.calls)
	}
}
