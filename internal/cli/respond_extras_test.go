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

	"github.com/kamenxrider/hollis/internal/runner"
)

type extrasRunner struct {
	sampledCLIRunner
	counts  int
	request runner.GenerationRequest
}

func (r *extrasRunner) CountTokens(_ context.Context, request runner.GenerationRequest, reserve int) (runner.TokenCount, error) {
	r.counts++
	r.request = request
	return runner.TokenCount{Tokens: 7, ContextSize: 4096, AnswerReserve: reserve, Fits: true}, nil
}

func TestRespondInstructionsAndPermissive(t *testing.T) {
	file := filepath.Join(t.TempDir(), "system.txt")
	if err := os.WriteFile(file, []byte("Answer briefly"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--system", "Answer briefly"}, {"-s", "Answer briefly"}, {"--system-file", file}, {"--permissive"}} {
		r := &sampledCLIRunner{}
		out, err := executeTools(t, r, append(append([]string{"respond", "--model", "local"}, args...), "Hello")...)
		if err != nil || out != "Sampled.\n" || len(r.generations) != 1 {
			t.Fatalf("%v out=%q err=%v", args, out, err)
		}
		if args[0] == "--permissive" {
			if !r.generations[0].Permissive {
				t.Fatal("permissive not passed")
			}
		} else if r.generations[0].Instructions != "Answer briefly" {
			t.Fatal("instructions not passed")
		}
		if r.generations[0].Prompt != "Hello" {
			t.Fatal("instructions flattened on Local")
		}
	}
}

func TestRespondMessagesAndTokenCount(t *testing.T) {
	messages := `[{"role":"system","content":"Be brief"},{"role":"developer","content":"Use digits"},{"role":"user","content":"two plus two?"},{"role":"assistant","content":"4"},{"role":"user","content":"and one?"}]`
	r := &extrasRunner{}
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(messages))
	cmd.SetArgs([]string{"respond", "--messages", "-", "--count-tokens", "--json"})
	if err := executeCommand(cmd, flags); err != nil {
		t.Fatal(err)
	}
	var count map[string]any
	if err := json.Unmarshal(out.Bytes(), &count); err != nil {
		t.Fatal(err)
	}
	if r.counts != 1 || len(r.generations) != 0 || count["tokens"] != float64(7) || count["fits"] != true {
		t.Fatalf("%s counts=%d", out.String(), r.counts)
	}
	if r.request.Instructions != "Be brief\n\nUse digits" || len(r.request.History) != 2 || r.request.Prompt != "and one?" {
		t.Fatalf("%+v", r.request)
	}
}

func TestRespondExtrasRejectBeforeInference(t *testing.T) {
	for _, args := range [][]string{
		{"--system", "a", "--system-file", "missing", "hi"},
		{"--messages", "-", "hi"},
		{"--messages", "-", "--prompt-file", "-"},
		{"--model", "cloud", "--permissive", "hi"},
		{"--model", "chatgpt", "--count-tokens", "hi"},
		{"--answer-reserve", "4", "hi"},
		{"--count-tokens", "--answer-reserve", "-1", "hi"},
		{"--count-tokens", "--image", "missing", "hi"},
	} {
		r := &extrasRunner{}
		_, err := executeTools(t, r, append([]string{"respond"}, args...)...)
		if ExitCode(err) != 2 || len(r.generations) != 0 || r.counts != 0 {
			t.Fatalf("%v err=%v", args, err)
		}
	}
	for _, messages := range []string{`[]`, `[{"role":"assistant","content":"x"}]`, `[{"role":"tool","content":"x"}]`, `[{"role":"user","content":"","unknown":true}]`} {
		r := &extrasRunner{}
		cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(messages))
		cmd.SetArgs([]string{"respond", "--messages", "-", "--count-tokens"})
		if err := executeCommand(cmd, flags); ExitCode(err) != 2 || r.counts != 0 {
			t.Fatalf("%s: %v", messages, err)
		}
	}
}

func TestQuietKeepsAnswerAndErrors(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		r := &sampledCLIRunner{}
		cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
		var out, notes bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&notes)
		cmd.SetIn(strings.NewReader(""))
		args := []string{"respond", "--model", "local", "--permissive", "Hello"}
		if quiet {
			args = append(args, "-q")
		}
		cmd.SetArgs(args)
		if err := executeCommand(cmd, flags); err != nil {
			t.Fatal(err)
		}
		if out.String() != "Sampled.\n" || (notes.Len() > 0) == quiet {
			t.Fatalf("quiet=%v out=%q notes=%q", quiet, out.String(), notes.String())
		}
	}
	r := &extrasRunner{}
	_, err := executeTools(t, r, "respond", "-q", "--permissive", "--model", "cloud", "Hi")
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "requires --model local") {
		t.Fatal(err)
	}
}
