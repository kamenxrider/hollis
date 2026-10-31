// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
)

func TestSelectionSchemaCoversDataCommands(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, mode := range commandOutputModes(cmd) {
			if mode == "json" && cmd.Name() != "help" && commandResultSchemas[cmd.CommandPath()] == nil {
				t.Errorf("missing deterministic result schema for %s", cmd.CommandPath())
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	for _, child := range cmd.Commands() {
		walk(child)
	}
}

func TestSelectionGroupCommands(t *testing.T) {
	root := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	var groups [][]string
	var walk func(*cobra.Command, []string)
	walk = func(cmd *cobra.Command, path []string) {
		if cmd.HasSubCommands() && cmd != root {
			groups = append(groups, path)
		}
		for _, child := range cmd.Commands() {
			walk(child, append(append([]string{}, path...), child.Name()))
		}
	}
	walk(root, nil)
	for _, path := range groups {
		for _, mode := range []string{"--json", "--agent"} {
			for _, help := range []bool{false, true} {
				args := append(append([]string{}, path...), mode, "--select", "bogus")
				if help {
					args = append(args, "--help")
				}
				t.Run(strings.Join(args, " "), func(t *testing.T) {
					cmd, flags := newRootCmdWithFlags(func() runner.Runner {
						t.Fatal("group constructed runner")
						return nil
					})
					var out bytes.Buffer
					cmd.SetOut(&out)
					cmd.SetErr(io.Discard)
					cmd.SetArgs(args)
					err := executeCommand(cmd, flags)
					var payload struct {
						Error struct {
							Code string `json:"code"`
							Exit int    `json:"exit_code"`
						} `json:"error"`
					}
					if ExitCode(err) != 2 || json.Unmarshal(out.Bytes(), &payload) != nil || payload.Error.Code != "usage" || payload.Error.Exit != 2 {
						t.Fatalf("error=%v output=%s", err, out.String())
					}
				})
			}
		}
	}
}

func TestSelectionHelpFallback(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		for _, field := range []string{"bogus", "command"} {
			t.Run(mode+field, func(t *testing.T) {
				cmd, flags := newRootCmdWithFlags(func() runner.Runner {
					t.Fatal("help constructed runner")
					return nil
				})
				// A late-added non-runnable group exercises Cobra's fallback
				// directly, without Hollis's pre-run validation.
				group := &cobra.Command{Use: "fallback"}
				group.AddCommand(&cobra.Command{Use: "child"})
				cmd.AddCommand(group)
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(io.Discard)
				cmd.SetArgs([]string{"fallback", mode, "--select", field})
				err := executeCommand(cmd, flags)
				if field == "bogus" {
					if ExitCode(err) != 2 || !strings.Contains(out.String(), `"code":"usage"`) {
						t.Fatalf("fallback swallowed selection error: %v %s", err, out.String())
					}
				} else if err != nil || !strings.Contains(out.String(), `"command":"hollis fallback"`) {
					t.Fatalf("fallback used wrong result schema: %v %s", err, out.String())
				}
			})
		}
	}
}

func TestSelectionRejectsUnknownBeforeSideEffects(t *testing.T) {
	oldConfig, oldStore, oldList := configPath, openStore, listInstalledShortcuts
	configPath = func() (string, error) { t.Fatal("selection touched config"); return "", nil }
	openStore = func() (*store.Store, error) { t.Fatal("selection opened store"); return nil, nil }
	listInstalledShortcuts = func(context.Context) ([]string, error) { t.Fatal("selection discovered bridges"); return nil, nil }
	t.Cleanup(func() { configPath, openStore, listInstalledShortcuts = oldConfig, oldStore, oldList })
	output := filepath.Join(t.TempDir(), "never.png")
	for _, args := range [][]string{
		{"respond", "fixture"}, {"chat", "fixture"},
		{"chats", "list"}, {"chats", "search", "fixture"}, {"chats", "show", "fixture"},
		{"chats", "rename", "fixture", "title"}, {"chats", "delete", "fixture", "--yes"},
		{"config", "show"}, {"config", "set", "model", "local"},
		{"config", "set", "bridge", "cloud", "fixture"},
		{"config", "set", "image-bridge", "fixture"},
		{"image", "styles"}, {"image", "generate", "fixture", "--output", output},
		{"models"}, {"doctor"}, {"batch", "plan"}, {"batch", "run"}, {"batch", "resume"},
		{"version"}, {"--version"}, {"agent-context"},
		{"respond", "--help"}, {"help", "respond"},
	} {
		for _, mode := range []string{"--json", "--agent"} {
			t.Run(strings.Join(args, " ")+mode, func(t *testing.T) {
				gen := &fakeImageGenerator{}
				cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner {
					t.Fatal("selection constructed runner")
					return nil
				}, gen)
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(append(append([]string{}, args...), mode, "--select", "not_a_result_field"))
				err := executeCommand(cmd, flags)
				if ExitCode(err) != 2 || !strings.Contains(err.Error(), "not_a_result_field") {
					t.Fatalf("error=%v exit=%d", err, ExitCode(err))
				}
				var payload struct {
					Meta  any `json:"meta"`
					Error struct {
						Code string `json:"code"`
						Exit int    `json:"exit_code"`
					} `json:"error"`
				}
				if json.Unmarshal(out.Bytes(), &payload) != nil || payload.Error.Code != "usage" || payload.Error.Exit != 2 {
					t.Fatalf("missing unfiltered usage error: %s", out.String())
				}
				if mode == "--agent" && payload.Meta == nil {
					t.Fatal("agent error lost metadata")
				}
				if gen.calls != 0 {
					t.Fatalf("image dispatches=%d", gen.calls)
				}
			})
		}
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid selection published output: %v", err)
	}
}

