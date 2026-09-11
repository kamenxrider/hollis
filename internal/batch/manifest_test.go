// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestPlanInventoriesSupportedFilesDeterministically(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	mustMkdir(t, input)
	mustWrite(t, filepath.Join(input, "z.TXT"), "last")
	mustWrite(t, filepath.Join(input, "a.md"), "first")
	mustWrite(t, filepath.Join(input, "photo.JPEG"), "jpeg fixture")
	mustWrite(t, filepath.Join(input, "ignore.pdf"), "unsupported")
	mustMkdir(t, filepath.Join(input, "nested"))
	mustWrite(t, filepath.Join(input, "nested", "hidden.txt"), "not recursive")
	instructions := filepath.Join(root, "instructions.txt")
	mustWrite(t, instructions, "Summarize the supplied file.")

	options := PlanOptions{
		InputDir: input, OutputDir: filepath.Join(root, "output"),
		JobPath: filepath.Join(root, "job.json"), InstructionSource: instructions,
		Model: runner.ModelCloud, CreatedAt: time.Unix(1_000, 0),
	}
	first, err := Plan(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Plan(options)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != second.ID || !slices.EqualFunc(first.Items, second.Items, func(a, b Item) bool {
		return a.ID == b.ID && a.ResultName == b.ResultName && a.InputSHA256 == b.InputSHA256
	}) {
		t.Fatalf("repeated plans differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if got := itemNames(first.Items); !slices.Equal(got, []string{"a.md", "photo.JPEG", "z.TXT"}) {
		t.Fatalf("item order = %v", got)
	}
	if got := skippedNames(first.Skipped); !slices.Equal(got, []string{"ignore.pdf", "nested"}) {
		t.Fatalf("skipped = %v", got)
	}
	if first.Skipped[0].Reason != "unsupported file type" || !strings.Contains(first.Skipped[1].Reason, "nonrecursive") {
		t.Fatalf("skipped reasons = %+v", first.Skipped)
	}
	if first.Items[0].Kind != ItemText || first.Items[1].Kind != ItemImage || first.Items[2].Kind != ItemText {
		t.Fatalf("item kinds = %+v", first.Items)
	}
	for _, item := range first.Items {
		if !strings.HasPrefix(item.ID, "item_") || !strings.HasSuffix(item.ResultName, ".response.json") {
			t.Fatalf("unstable item identity: %+v", item)
		}
		if item.Status != StatusPending || item.Attempts != 0 || item.Result != nil {
			t.Fatalf("new item state = %+v", item)
		}
	}
	if first.InstructionSHA256 != digest("Summarize the supplied file.") || first.InstructionBytes != 28 {
		t.Fatalf("instruction identity = %s/%d", first.InstructionSHA256, first.InstructionBytes)
	}
	if first.Items[0].InputSHA256 != digest("first") || first.Items[0].InputBytes != 5 {
		t.Fatalf("input identity = %+v", first.Items[0])
	}
	if err := ValidateJob(first); err != nil {
		t.Fatalf("ValidateJob: %v", err)
	}
	if err := RevalidateSources(first); err != nil {
		t.Fatalf("RevalidateSources: %v", err)
	}
}

func TestPlanRejectsSymlinkEntry(t *testing.T) {
	root, input, instruction := planFixture(t, "cloud", map[string]string{"ok.txt": "ok"})
	if err := os.Symlink(filepath.Join(input, "ok.txt"), filepath.Join(input, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	_, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Plan error = %v", err)
	}
}

func TestPlanRejectsUnsafePathOverlapIncludingSymlinkParent(t *testing.T) {
	tests := []struct {
		name   string
		paths  func(root, input string) (output, job string)
		needle string
	}{
		{
			name: "output inside input",
			paths: func(root, input string) (string, string) {
				return filepath.Join(input, "results"), filepath.Join(root, "job.json")
			},
			needle: "input and output directories must not overlap",
		},
		{
			name: "input inside output",
			paths: func(root, input string) (string, string) {
				return root, filepath.Join(root, "job.json")
			},
			needle: "input and output directories must not overlap",
		},
		{
			name: "job inside input",
			paths: func(root, input string) (string, string) {
				return filepath.Join(root, "results"), filepath.Join(input, "job.json")
			},
			needle: "job path must not be inside the input directory",
		},
		{
			name: "job inside output",
			paths: func(root, input string) (string, string) {
				output := filepath.Join(root, "results")
				return output, filepath.Join(output, "job.json")
			},
			needle: "job path must not be inside the output directory",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, input, instruction := planFixture(t, "cloud", map[string]string{"ok.txt": "ok"})
			output, job := test.paths(root, input)
			options := planOptions(root, input, instruction, runner.ModelCloud)
			options.OutputDir, options.JobPath = output, job
			_, err := Plan(options)
			if err == nil || !strings.Contains(err.Error(), test.needle) {
				t.Fatalf("Plan error = %v, want %q", err, test.needle)
			}
		})
	}

	t.Run("symlinked output parent resolves into input", func(t *testing.T) {
		root, input, instruction := planFixture(t, "cloud", map[string]string{"ok.txt": "ok"})
		alias := filepath.Join(root, "alias")
		if err := os.Symlink(input, alias); err != nil {
			t.Fatal(err)
		}
		options := planOptions(root, input, instruction, runner.ModelCloud)
		options.OutputDir = filepath.Join(alias, "results")
		_, err := Plan(options)
		if err == nil || !strings.Contains(err.Error(), "must not overlap") {
			t.Fatalf("Plan error = %v", err)
		}
	})
}

func TestPlanReportsEmptyAndUnsupportedFolders(t *testing.T) {
	root, input, instruction := planFixture(t, "cloud", nil)
	_, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
	if err == nil || !strings.Contains(err.Error(), "no supported regular files") {
		t.Fatalf("empty Plan error = %v", err)
	}
	mustWrite(t, filepath.Join(input, "only.pdf"), "pdf")
	_, err = Plan(planOptions(root, input, instruction, runner.ModelCloud))
	if err == nil || !strings.Contains(err.Error(), "no supported regular files") {
		t.Fatalf("unsupported-only Plan error = %v", err)
	}
}

func TestPlanValidatesConcreteModelAndImageCompatibility(t *testing.T) {
	tests := []struct {
		name   string
		model  runner.Model
		files  map[string]string
		ok     bool
		needle string
	}{
		{name: "auto rejected", model: runner.ModelAuto, files: map[string]string{"a.txt": "a"}, needle: "concrete model"},
		{name: "unknown rejected", model: "future", files: map[string]string{"a.txt": "a"}, needle: "unsupported batch model"},
		{name: "on-device text", model: runner.ModelOnDevice, files: map[string]string{"a.txt": "a"}, ok: true},
		{name: "on-device image rejected", model: runner.ModelOnDevice, files: map[string]string{"a.png": "png"}, needle: "cannot contain images"},
		{name: "cloud mixed", model: runner.ModelCloud, files: map[string]string{"a.txt": "a", "b.jpg": "jpg"}, ok: true},
		{name: "cloud-pro mixed", model: runner.ModelCloudPro, files: map[string]string{"a.md": "a", "b.jpeg": "jpeg"}, ok: true},
		{name: "chatgpt image item", model: runner.ModelChatGPT, files: map[string]string{"b.png": "png"}, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, input, instruction := planFixture(t, string(test.model), test.files)
			job, err := Plan(planOptions(root, input, instruction, test.model))
			if test.ok && err != nil {
				t.Fatal(err)
			}
			if !test.ok && (err == nil || !strings.Contains(err.Error(), test.needle)) {
				t.Fatalf("Plan = %+v, error = %v, want %q", job, err, test.needle)
			}
		})
	}
}

