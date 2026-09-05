// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/cli"
	"github.com/kamenxrider/hollis/internal/runner"
)

// Exercise the registered public commands with actual durable storage. Only
// model execution is fake; no Shortcuts discovery or model process is launched.
func TestPublicBatchCommandsPersistAndResumeWithoutRepeatingSuccess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOLLIS_STATE_DIR", filepath.Join(root, "state"))
	input := filepath.Join(root, "input")
	if err := os.Mkdir(input, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a.txt": "first body", "b.md": "second body", "ignored.pdf": "unsupported"} {
		if err := os.WriteFile(filepath.Join(input, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	instruction := filepath.Join(root, "instruction.txt")
	if err := os.WriteFile(instruction, []byte("Summarize"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	jobPath, err := filepath.Rel(cwd, filepath.Join(root, "job.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &documentRecordingRunner{}
	execute := func(args ...string) (map[string]any, error) {
		cmd := cli.NewRootCmd(func() runner.Runner { return fake })
		cmd.SetArgs(args)
		cmd.SetIn(strings.NewReader(""))
		var out, stderr bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		if err := cmd.Execute(); err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal(out.Bytes(), &data); err != nil {
			t.Fatalf("invalid CLI JSON %q: %v", out.String(), err)
		}
		if results, ok := data["results"].(map[string]any); ok {
			return results, nil
		}
		return data, nil
	}
	plan, err := execute("batch", "plan", "--input-dir", input, "--prompt-file", instruction, "--model", "on-device", "--output-dir", filepath.Join(root, "results"), "--job", jobPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 || plan["item_count"] != float64(2) || plan["skipped_count"] != float64(1) {
		t.Fatalf("plan=%v calls=%d", plan, fake.calls)
	}
	first, err := execute("batch", "run", "--job", jobPath, "--max-calls", "1", "--agent")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || first["budget_exhausted"] != true || first["complete"] != false {
		t.Fatalf("first=%v calls=%d", first, fake.calls)
	}
	resumed, err := execute("batch", "resume", "--job", jobPath, "--max-calls", "1", "--agent", "--select", "attempted,total_attempts,complete")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 2 || resumed["attempted"] != float64(1) || resumed["total_attempts"] != float64(2) || resumed["complete"] != true {
		t.Fatalf("resume=%v calls=%d", resumed, fake.calls)
	}
	again, err := execute("batch", "resume", "--job", jobPath, "--max-calls", "1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 2 || again["attempted"] != float64(0) || again["complete"] != true {
		t.Fatalf("repeated resume=%v calls=%d", again, fake.calls)
	}
	job, err := batch.NewLocalStore().Load(t.Context(), jobPath)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := job.Items[0].Result.Path
	if !strings.HasSuffix(resultPath, ".response.json") {
		t.Fatalf("unexpected result format path %q", resultPath)
	}
	if err := os.WriteFile(resultPath, []byte("corrupt result"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute("batch", "resume", "--job", jobPath, "--max-calls", "1", "--json"); err == nil {
		t.Fatal("corrupt success was accepted")
	}
	if fake.calls != 2 {
		t.Fatalf("corrupt result triggered an extra model call: %d", fake.calls)
	}
}
