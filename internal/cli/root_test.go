// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// fakeRunner returns canned responses/errors without touching the transport.
type fakeRunner struct {
	err error
}

func (f *fakeRunner) Run(_ context.Context, _ runner.Model, prompt string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return prompt, nil
}

func TestRespondEmptyPromptExitsUsage2(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "   "})
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want error for empty prompt")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestRespondJSONOutput(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "--model", "cloud", "hello world"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	// printJSONFiltered writes to os.Stdout; swap it for a pipe.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model"] != "cloud" || got["response"] != "hello world" {
		t.Fatalf("unexpected JSON: %s", buf.String())
	}
}

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		kind runner.Kind
		want int
	}{
		{runner.KindEmptyPrompt, 2},
		{runner.KindUsage, 2},
		{runner.KindShortcutMissing, 3},
		{runner.KindNoOutput, 5},
		{runner.KindSIGABRT, 5},
		{runner.KindTransport, 5},
		{runner.KindTimeout, 7},
	}
	for _, tc := range cases {
		err := toCLIError(&runner.Error{Kind: tc.kind, ExitCode: -1, Err: errors.New("x")})
		if got := ExitCode(err); got != tc.want {
			t.Fatalf("kind %s: exit = %d, want %d", tc.kind, got, tc.want)
		}
	}
}

func TestUnknownModelFlagExitsUsage(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--model", "nope", "hello"})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestRespondNewModelsAccepted(t *testing.T) {
	for _, model := range []string{"on-device", "chatgpt"} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs([]string{"respond", "--model", model, "hello"})
		cmd.SetOut(&bytes.Buffer{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("respond --model %s: %v", model, err)
		}
	}
}

func TestRespondDefaultsToAuto(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "hello"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model"] != "auto" {
		t.Fatalf("default model = %v, want auto", got["model"])
	}
}

func TestSplitModelArgs(t *testing.T) {
	tier, rest, has := splitModelArgs([]string{"model", "cloud-pro", "Draft", "a", "reply"})
	if !has || tier != "cloud-pro" || strings.Join(rest, " ") != "Draft a reply" {
		t.Fatalf("splitModelArgs = (%q, %v, %v)", tier, rest, has)
	}
	// "model" followed by a non-tier is a literal prompt.
	tier, rest, has = splitModelArgs([]string{"model", "not-a-tier", "hello"})
	if has || tier != "" || strings.Join(rest, " ") != "model not-a-tier hello" {
		t.Fatalf("splitModelArgs literal-prompt case = (%q, %v, %v)", tier, rest, has)
	}
	// No prefix at all.
	tier, rest, has = splitModelArgs([]string{"just", "a", "prompt"})
	if has || tier != "" || strings.Join(rest, " ") != "just a prompt" {
		t.Fatalf("splitModelArgs no-prefix case = (%q, %v, %v)", tier, rest, has)
	}
}

func TestRespondPositionalModelSelectsTier(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "model", "on-device", "hello"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model"] != "on-device" || got["response"] != "hello" {
		t.Fatalf("unexpected JSON: %s", buf.String())
	}
}

func TestAgentContextSchemaPresent(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"agent-context"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent-context: %v", err)
	}
}

func TestModelsCommandListsAllTiers(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"models"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("models: %v", err)
	}
	for _, tier := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		if !bytes.Contains(out.Bytes(), []byte(tier)) {
			t.Fatalf("models output missing tier %q: %q", tier, out.String())
		}
	}
	// Internal Shortcuts jargon stays out of the human surface; it lives
	// in --json and the README.
	if bytes.Contains(out.Bytes(), []byte("WFLLMModel")) {
		t.Fatalf("human output must not leak internal parameter names: %q", out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("pcc-")) || bytes.Contains(out.Bytes(), []byte("gpt-")) {
		t.Fatalf("models output must not contain fabricated backend model IDs: %q", out.String())
	}
}

func TestModelsCommandJSONShape(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"models", "--json"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if len(got) != 4 {
		t.Fatalf("models JSON rows = %d, want 4", len(got))
	}
	for _, row := range got {
		for _, field := range []string{"model", "wfllm_model", "apple_model", "bridge_uuid"} {
			if _, ok := row[field]; !ok {
				t.Fatalf("models JSON row missing %q: %v", field, row)
			}
		}
	}
}

func TestVersionFlag(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"--version"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("hollis")) {
		t.Fatalf("version output missing name: %q", out.String())
	}
}