func TestPlanBoundsHashReadsAndValidatesUTF8(t *testing.T) {
	t.Run("instruction limit", func(t *testing.T) {
		root, input, instruction := planFixture(t, "instruction-limit", map[string]string{"a.txt": "a"})
		mustWrite(t, instruction, "12345")
		options := planOptions(root, input, instruction, runner.ModelCloud)
		options.MaxInstructionBytes = 4
		_, err := Plan(options)
		if err == nil || !strings.Contains(err.Error(), "4-byte limit") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("text limit", func(t *testing.T) {
		root, input, instruction := planFixture(t, "text-limit", map[string]string{"a.txt": "12345"})
		options := planOptions(root, input, instruction, runner.ModelCloud)
		options.MaxTextInputBytes = 4
		_, err := Plan(options)
		if err == nil || !strings.Contains(err.Error(), "4-byte limit") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("image limit", func(t *testing.T) {
		root, input, instruction := planFixture(t, "image-limit", map[string]string{"a.png": "12345"})
		options := planOptions(root, input, instruction, runner.ModelCloud)
		options.MaxImageInputBytes = 4
		_, err := Plan(options)
		if err == nil || !strings.Contains(err.Error(), "4-byte limit") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("invalid UTF-8 instruction", func(t *testing.T) {
		root, input, instruction := planFixture(t, "utf8-instruction", map[string]string{"a.txt": "a"})
		if err := os.WriteFile(instruction, []byte{0xff}, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
		if err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("invalid UTF-8 text", func(t *testing.T) {
		root, input, instruction := planFixture(t, "utf8-text", map[string]string{"a.txt": "a"})
		if err := os.WriteFile(filepath.Join(input, "a.txt"), []byte{0xff}, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
		if err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("whitespace text", func(t *testing.T) {
		root, input, instruction := planFixture(t, "empty-text", map[string]string{"a.txt": " \t\n"})
		_, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
		if err == nil || !strings.Contains(err.Error(), "non-empty content") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("empty image", func(t *testing.T) {
		root, input, instruction := planFixture(t, "empty-image", map[string]string{"a.png": ""})
		_, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
		if err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("prepared text limit includes wrapper", func(t *testing.T) {
		root, input, instruction := planFixture(t, "rendered-limit", map[string]string{
			"a.txt": strings.Repeat("x", MaxPreparedPromptBytes-100),
		})
		mustWrite(t, instruction, strings.Repeat("i", 100))
		_, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
		if err == nil || !strings.Contains(err.Error(), "prepared prompt") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("configured limit cannot raise product ceiling", func(t *testing.T) {
		root, input, instruction := planFixture(t, "raised-limit", map[string]string{"a.txt": "a"})
		options := planOptions(root, input, instruction, runner.ModelCloud)
		options.MaxTextInputBytes = DefaultMaxTextInputBytes + 1
		_, err := Plan(options)
		if err == nil || !strings.Contains(err.Error(), "must not exceed") {
			t.Fatalf("Plan error = %v", err)
		}
	})
}

func TestBoundedReadRejectsSpecialFilesWithoutBlockingOrOverflow(t *testing.T) {
	t.Run("fifo", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "input.txt")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, _, _, err := readRegularBounded(path, 128, false)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("readRegularBounded error = %v", err)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("readRegularBounded blocked on a FIFO")
		}
	})
	t.Run("maximum int64 limit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "input.txt")
		mustWrite(t, path, "small")
		hash, size, _, err := readRegularBounded(path, math.MaxInt64, false)
		if err != nil || hash != digest("small") || size != 5 {
			t.Fatalf("readRegularBounded = %s/%d/%v", hash, size, err)
		}
	})
}

func TestRevalidateSourcesDetectsChangesAndReplacementSymlinks(t *testing.T) {
	tests := []struct {
		name   string
		change func(t *testing.T, job *Job)
		needle string
	}{
		{
			name: "instruction content", needle: "instruction source changed",
			change: func(t *testing.T, job *Job) { mustWrite(t, job.InstructionSource, "No the task.") },
		},
		{
			name: "input content same size", needle: "input \"a.txt\" changed",
			change: func(t *testing.T, job *Job) { mustWrite(t, job.Items[0].SourcePath, "zzzz") },
		},
		{
			name: "input grows", needle: "input \"a.txt\" changed",
			change: func(t *testing.T, job *Job) { mustWrite(t, job.Items[0].SourcePath, "longer") },
		},
		{
			name: "input becomes symlink", needle: "direct regular file",
			change: func(t *testing.T, job *Job) {
				target := filepath.Join(filepath.Dir(job.InputDir), "target.txt")
				mustWrite(t, target, "aaaa")
				if err := os.Remove(job.Items[0].SourcePath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, job.Items[0].SourcePath); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, input, instruction := planFixture(t, test.name, map[string]string{"a.txt": "aaaa"})
			job, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
			if err != nil {
				t.Fatal(err)
			}
			test.change(t, job)
			err = RevalidateSources(job)
			if err == nil || !strings.Contains(err.Error(), test.needle) {
				t.Fatalf("RevalidateSources error = %v, want %q", err, test.needle)
			}
		})
	}
}

func TestPlanRejectsExistingJobAndResultDestinations(t *testing.T) {
	t.Run("job", func(t *testing.T) {
		root, input, instruction := planFixture(t, "existing-job", map[string]string{"a.txt": "a"})
		options := planOptions(root, input, instruction, runner.ModelCloud)
		mustWrite(t, options.JobPath, "existing")
		_, err := Plan(options)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("Plan error = %v", err)
		}
	})
	t.Run("reserved result", func(t *testing.T) {
		root, input, instruction := planFixture(t, "existing-result", map[string]string{"a.txt": "a"})
		options := planOptions(root, input, instruction, runner.ModelCloud)
		job, err := Plan(options)
		if err != nil {
			t.Fatal(err)
		}
		mustMkdir(t, options.OutputDir)
		mustWrite(t, filepath.Join(options.OutputDir, job.Items[0].ResultName), "collision")
		_, err = Plan(options)
		if err == nil || !strings.Contains(err.Error(), "result destination") {
			t.Fatalf("Plan error = %v", err)
		}
	})
}

func TestValidateJobRejectsSchemaAndRecoveryStateErrors(t *testing.T) {
	root, input, instruction := planFixture(t, "schema", map[string]string{"a.txt": "aaaa"})
	job, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("unknown schema", func(t *testing.T) {
		copy := *job
		copy.SchemaVersion++
		if err := ValidateJob(&copy); err == nil || !strings.Contains(err.Error(), "unsupported batch schema") {
			t.Fatalf("ValidateJob error = %v", err)
		}
	})
	t.Run("tampered item hash", func(t *testing.T) {
		copy := cloneJob(job)
		copy.Items[0].InputSHA256 = strings.Repeat("0", 64)
		if err := ValidateJob(copy); err == nil || !strings.Contains(err.Error(), "does not match item identity") {
			t.Fatalf("ValidateJob error = %v", err)
		}
	})
	t.Run("declared input exceeds product bound", func(t *testing.T) {
		copy := cloneJob(job)
		copy.Items[0].InputBytes = DefaultMaxTextInputBytes + 1
		if err := ValidateJob(copy); err == nil || !strings.Contains(err.Error(), "input_bytes exceeds") {
			t.Fatalf("ValidateJob error = %v", err)
		}
	})
	t.Run("running without attempt", func(t *testing.T) {
		copy := cloneJob(job)
		copy.Items[0].Status = StatusRunning
		if err := ValidateJob(copy); err == nil || !strings.Contains(err.Error(), "requires an attempt") {
			t.Fatalf("ValidateJob error = %v", err)
		}
	})
	t.Run("valid succeeded result", func(t *testing.T) {
		copy := cloneJob(job)
		item := &copy.Items[0]
		item.Status, item.Attempts = StatusSucceeded, 1
		item.CurrentRequestID = RequestID(copy.ID, item.ID, item.Attempts)
		resultHash := digest("answer")
		item.Result = &ResultRef{
			ID: ResultID(item.CurrentRequestID, resultHash), RequestID: item.CurrentRequestID,
			Path: filepath.Join(copy.OutputDir, item.ResultName), SHA256: resultHash,
			Bytes: 6, ModelUsed: runner.ModelCloud,
		}
		if err := ValidateJob(copy); err != nil {
			t.Fatalf("ValidateJob: %v", err)
		}
		item.Result.ID = "result_tampered"
		if err := ValidateJob(copy); err == nil || !strings.Contains(err.Error(), "result id") {
			t.Fatalf("ValidateJob error = %v", err)
		}
	})
}

func TestRevalidateItemChecksMembershipAndCurrentBytes(t *testing.T) {
	root, input, instruction := planFixture(t, "single-item", map[string]string{"a.txt": "aaaa", "b.md": "bbbb"})
	job, err := Plan(planOptions(root, input, instruction, runner.ModelCloud))
	if err != nil {
		t.Fatal(err)
	}
	if err := RevalidateItem(job, job.Items[1]); err != nil {
		t.Fatal(err)
	}
	foreign := job.Items[1]
	foreign.ID = job.Items[0].ID
	if err := RevalidateItem(job, foreign); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign RevalidateItem error = %v", err)
	}
	mustWrite(t, job.Items[1].SourcePath, "zzzz")
	if err := RevalidateItem(job, job.Items[1]); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed RevalidateItem error = %v", err)
	}
}

func TestManifestOmitsZeroPacingCheckpoint(t *testing.T) {
	raw, err := json.Marshal(Job{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "next_call_not_before") {
		t.Fatalf("zero pacing checkpoint was serialized: %s", raw)
	}
}

func TestInvocationOptionsValidateBudgetAndExplicitUncertainChoice(t *testing.T) {
	for _, calls := range []int{1, MaxCallsPerInvocation} {
		if err := (InvocationOptions{MaxCalls: calls}).Validate(); err != nil {
			t.Fatalf("MaxCalls %d: %v", calls, err)
		}
	}
	for _, calls := range []int{0, MaxCallsPerInvocation + 1} {
		if err := (InvocationOptions{MaxCalls: calls}).Validate(); err == nil {
			t.Fatalf("MaxCalls %d accepted", calls)
		}
	}
	if err := (InvocationOptions{MaxCalls: 1, RetryUncertain: true, SkipUncertain: true}).Validate(); err == nil {
		t.Fatal("conflicting uncertain choices accepted")
	}
}

func TestFrozenInterfaceSignatures(t *testing.T) {
	var _ Executor = contractExecutor{}
	var _ Clock = contractClock{}
	var _ Lock = contractLock{}
	var _ Store = contractStore{}
}

type contractExecutor struct{}

func (contractExecutor) Execute(context.Context, ExecutionRequest) (ExecutionOutput, error) {
	return ExecutionOutput{}, nil
}

type contractClock struct{}

func (contractClock) Now() time.Time                            { return time.Time{} }
func (contractClock) Wait(context.Context, time.Duration) error { return nil }

type contractLock struct{}

func (contractLock) Unlock() error { return nil }

type contractStore struct{}

func (contractStore) Lock(context.Context, string) (Lock, error) { return contractLock{}, nil }
func (contractStore) Load(context.Context, string) (*Job, error) { return nil, nil }
func (contractStore) Save(context.Context, string, *Job) error   { return nil }
func (contractStore) CommitResult(context.Context, string, ExecutionRequest, ExecutionOutput) (ResultRef, error) {
	return ResultRef{}, nil
}
func (contractStore) VerifyResult(context.Context, string, Item) (ResultRef, bool, error) {
	return ResultRef{}, false, nil
}

func planFixture(t *testing.T, name string, files map[string]string) (root, input, instruction string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), strings.ReplaceAll(name, " ", "-"))
	input = filepath.Join(root, "input")
	mustMkdir(t, input)
	for filename, content := range files {
		mustWrite(t, filepath.Join(input, filename), content)
	}
	instruction = filepath.Join(root, "instructions.txt")
	mustWrite(t, instruction, "Do the task.")
	return root, input, instruction
}

func planOptions(root, input, instruction string, model runner.Model) PlanOptions {
	return PlanOptions{
		InputDir: input, OutputDir: filepath.Join(root, "output"),
		JobPath: filepath.Join(root, "job.json"), InstructionSource: instruction,
		Model: model, CreatedAt: time.Unix(1_000, 0),
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func digest(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

func itemNames(items []Item) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func skippedNames(skipped []Skipped) []string {
	names := make([]string, 0, len(skipped))
	for _, item := range skipped {
		names = append(names, item.Name)
	}
	return names
}

func cloneJob(job *Job) *Job {
	copy := *job
	copy.Items = slices.Clone(job.Items)
	copy.Skipped = slices.Clone(job.Skipped)
	return &copy
}

func TestNativeLocalBatchPlanAcceptsTextRejectsImages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		wantErr bool
	}{
		{"text", map[string]string{"note.txt": "Hello", "other.md": "Second"}, false},
		{"image", map[string]string{"photo.png": "fixture", "note.txt": "Hello"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, input, instruction := planFixture(t, "local", tc.files)
			job, err := Plan(planOptions(root, input, instruction, runner.ModelLocal))
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "local batch jobs cannot contain images") {
					t.Fatalf("%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateJob(job); err != nil {
				t.Fatal(err)
			}
			if job.Model != runner.ModelLocal {
				t.Fatal("local silently substituted")
			}
		})
	}
}
