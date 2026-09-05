// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestLocalStoreCreateLoadAndNoClobber(t *testing.T) {
	t.Parallel()
	store, job := plannedStoreJob(t)

	loaded, err := store.Load(t.Context(), job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != job.ID || loaded.JobPath != job.JobPath {
		t.Fatalf("loaded wrong job: %#v", loaded)
	}
	assertMode(t, job.JobPath, 0o600)
	assertMode(t, job.OutputDir, 0o700)

	original, err := os.ReadFile(job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), job.JobPath, job); err == nil {
		t.Fatal("Create replaced an existing manifest")
	}
	after, err := os.ReadFile(job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("failed Create changed the existing manifest")
	}
}

func TestLocalStoreNormalizesRelativeAndTmpAliasPaths(t *testing.T) {
	t.Parallel()
	t.Run("relative", func(t *testing.T) {
		root := t.TempDir()
		inputDir := filepath.Join(root, "input")
		storeMustMkdir(t, inputDir, 0o700)
		storeMustWrite(t, filepath.Join(inputDir, "a.txt"), "input")
		instructions := filepath.Join(root, "instructions.txt")
		storeMustWrite(t, instructions, "summarize")
		absoluteJobPath := filepath.Join(root, "state", "job.json")
		workingDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		relativeJobPath, err := filepath.Rel(workingDir, absoluteJobPath)
		if err != nil {
			t.Fatal(err)
		}
		job, err := Plan(PlanOptions{
			InputDir: inputDir, OutputDir: filepath.Join(root, "output"), JobPath: relativeJobPath,
			InstructionSource: instructions, Model: runner.ModelCloud, CreatedAt: time.Unix(1, 0).UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		store := NewLocalStore()
		if err := store.Create(t.Context(), relativeJobPath, job); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(t.Context(), relativeJobPath); err != nil {
			t.Fatal(err)
		}
		lock := mustLock(t, store, relativeJobPath)
		if err := lock.Unlock(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("tmp alias", func(t *testing.T) {
		root, err := os.MkdirTemp("/tmp", "hollis-batch-store-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(root)
		inputDir := filepath.Join(root, "input")
		storeMustMkdir(t, inputDir, 0o700)
		storeMustWrite(t, filepath.Join(inputDir, "a.txt"), "input")
		instructions := filepath.Join(root, "instructions.txt")
		storeMustWrite(t, instructions, "summarize")
		aliasJobPath := filepath.Join(root, "state", "job.json")
		job, err := Plan(PlanOptions{
			InputDir: inputDir, OutputDir: filepath.Join(root, "output"), JobPath: aliasJobPath,
			InstructionSource: instructions, Model: runner.ModelCloud, CreatedAt: time.Unix(1, 0).UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		store := NewLocalStore()
		if err := store.Create(t.Context(), aliasJobPath, job); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(t.Context(), aliasJobPath); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLocalStoreRejectsInsecureExistingOutputDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inputDir := filepath.Join(root, "input")
	outputDir := filepath.Join(root, "output")
	storeMustMkdir(t, inputDir, 0o700)
	storeMustMkdir(t, outputDir, 0o755)
	storeMustWrite(t, filepath.Join(inputDir, "a.txt"), "input")
	instructions := filepath.Join(root, "instructions.txt")
	storeMustWrite(t, instructions, "summarize")
	jobPath := filepath.Join(root, "job.json")
	job, err := Plan(PlanOptions{
		InputDir: inputDir, OutputDir: outputDir, JobPath: jobPath,
		InstructionSource: instructions, Model: runner.ModelCloud, CreatedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewLocalStore().Create(t.Context(), jobPath, job); err == nil || !strings.Contains(err.Error(), "not private") {
		t.Fatalf("Create error = %v, want private-permissions rejection", err)
	}
	assertMode(t, outputDir, 0o755)
}

func TestLocalStoreLockIsExclusiveStableAndReleased(t *testing.T) {
	t.Parallel()
	firstStore, job := plannedStoreJob(t)
	secondStore := NewLocalStore()
	first, err := firstStore.Lock(t.Context(), job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := job.JobPath + ".lock"
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.Lock(t.Context(), job.JobPath); err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Fatalf("competing Lock error = %v", err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("unlock replaced or removed the stable lock file")
	}
	second, err := secondStore.Lock(t.Context(), job.JobPath)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	if err := second.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalStoreLockReleasedOnProcessDeath(t *testing.T) {
	t.Parallel()
	store, job := plannedStoreJob(t)
	command := exec.Command(os.Args[0], "-test.run=^TestLocalStoreLockHelperProcess$")
	command.Env = append(os.Environ(),
		"GO_WANT_BATCH_LOCK_HELPER=1",
		"HOLLIS_BATCH_LOCK_JOB_PATH="+job.JobPath,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("lock helper readiness = %q, %v", line, err)
	}
	lockPath := job.JobPath + ".lock"
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lock(t.Context(), job.JobPath); err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Fatalf("Lock while helper owns flock error = %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed lock helper exited successfully")
	}
	waited = true

	lock, err := store.Lock(t.Context(), job.JobPath)
	if err != nil {
		t.Fatalf("Lock after helper death: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("process death caused the stable lock file to be replaced")
	}
}

func TestLocalStoreLockHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_BATCH_LOCK_HELPER") != "1" {
		return
	}
	store := NewLocalStore()
	if _, err := store.Lock(context.Background(), os.Getenv("HOLLIS_BATCH_LOCK_JOB_PATH")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "locked")
	for {
		time.Sleep(time.Hour)
	}
}

func TestLocalStoreSaveRequiresLockAndPreservesValidatedIdentity(t *testing.T) {
	t.Parallel()
	store, job := plannedStoreJob(t)
	if err := store.Save(t.Context(), job.JobPath, job); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("Save without lock error = %v", err)
	}

	lock := mustLock(t, store, job.JobPath)
	defer lock.Unlock()
	job.Items[0].Attempts = 1
	job.Items[0].CurrentRequestID = RequestID(job.ID, job.Items[0].ID, 1)
	job.Items[0].Status = StatusRunning
	if err := store.Save(t.Context(), job.JobPath, job); err != nil {
		t.Fatal(err)
	}
	assertMode(t, job.JobPath, 0o600)

	unrelated := *job
	unrelated.JobPath = filepath.Join(filepath.Dir(job.JobPath), "other.json")
	if err := store.Save(t.Context(), job.JobPath, &unrelated); err == nil {
		t.Fatal("Save accepted a different manifest identity")
	}
}

func TestLocalStoreCommitAndVerifyEnvelope(t *testing.T) {
	t.Parallel()
	store, job := plannedStoreJob(t)
	lock := mustLock(t, store, job.JobPath)
	defer lock.Unlock()
	request := persistRunning(t, store, job)

	content := []byte("durable response\n")
	ref, err := store.CommitResult(t.Context(), job.JobPath, request, ExecutionOutput{Content: content, ModelUsed: job.Model})
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, ref.Path, 0o600)
	verified, found, err := store.VerifyResult(t.Context(), job.JobPath, job.Items[0])
	if err != nil || !found || verified != ref {
		t.Fatalf("VerifyResult = (%#v, %v, %v), want (%#v, true, nil)", verified, found, err, ref)
	}

	var raw map[string]json.RawMessage
	data, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "request", "result", "content"} {
		if len(raw[key]) == 0 {
			t.Fatalf("result envelope is missing %q", key)
		}
	}
	var storedRequest ExecutionRequest
	if err := json.Unmarshal(raw["request"], &storedRequest); err != nil || storedRequest != request {
		t.Fatalf("stored request = %#v, %v; want %#v", storedRequest, err, request)
	}

	if _, err := store.CommitResult(t.Context(), job.JobPath, request, ExecutionOutput{Content: []byte("replacement"), ModelUsed: job.Model}); err == nil {
		t.Fatal("CommitResult overwrote an existing destination")
	}
	after, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatal("failed second commit changed the first result")
	}
}

func TestLocalStoreCommitRejectsInvalidRequestAndOutput(t *testing.T) {
	t.Parallel()
	store, job := plannedStoreJob(t)
	lock := mustLock(t, store, job.JobPath)
	defer lock.Unlock()
	request := persistRunning(t, store, job)

	tests := []struct {
		name    string
		request ExecutionRequest
		output  ExecutionOutput
	}{
		{name: "wrong request", request: func() ExecutionRequest {
			changed := request
			changed.InputSHA256 = strings.Repeat("0", 64)
			return changed
		}(), output: ExecutionOutput{Content: []byte("ok"), ModelUsed: job.Model}},
		{name: "empty", request: request, output: ExecutionOutput{ModelUsed: job.Model}},
		{name: "invalid utf8", request: request, output: ExecutionOutput{Content: []byte{0xff}, ModelUsed: job.Model}},
		{name: "wrong model", request: request, output: ExecutionOutput{Content: []byte("ok"), ModelUsed: runner.ModelCloudPro}},
		{name: "too large", request: request, output: ExecutionOutput{Content: make([]byte, MaxResultContentBytes+1), ModelUsed: job.Model}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.CommitResult(t.Context(), job.JobPath, test.request, test.output); err == nil {
				t.Fatal("CommitResult accepted invalid input")
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(job.OutputDir, job.Items[0].ResultName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid commit created destination: %v", err)
	}
}

func TestLocalStoreVerifyRejectsCorruptionMismatchAndSpecialFiles(t *testing.T) {
	t.Parallel()
	t.Run("corrupt", func(t *testing.T) {
		store, job := plannedStoreJob(t)
		request := withRunningManifest(t, store, job)
		_ = request
		storeMustWrite(t, filepath.Join(job.OutputDir, job.Items[0].ResultName), "{bad")
		if _, found, err := store.VerifyResult(t.Context(), job.JobPath, job.Items[0]); err == nil || found {
			t.Fatalf("VerifyResult = found %v, err %v", found, err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		store, job := plannedStoreJob(t)
		withRunningManifest(t, store, job)
		target := filepath.Join(t.TempDir(), "target")
		storeMustWrite(t, target, "{}")
		if err := os.Symlink(target, filepath.Join(job.OutputDir, job.Items[0].ResultName)); err != nil {
			t.Fatal(err)
		}
		if _, found, err := store.VerifyResult(t.Context(), job.JobPath, job.Items[0]); err == nil || found {
			t.Fatalf("VerifyResult = found %v, err %v", found, err)
		}
	})
	t.Run("fifo", func(t *testing.T) {
		store, job := plannedStoreJob(t)
		withRunningManifest(t, store, job)
		path := filepath.Join(job.OutputDir, job.Items[0].ResultName)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() {
			_, _, err := store.VerifyResult(context.Background(), job.JobPath, job.Items[0])
			finished <- err
		}()
		select {
		case err := <-finished:
			if err == nil {
				t.Fatal("VerifyResult accepted FIFO")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("VerifyResult blocked opening FIFO")
		}
	})
}

func TestLocalStoreVerifyDetectsTamperedEnvelope(t *testing.T) {
	t.Parallel()
	store, job := plannedStoreJob(t)
	lock := mustLock(t, store, job.JobPath)
	request := persistRunning(t, store, job)
	ref, err := store.CommitResult(t.Context(), job.JobPath, request, ExecutionOutput{Content: []byte("original"), ModelUsed: job.Model})
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), "original", "tampered", 1)
	if err := os.WriteFile(ref.Path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.VerifyResult(t.Context(), job.JobPath, job.Items[0]); err == nil || found {
		t.Fatalf("VerifyResult = found %v, err %v; want checksum error", found, err)
	}
}

func TestLocalStoreLoadIsStrictBoundedAndDoesNotFollowSymlink(t *testing.T) {
	t.Parallel()
	store, job := plannedStoreJob(t)
	data, err := os.ReadFile(job.JobPath)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := strings.Replace(string(data), "{", "{\n  \"unknown\": true,", 1)
	if err := os.WriteFile(job.JobPath, []byte(withUnknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(t.Context(), job.JobPath); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("Load error = %v, want unknown-field rejection", err)
	}

	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	storeMustWrite(t, target, "{}")
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(t.Context(), link); err == nil {
		t.Fatal("Load followed a manifest symlink")
	}
}

func plannedStoreJob(t *testing.T) (*LocalStore, *Job) {
	t.Helper()
	root := t.TempDir()
	inputDir := filepath.Join(root, "input")
	storeMustMkdir(t, inputDir, 0o700)
	storeMustWrite(t, filepath.Join(inputDir, "a.txt"), "input")
	instructions := filepath.Join(root, "instructions.txt")
	storeMustWrite(t, instructions, "summarize")
	jobPath := filepath.Join(root, "state", "job.json")
	job, err := Plan(PlanOptions{
		InputDir: inputDir, OutputDir: filepath.Join(root, "output"), JobPath: jobPath,
		InstructionSource: instructions, Model: runner.ModelCloud, CreatedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := NewLocalStore()
	if err := store.Create(t.Context(), jobPath, job); err != nil {
		t.Fatal(err)
	}
	return store, job
}

func persistRunning(t *testing.T, store *LocalStore, job *Job) ExecutionRequest {
	t.Helper()
	item := &job.Items[0]
	item.Attempts++
	item.CurrentRequestID = RequestID(job.ID, item.ID, item.Attempts)
	item.Status = StatusRunning
	item.Failure = ""
	item.Result = nil
	if err := store.Save(t.Context(), job.JobPath, job); err != nil {
		t.Fatal(err)
	}
	return executionRequestFor(job, *item)
}

func withRunningManifest(t *testing.T, store *LocalStore, job *Job) ExecutionRequest {
	t.Helper()
	lock := mustLock(t, store, job.JobPath)
	request := persistRunning(t, store, job)
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	return request
}

func mustLock(t *testing.T, store *LocalStore, jobPath string) Lock {
	t.Helper()
	lock, err := store.Lock(t.Context(), jobPath)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func storeMustMkdir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
}

func storeMustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
