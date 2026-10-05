// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func setupSelectFixture(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("HOLLIS_STATE_DIR", state)
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	return state
}

func executeSelectFixture(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := executeCommand(cmd, flags)
	return out.Bytes(), err
}

func selectFixtureResult(t *testing.T, raw []byte, agent bool) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var data any
	if err := decoder.Decode(&data); err != nil {
		t.Fatalf("decode: %v; output=%s", err, raw)
	}
	if agent {
		envelope, ok := data.(map[string]any)
		if !ok || envelope["meta"] == nil || len(envelope) != 2 {
			t.Fatalf("agent envelope=%#v", data)
		}
		return envelope["results"]
	}
	return data
}

func seedSelectChat(t *testing.T, state string) string {
	t.Helper()
	st, err := store.Open(filepath.Join(state, "hollis.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "selection fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "selection fixture search marker"); err != nil {
		t.Fatal(err)
	}
	return conv.ID
}

func TestSelectArrayCommands(t *testing.T) {
	commands := []struct {
		name  string
		args  []string
		field string
		count int
	}{
		{"chats list", []string{"chats", "list"}, "id", 1},
		{"chats search", []string{"chats", "search", "selection fixture"}, "id", 1},
		{"models", []string{"models"}, "model", 6},
		{"models local", []string{"models", "--model", "local"}, "model", 1},
		{"image styles", []string{"image", "styles"}, "style", 6},
	}
	for _, command := range commands {
		for _, mode := range []string{"--json", "--agent"} {
			for _, typo := range []bool{false, true} {
				name := command.name + "/" + mode
				if typo {
					name += "/typo"
				}
				t.Run(name, func(t *testing.T) {
					state := setupSelectFixture(t)
					id := seedSelectChat(t, state)
					field := command.field
					if typo {
						field += "_typo"
					}
					args := append(append([]string(nil), command.args...), mode, "--select", field)
					raw, err := executeSelectFixture(t, args...)
					if typo {
						if ExitCode(err) != 2 || !ErrorReported(err) {
							t.Fatalf("exit=%d reported=%v err=%v output=%s", ExitCode(err), ErrorReported(err), err, raw)
						}
						var body map[string]any
						if json.Unmarshal(raw, &body) != nil || body["error"].(map[string]any)["code"] != "usage" {
							t.Fatalf("usage envelope=%s", raw)
						}
						return
					}
					if err != nil {
						t.Fatalf("err=%v output=%s", err, raw)
					}
					rows, ok := selectFixtureResult(t, raw, mode == "--agent").([]any)
					if !ok || len(rows) != command.count {
						t.Fatalf("rows=%#v output=%s", rows, raw)
					}
					for _, entry := range rows {
						row := entry.(map[string]any)
						if len(row) != 1 || row[field] == nil {
							t.Fatalf("selected %s: row=%#v", field, row)
						}
						if field == "id" && row[field] != id {
							t.Fatalf("id=%v want=%s", row[field], id)
						}
					}
				})
			}
		}
	}
}

func TestSelectArrayUnionAndNestedMaps(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		t.Run(mode, func(t *testing.T) {
			setupSelectFixture(t)
			items := []map[string]any{
				{"id": "first", "metadata": map[string]any{"other": true}},
				{"id": "second", "metadata": map[string]any{"foo": "value", "drop": true}},
			}
			original, _ := json.Marshal(items)
			var out bytes.Buffer
			if err := printJSONFilteredTo(&out, items, &rootFlags{asJSON: true, agent: mode == "--agent", selectFields: "metadata.foo"}); err != nil {
				t.Fatal(err)
			}
			want := []any{map[string]any{}, map[string]any{"metadata": map[string]any{"foo": "value"}}}
			if got := selectFixtureResult(t, out.Bytes(), mode == "--agent"); !reflect.DeepEqual(got, want) {
				t.Fatalf("got=%#v want=%#v", got, want)
			}
			after, _ := json.Marshal(items)
			if !bytes.Equal(original, after) {
				t.Fatalf("filter mutated input: before=%s after=%s", original, after)
			}
		})
	}
}

