// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/examples"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func TestExamplesPrintWithoutStateOrRunner(t *testing.T) {
	old := configPath
	configPath = func() (string, error) { t.Fatal("examples read config"); return "", nil }
	t.Cleanup(func() { configPath = old })
	for _, mode := range []string{"", "--json", "--agent"} {
		cmd, flags := newRootCmdWithFlags(func() runner.Runner { t.Fatal("examples constructed a runner"); return nil })
		var out bytes.Buffer
		cmd.SetOut(&out)
		args := []string{"examples"}
		if mode != "" {
			args = append(args, mode)
		}
		cmd.SetArgs(args)
		if err := executeCommand(cmd, flags); err != nil {
			t.Fatal(err)
		}
		if mode == "" {
			if out.String() != examples.Content {
				t.Fatal("printed guide differs from embedded recipes")
			}
		} else {
			var value map[string]any
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if mode == "--agent" {
				value = value["results"].(map[string]any)
			}
			if value["format"] != "markdown" || value["content"] != examples.Content {
				t.Fatal("JSON guide differs")
			}
		}
	}
}

func TestExamplesSelectionAndExtraArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		exit int
	}{
		{[]string{"examples", "--json", "--select", "format"}, 0},
		{[]string{"examples", "--agent", "--select", "content"}, 0},
		{[]string{"examples", "extra"}, 2},
		{[]string{"examples", "--json", "--select", "unknown"}, 2},
	} {
		cmd, flags := newRootCmdWithFlags(func() runner.Runner { t.Fatal("unexpected runner"); return nil })
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(tc.args)
		err := executeCommand(cmd, flags)
		if ExitCode(err) != tc.exit {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if tc.exit == 0 {
			var row map[string]any
			if err := json.Unmarshal(out.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(tc.args, "--agent") {
				row = row["results"].(map[string]any)
			}
			if len(row) != 1 {
				t.Fatalf("selection ignored: %s", out.String())
			}
		}
	}
}

type exampleInvocation struct {
	Args   []string     `json:"args"`
	Model  runner.Model `json:"model"`
	Prompt string       `json:"prompt"`
	Schema string       `json:"schema"`
	Calls  int          `json:"calls"`
}

const recipeCode = "func Reverse(values []string) { for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 { values[i], values[j] = values[j], values[i] } }\n"

// Only this test subprocess substitutes a transport. The production command
// has no test-mode flag or environment bypass.
func TestExamplesCLIProcess(t *testing.T) {
	if os.Getenv("HOLLIS_EXAMPLE_PROCESS") != "1" {
		t.Skip("recipe subprocess only")
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing command arguments")
	}
	args := os.Args[separator+1:]
	r := &schemaFixtureRunner{text: "fixture answer", structured: json.RawMessage(`{"name":"Ada"}`)}
	if slices.Contains(args, "--code") {
		r.text = "```go\n" + recipeCode + "```\n"
	}
	if slices.Contains(args, "--schema") {
		r.text = `{"name":"Ada"}`
	}
	listInstalledShortcuts = func(context.Context) ([]string, error) { t.Fatal("recipe attempted real discovery"); return nil, nil }
	macosMajorVersion = func() int { t.Fatal("recipe attempted platform probe"); return 0 }
	macosBuild = func() string { t.Fatal("recipe attempted platform probe"); return "" }
	cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return r }, nil)
	resolved, err := ResolveCommandLine(cmd, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) == 0 || !slices.Contains([]string{"respond", "chat", "chats"}, resolved[0]) {
		t.Fatalf("recipe uses a command outside the offline harness: %v", resolved)
	}
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	cmd.SetArgs(resolved)
	err = executeCommand(cmd, flags)
	log, logErr := os.OpenFile(os.Getenv("HOLLIS_EXAMPLE_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if logErr != nil {
		t.Fatal(logErr)
	}
	logErr = json.NewEncoder(log).Encode(exampleInvocation{Args: args, Model: r.consumed, Prompt: r.prompt, Schema: r.schemaText, Calls: r.calls})
	if closeErr := log.Close(); logErr != nil || closeErr != nil {
		t.Fatalf("record invocation: %v %v", logErr, closeErr)
	}
	if err != nil && !ErrorReported(err) {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(ExitCode(err))
}

func exampleRecipes(t *testing.T) map[string]string {
	t.Helper()
	recipes := make(map[string]string)
	heading := ""
	var script strings.Builder
	inBlock, count := false, 0
	for _, line := range strings.Split(examples.Content, "\n") {
		if !inBlock && strings.HasPrefix(line, "## ") {
			heading = strings.TrimPrefix(line, "## ")
		}
		if !inBlock && line == "```sh" {
			inBlock = true
			script.Reset()
			continue
		}
		if inBlock && line == "```" {
			count++
			recipes[heading] = script.String()
			inBlock = false
			continue
		}
		if inBlock {
			script.WriteString(line)
			script.WriteByte('\n')
		}
	}
	if inBlock || count != 8 || len(recipes) != 8 {
		t.Fatalf("incomplete recipe coverage: open=%t count=%d", inBlock, count)
	}
	return recipes
}

func TestExamplesRecipesVerbatimOffline(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			if _, err := os.Stat(shell); os.IsNotExist(err) {
				t.Skip("shell not installed")
			}
			for heading, body := range exampleRecipes(t) {
				t.Run(heading, func(t *testing.T) {
					dir := t.TempDir()
					bin, home, state := filepath.Join(dir, "bin"), filepath.Join(dir, "home"), filepath.Join(dir, "state")
					for _, path := range []string{bin, home, state} {
						if err := os.Mkdir(path, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					wrapper := "#!/bin/sh\nexec \"$HOLLIS_EXAMPLE_TEST_BINARY\" -test.run='^TestExamplesCLIProcess$' -- \"$@\"\n"
					if err := os.WriteFile(filepath.Join(bin, "hollis"), []byte(wrapper), 0o700); err != nil {
						t.Fatal(err)
					}
					logPath := filepath.Join(dir, "calls.jsonl")
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, shell, "-c", body)
					cmd.Dir = dir
					cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "HOLLIS_STATE_DIR=" + state,
						"HOLLIS_EXAMPLE_PROCESS=1", "HOLLIS_EXAMPLE_LOG=" + logPath, "HOLLIS_EXAMPLE_TEST_BINARY=" + executable}
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("verbatim recipe failed: %v\n%s", err, out)
					}
					raw, err := os.ReadFile(logPath)
					if err != nil {
						t.Fatal(err)
					}
					decoder := json.NewDecoder(bytes.NewReader(raw))
					requests := []exampleInvocation{}
					for {
						var call exampleInvocation
						err := decoder.Decode(&call)
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						if call.Calls > 0 {
							requests = append(requests, call)
						}
					}
					if len(requests) != 1 || requests[0].Calls != 1 || requests[0].Prompt == "" {
						t.Fatalf("requests=%+v", requests)
					}
					request := requests[0]
					wantModel := runner.ModelCloud
					if heading == "Request a structured local answer" {
						wantModel = runner.ModelLocal
					}
					if request.Model != wantModel {
						t.Fatalf("model=%s want=%s", request.Model, wantModel)
					}
					switch heading {
					case "Summarize meeting notes":
						if !strings.Contains(request.Prompt, "Summarize these notes") || !strings.Contains(request.Prompt, "Release notes: tests pass.") {
							t.Fatal("piped instruction or attachment lost")
						}
					case "Save code for review":
						code, err := os.ReadFile(filepath.Join(dir, "reverse.go"))
						if err != nil || string(code) != recipeCode {
							t.Fatalf("code=%q err=%v", code, err)
						}
					case "Request a structured local answer":
						var envelope struct {
							Results struct {
								Response map[string]any `json:"response"`
							} `json:"results"`
						}
						if err := json.Unmarshal(out, &envelope); err != nil || envelope.Results.Response["name"] != "Ada" || request.Schema == "" {
							t.Fatalf("guided answer=%s err=%v", out, err)
						}
					case "Save and find a conversation":
						st, err := store.Open(filepath.Join(state, "hollis.db"))
						if err != nil {
							t.Fatal(err)
						}
						defer st.Close()
						chats, err := st.ListConversations(false)
						if err != nil || len(chats) != 1 || chats[0].Model != "cloud" || chats[0].Messages != 2 {
							t.Fatalf("chats=%v err=%v", chats, err)
						}
					}
				})
			}
		})
	}
}