func TestSelectionValidationUsesSchemaNotRows(t *testing.T) {
	schema := commandResultSchemas["hollis models"]
	for _, selection := range []string{"not_real", "capabilities.not_real", "model.child", ".model", "model.", "model..child", "model,", ",model", ","} {
		for _, agent := range []bool{false, true} {
			for _, rows := range [][]map[string]any{nil, {}, {{"model": "auto"}}} {
				var out bytes.Buffer
				err := printJSONArrayFilteredTo(&out, rows, &rootFlags{agent: agent, selectFields: selection, resultSchema: schema})
				if ExitCode(err) != 2 || out.Len() != 0 {
					t.Fatalf("selection=%q rows=%v err=%v out=%s", selection, rows, err, out.String())
				}
			}
		}
	}
	for _, selection := range []string{"model", "verified", "capabilities.streaming"} {
		if _, err := selectionPaths(selection, schema); err != nil {
			t.Fatalf("valid absent/optional field %s: %v", selection, err)
		}
	}
}

func TestSelectionArrayParityAndLaterFields(t *testing.T) {
	rows := []map[string]any{
		{"model": "auto", "apple_model": "strategy"},
		{"model": "cloud", "verified": true, "source": "fixture"},
		{"model": "local", "capabilities": map[string]bool{"streaming": true, "tools": false}},
	}
	want := `[{"model":"auto"},{"model":"cloud","verified":true},{"capabilities":{"streaming":true},"model":"local"}]`
	for _, agent := range []bool{false, true} {
		var out bytes.Buffer
		flags := &rootFlags{agent: agent, selectFields: " MODEL, verified, capabilities.streaming ", resultSchema: commandResultSchemas["hollis models"]}
		if err := printJSONArrayFilteredTo(&out, rows, flags); err != nil {
			t.Fatal(err)
		}
		got := out.Bytes()
		if agent {
			var envelope struct {
				Meta    map[string]any  `json:"meta"`
				Results json.RawMessage `json:"results"`
			}
			if json.Unmarshal(got, &envelope) != nil || envelope.Meta["schema_version"] != "2" {
				t.Fatalf("bad envelope: %s", got)
			}
			got = envelope.Results
		}
		if strings.TrimSpace(string(got)) != want {
			t.Fatalf("agent=%v got=%s want=%s", agent, got, want)
		}
	}
	if rows[1]["source"] != "fixture" {
		t.Fatal("projection mutated original rows")
	}
}

func TestSelectionEmptyAndOptionalResults(t *testing.T) {
	for _, agent := range []bool{false, true} {
		for _, tc := range []struct {
			rows []map[string]any
			want string
		}{{nil, "null"}, {[]map[string]any{}, "[]"}, {[]map[string]any{{"model": "auto"}}, "[{}]"}} {
			var out bytes.Buffer
			if err := printJSONArrayFilteredTo(&out, tc.rows, &rootFlags{agent: agent, selectFields: "verified", resultSchema: commandResultSchemas["hollis models"]}); err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSpace(out.String())
			if agent {
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				got = string(payload["results"])
			}
			if got != tc.want {
				t.Fatalf("got=%s want=%s", got, tc.want)
			}
		}
	}
}