func TestSelectModelsHeterogeneousKeys(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		t.Run(mode, func(t *testing.T) {
			setupSelectFixture(t)
			raw, err := executeSelectFixture(t, "models", mode, "--select", "wfllm_model,backend")
			if err != nil {
				t.Fatalf("err=%v output=%s", err, raw)
			}
			rows := selectFixtureResult(t, raw, mode == "--agent").([]any)
			if len(rows) != 6 || len(rows[0].(map[string]any)) != 0 || !reflect.DeepEqual(rows[5], map[string]any{"backend": "native"}) {
				t.Fatalf("rows=%#v", rows)
			}
			for _, entry := range rows[1:5] {
				row := entry.(map[string]any)
				if len(row) != 1 || row["wfllm_model"] == nil {
					t.Fatalf("row=%#v", row)
				}
			}
		})
	}
}

func TestSelectEmptyArrays(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		for _, field := range []string{"id", "unknown"} {
			t.Run(mode+"/"+field, func(t *testing.T) {
				setupSelectFixture(t)
				raw, err := executeSelectFixture(t, "chats", "list", mode, "--select", field)
				if err != nil {
					t.Fatalf("err=%v output=%s", err, raw)
				}
				if got := selectFixtureResult(t, raw, mode == "--agent"); !reflect.DeepEqual(got, []any{}) {
					t.Fatalf("empty array=%#v", got)
				}
			})
		}
	}
}

func TestSelectNilArray(t *testing.T) {
	for _, agent := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "agent"}[agent], func(t *testing.T) {
			setupSelectFixture(t)
			var out bytes.Buffer
			if err := printJSONFilteredTo(&out, []map[string]any(nil), &rootFlags{asJSON: true, agent: agent, selectFields: "id"}); err != nil {
				t.Fatal(err)
			}
			if got := selectFixtureResult(t, out.Bytes(), agent); !reflect.DeepEqual(got, []any{}) {
				t.Fatalf("nil array=%#v", got)
			}
		})
	}
}

func TestSelectObjectNestedPaths(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		t.Run(mode, func(t *testing.T) {
			setupSelectFixture(t)
			var out bytes.Buffer
			data := map[string]any{"metadata": map[string]any{"foo": "value", "drop": true}, "id": "drop"}
			if err := printJSONFilteredTo(&out, data, &rootFlags{asJSON: true, agent: mode == "--agent", selectFields: " METADATA.FOO "}); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"metadata": map[string]any{"foo": "value"}}
			if got := selectFixtureResult(t, out.Bytes(), mode == "--agent"); !reflect.DeepEqual(got, want) {
				t.Fatalf("got=%#v want=%#v", got, want)
			}
		})
	}
}

func TestSelectUnknownAndMalformedPaths(t *testing.T) {
	for _, field := range []string{"missing", "metadata.missing", "metadata.foo,missing", "metadata.foo.child", "metadata.", ",,,"} {
		t.Run(field, func(t *testing.T) {
			setupSelectFixture(t)
			var out bytes.Buffer
			err := printJSONFilteredTo(&out, map[string]any{"metadata": map[string]any{"foo": "value"}}, &rootFlags{asJSON: true, selectFields: field})
			if ExitCode(err) != 2 || out.Len() != 0 {
				t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, out.Bytes())
			}
		})
	}
}

func TestSelectDoctorSerializedStructPaths(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		for _, field := range []string{"bridges.status", "bridges", "bridges.statuss"} {
			t.Run(mode+"/"+field, func(t *testing.T) {
				setupSelectFixture(t)
				raw, err := executeSelectFixture(t, "doctor", mode, "--select", field)
				if field == "bridges.statuss" {
					if ExitCode(err) != 2 {
						t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, raw)
					}
					return
				}
				if err != nil {
					t.Fatalf("err=%v output=%s", err, raw)
				}
				obj := selectFixtureResult(t, raw, mode == "--agent").(map[string]any)
				rows, ok := obj["bridges"].([]any)
				if len(obj) != 1 || !ok || len(rows) != 4 {
					t.Fatalf("results=%#v", obj)
				}
				for _, entry := range rows {
					row := entry.(map[string]any)
					if row["status"] != "ok" || (field == "bridges.status" && len(row) != 1) || (field == "bridges" && len(row) != 8) {
						t.Fatalf("row=%#v", row)
					}
				}
			})
		}
	}
}

func TestSelectDoctorDiagnosticError(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		t.Run(mode, func(t *testing.T) {
			setupSelectFixture(t)
			stubResolution(t, []string{"AFM Bridge - Cloud.signed"}, true, 27)
			raw, err := executeSelectFixture(t, "doctor", mode, "--select", "bridges.status")
			if ExitCode(err) != 3 || !ErrorReported(err) {
				t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, raw)
			}
			var body map[string]json.RawMessage
			if json.Unmarshal(raw, &body) != nil || body["error"] == nil {
				t.Fatalf("diagnostic envelope=%s", raw)
			}
			results := raw
			if mode == "--agent" {
				results = body["results"]
			}
			var obj map[string]json.RawMessage
			if json.Unmarshal(results, &obj) != nil || obj["bridges"] == nil {
				t.Fatalf("diagnostic results=%s", results)
			}
			var rows []map[string]any
			if json.Unmarshal(obj["bridges"], &rows) != nil || len(rows) != 4 {
				t.Fatalf("bridges=%s", obj["bridges"])
			}
			for _, row := range rows {
				if len(row) != 1 || row["status"] == nil {
					t.Fatalf("row=%#v", row)
				}
			}
		})
	}
}

func TestSelectBatchPlanTypedItems(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		for _, field := range []string{"items.name", "items.missing"} {
			t.Run(mode+"/"+field, func(t *testing.T) {
				dir := setupSelectFixture(t)
				input := filepath.Join(dir, "input")
				if err := os.Mkdir(input, 0o700); err != nil {
					t.Fatal(err)
				}
				prompt := filepath.Join(dir, "prompt.txt")
				for path, text := range map[string]string{prompt: "Summarize", filepath.Join(input, "note.txt"): "synthetic source"} {
					if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				raw, err := executeSelectFixture(t, "batch", "plan", "--input-dir", input, "--prompt-file", prompt, "--model", "cloud", "--output-dir", filepath.Join(dir, "results"), "--job", filepath.Join(dir, "job.json"), mode, "--select", field)
				if field == "items.missing" {
					if ExitCode(err) != 2 {
						t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, raw)
					}
					return
				}
				if err != nil {
					t.Fatalf("err=%v output=%s", err, raw)
				}
				want := map[string]any{"items": []any{map[string]any{"name": "note.txt"}}}
				if got := selectFixtureResult(t, raw, mode == "--agent"); !reflect.DeepEqual(got, want) {
					t.Fatalf("got=%#v want=%#v", got, want)
				}
			})
		}
	}
}

func TestSelectPreservesExactNumericValues(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		t.Run(mode, func(t *testing.T) {
			setupSelectFixture(t)
			data := map[string]any{"count": int64(9007199254740993), "drop": true}
			var reference, selected bytes.Buffer
			flags := &rootFlags{asJSON: true, agent: mode == "--agent"}
			if err := printJSONFilteredTo(&reference, data, flags); err != nil {
				t.Fatal(err)
			}
			flags.selectFields = "count"
			if err := printJSONFilteredTo(&selected, data, flags); err != nil {
				t.Fatal(err)
			}
			before := selectFixtureResult(t, reference.Bytes(), flags.agent).(map[string]any)["count"]
			after := selectFixtureResult(t, selected.Bytes(), flags.agent).(map[string]any)["count"]
			if before != json.Number("9007199254740993") || after != before || !strings.Contains(selected.String(), `"count":9007199254740993`) {
				t.Fatalf("before=%#v after=%#v output=%s", before, after, selected.Bytes())
			}
			t.Logf("exact before/after JSON numeric value=%v; output=%s", after, selected.Bytes())
		})
	}
}

func TestSelectHelpRejectsUnknownFields(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		for _, topic := range [][]string{{"respond", "--help"}, {"help", "respond"}} {
			t.Run(mode+"/"+strings.Join(topic, " "), func(t *testing.T) {
				setupSelectFixture(t)
				args := append([]string{mode, "--select", "missing"}, topic...)
				raw, err := executeSelectFixture(t, args...)
				if ExitCode(err) != 2 {
					t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, raw)
				}
			})
		}
	}
}