func TestSelectionTypedNestedValuesAndPrecision(t *testing.T) {
	data := map[string]any{
		"usage":             &runner.Usage{InputTokens: 9007199254740993, OutputTokens: 3},
		"output_processing": imagegen.OutputOptions{Fit: "crop"},
		"response":          "not selected",
	}
	for _, agent := range []bool{false, true} {
		var out bytes.Buffer
		err := printJSONFilteredTo(&out, data, &rootFlags{
			agent: agent, selectFields: "usage.input_tokens,output_processing.fit",
			resultSchema: commandResultSchemas["hollis chat"],
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"input_tokens":9007199254740993`) || strings.Contains(out.String(), "output_tokens") || strings.Contains(out.String(), "not selected") {
			t.Fatalf("projection changed nested values: %s", out.String())
		}
	}
}

func TestSelectionNestedCollectionsAndRecursiveContext(t *testing.T) {
	for _, selection := range []string{"commands.name", "commands.flags.name", "commands.subcommands.subcommands.name", "contracts.exit_codes.usage", "commands.annotations.mcp:read-only"} {
		if _, err := selectionPaths(selection, commandResultSchemas["hollis agent-context"]); err != nil {
			t.Fatalf("%s: %v", selection, err)
		}
	}
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"agent-context", "--json", "--pretty", "--select", "cli.name,commands.flags.name"})
	if err := executeCommand(cmd, flags); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(out.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if len(value) != 2 || !strings.Contains(out.String(), "\n  ") || strings.Contains(out.String(), `"usage"`) {
		t.Fatalf("agent-context ignored selection/pretty: %s", out.String())
	}
}

func TestSelectionEmptyChatListCommand(t *testing.T) {
	stubConfigPath(t)
	db := filepath.Join(t.TempDir(), "chats.db")
	old := openStore
	openStore = func() (*store.Store, error) { return store.Open(db) }
	t.Cleanup(func() { openStore = old })
	for _, mode := range []string{"--json", "--agent"} {
		cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"chats", "list", mode, "--select", "id"})
		if err := executeCommand(cmd, flags); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "[]") {
			t.Fatalf("empty list lost: %s", out.String())
		}
	}
}

func TestSelectionDoctorDiagnosticsPreserved(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		t.Run(mode, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, []string{"AFM Bridge - Cloud.signed"}, true, 27)
			cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"doctor", mode, "--select", "bridges.model"})
			err := executeCommand(cmd, flags)
			if ExitCode(err) != 3 || !ErrorReported(err) {
				t.Fatalf("error=%v output=%s", err, out.String())
			}
			var payload map[string]any
			if json.Unmarshal(out.Bytes(), &payload) != nil || payload["error"] == nil {
				t.Fatalf("lost diagnostics: %s", out.String())
			}
			result := payload
			if mode == "--agent" {
				if payload["meta"] == nil {
					t.Fatal("lost metadata")
				}
				result = payload["results"].(map[string]any)
			}
			for _, row := range result["bridges"].([]any) {
				if len(row.(map[string]any)) != 1 {
					t.Fatalf("nested bridge selection ignored: %v", row)
				}
			}
		})
	}
}

func TestSelectionNoSelectionKeepsEncoding(t *testing.T) {
	data := map[string]any{"usage": &runner.Usage{InputTokens: 5}, "output_processing": imagegen.OutputOptions{}}
	for _, fields := range []string{"", "   "} {
		result, err := projectOutput(data, &rootFlags{selectFields: fields})
		if err != nil || !reflect.DeepEqual(result, data) {
			t.Fatalf("no-selection changed data: %v %v", result, err)
		}
	}
}

func TestTimeoutContractFactualAttribution(t *testing.T) {
	for _, mode := range []string{"human", "--json", "--agent"} {
		t.Run(mode, func(t *testing.T) {
			stubConfigPath(t)
			fake := &localFixtureRunner{err: &runner.Error{Kind: runner.KindTimeout, ExitCode: -1, Err: errors.New("model execution deadline exceeded")}}
			cmd, flags := newRootCmdWithFlags(func() runner.Runner { return fake })
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			args := []string{"respond", "--model", "local", "fixture"}
			if mode != "human" {
				args = append(args, mode, "--select", "response")
			}
			cmd.SetArgs(args)
			err := executeCommand(cmd, flags)
			if ExitCode(err) != 7 || errorCode(err) != "timeout" || strings.Contains(err.Error(), "Apple-side") || !strings.Contains(err.Error(), "does not identify the cause") {
				t.Fatalf("error=%v", err)
			}
			if fake.calls != 1 || fake.streams != 0 {
				t.Fatalf("retry/stream: %d/%d", fake.calls, fake.streams)
			}
			if mode != "human" && (!strings.Contains(out.String(), `"code":"timeout"`) || !strings.Contains(out.String(), `"exit_code":7`)) {
				t.Fatalf("selected error lost: %s", out.String())
			}
		})
	}
}
