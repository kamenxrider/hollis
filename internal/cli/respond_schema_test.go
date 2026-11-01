// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// schemaFixtureRunner records guided-output calls and fails loudly if the
// command reaches the transport when it should have refused earlier.
type schemaFixtureRunner struct {
	calls      int
	consumed   runner.Model
	prompt     string
	schemaText string
	text       string
	structured json.RawMessage
	err        error
}

func (r *schemaFixtureRunner) Run(ctx context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	completion, err := r.RunComplete(ctx, m, prompt)
	return completion.Text, completion.Model, err
}

func (r *schemaFixtureRunner) RunComplete(_ context.Context, m runner.Model, prompt string) (runner.Completion, error) {
	r.calls++
	r.consumed, r.prompt = m, prompt
	return runner.Completion{Text: r.text, Model: m, Usage: &runner.Usage{InputTokens: 9, OutputTokens: 4}}, r.err
}

func (r *schemaFixtureRunner) RunSchema(_ context.Context, m runner.Model, prompt string, schema *runner.Schema) (runner.Completion, error) {
	r.calls++
	r.consumed, r.prompt, r.schemaText = m, prompt, string(schema.Bytes())
	return runner.Completion{Text: r.text, Model: m, Structured: r.structured,
		Usage: &runner.Usage{InputTokens: 9, OutputTokens: 4, ReasoningTokens: 1}}, r.err
}

// completeOnlyRunner implements Runner without SchemaRunner, which is what an
// unrelated provider-free runner looks like.
type completeOnlyRunner struct{ calls int }

func (r *completeOnlyRunner) Run(_ context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	return prompt, m, nil
}

func writeSchemaFixture(t *testing.T, schema string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const cliGuidedSchema = `{"type":"object","additionalProperties":false,"required":["name"],` +
	`"properties":{"name":{"type":"string"},"age":{"type":"integer","minimum":0}}}`

func TestRespondSchemaRejectionsHappenBeforeTheRunner(t *testing.T) {
	valid := writeSchemaFixture(t, cliGuidedSchema)
	badPointer := writeSchemaFixture(t, `{"type":"object","additionalProperties":false,`+
		`"properties":{"p":{"type":"string","pattern":"x"}},"required":["p"]}`)
	intEnum := writeSchemaFixture(t, `{"type":"object","additionalProperties":false,`+
		`"properties":{"p":{"type":"integer","enum":[1,2]}}}`)
	notJSON := writeSchemaFixture(t, `not a schema`)
	deep := writeSchemaFixture(t, `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"object",`+
		`"additionalProperties":false,"properties":{"b":{"type":"object","additionalProperties":false,"properties":{`+
		`"c":{"type":"object","additionalProperties":false,"properties":{"d":{"type":"string"}}}}}}}}}`)
	image := writeCLIImage(t, "fixture.png")

	cases := []struct {
		name     string
		args     []string
		wantIn   string
		wantExit int
	}{
		{"pattern keyword", []string{"respond", "--model", "local", "--schema", badPointer, "hi"}, "/properties/p/pattern", 2},
		{"integer enum", []string{"respond", "--model", "local", "--schema", intEnum, "hi"}, "/properties/p/enum", 2},
		{"depth four", []string{"respond", "--model", "local", "--schema", deep, "hi"}, "object nesting exceeds depth 3", 2},
		{"not json", []string{"respond", "--model", "local", "--schema", notJSON, "hi"}, "schema is not valid JSON", 2},
		{"missing file", []string{"respond", "--model", "local", "--schema", filepath.Join(t.TempDir(), "absent.json"), "hi"}, "read schema file", 2},
		{"cloud route", []string{"respond", "--model", "cloud", "--schema", valid, "hi"}, "requires --model local", 2},
		{"auto route", []string{"respond", "--schema", valid, "hi"}, "requires --model local", 2},
		{"stream", []string{"respond", "--model", "local", "--schema", valid, "--stream", "hi"}, "complete-only", 2},
		{"image", []string{"respond", "--model", "local", "--schema", valid, "--image", image, "hi"}, "cannot be combined with --image", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			r := &schemaFixtureRunner{text: "must not run"}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs(tc.args)
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			err := cmd.Execute()
			if err == nil || ExitCode(err) != tc.wantExit {
				t.Fatalf("err=%v exit=%d, want exit %d", err, ExitCode(err), tc.wantExit)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error = %q; want %q", err.Error(), tc.wantIn)
			}
			if r.calls != 0 {
				t.Fatalf("runner calls = %d; a rejection must not reach the transport", r.calls)
			}
			if out.Len() != 0 {
				t.Fatalf("stdout = %q; want nothing on a rejected request", out.String())
			}
		})
	}
}

func TestRespondSchemaJSONReturnsTheDecodedObject(t *testing.T) {
	stubConfigPath(t)
	schema := writeSchemaFixture(t, cliGuidedSchema)
	structured := json.RawMessage(`{"name":"Ada","age":36}`)
	r := &schemaFixtureRunner{text: "{\"name\":\"Ada\",\"age\":36}", structured: structured}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--model", "local", "--schema", schema, "--json", "produce"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, out.String())
	}
	if r.calls != 1 || r.consumed != runner.ModelLocal {
		t.Fatalf("runner calls=%d model=%q", r.calls, r.consumed)
	}
	response, ok := got["response"].(map[string]any)
	if !ok {
		t.Fatalf("response = %#v; want the decoded object", got["response"])
	}
	if response["name"] != "Ada" || response["age"] != float64(36) {
		t.Fatalf("response = %#v", response)
	}
	if got["model_requested"] != "local" || got["model_used"] != "local" {
		t.Fatalf("model fields = %#v", got)
	}
	usage, ok := got["usage"].(map[string]any)
	if !ok || usage["input_tokens"] != float64(9) {
		t.Fatalf("usage = %#v", got["usage"])
	}
}

func TestRespondSchemaHumanPrintsIndentedObject(t *testing.T) {
	stubConfigPath(t)
	schema := writeSchemaFixture(t, cliGuidedSchema)
	r := &schemaFixtureRunner{structured: json.RawMessage(`{"name":"Ada"}`)}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--model", "local", "--schema", schema, "produce"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// The object is the answer: no duplicated raw text field, and valid JSON
	// remains parseable through the indentation.
	var parsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("human output is not parseable JSON: %v (%q)", err, out.String())
	}
	if parsed["name"] != "Ada" || strings.Count(out.String(), "Ada") != 1 {
		t.Fatalf("output = %q; want the object printed exactly once", out.String())
	}
}

func TestRespondSchemaRequiresARunnerThatSupportsIt(t *testing.T) {
	stubConfigPath(t)
	schema := writeSchemaFixture(t, cliGuidedSchema)
	r := &completeOnlyRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--model", "local", "--schema", schema, "produce"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 5 {
		t.Fatalf("err=%v exit=%d, want transport 5", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "does not support guided output") {
		t.Fatalf("error = %v", err)
	}
}

func TestRespondSchemaFailuresKeepTheirKinds(t *testing.T) {
	stubConfigPath(t)
	schema := writeSchemaFixture(t, cliGuidedSchema)
	cases := []struct {
		name     string
		err      error
		wantExit int
		wantCode string
	}{
		{"declined", &runner.Error{Kind: runner.KindRequestDeclined, ExitCode: -1}, 5, "request_declined"},
		{"protocol", &runner.Error{Kind: runner.KindNativeProtocol, ExitCode: -1}, 5, "native_protocol"},
		{"native failed", &runner.Error{Kind: runner.KindNativeFailed, ExitCode: -1}, 5, "native_failed"},
		{"context capacity", &runner.Error{Kind: runner.KindContextCapacity, ExitCode: -1}, 5, "context_capacity"},
		{"timeout", &runner.Error{Kind: runner.KindTimeout, ExitCode: -1}, 7, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &schemaFixtureRunner{err: tc.err}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs([]string{"respond", "--model", "local", "--schema", schema, "produce"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if err == nil || ExitCode(err) != tc.wantExit {
				t.Fatalf("err=%v exit=%d, want %d", err, ExitCode(err), tc.wantExit)
			}
			if tc.wantCode != "" {
				if code := errorCode(err); code != tc.wantCode {
					t.Fatalf("machine code = %q; want %q", code, tc.wantCode)
				}
			}
			if out.Len() != 0 {
				t.Fatalf("stdout = %q; a failure must not print a partial object", out.String())
			}
		})
	}
}

// statusFixtureRunner answers the non-generative status probe with a chosen
// context window so doctor/models rows can be checked offline.
type statusFixtureRunner struct {
	status runner.LocalStatus
	err    error
}

func (r *statusFixtureRunner) Run(_ context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	return prompt, m, nil
}

func (r *statusFixtureRunner) NativeStatus(context.Context) (runner.LocalStatus, error) {
	return r.status, r.err
}

func TestContextSizeSurfacesOnlyWhereMeasured(t *testing.T) {
	measured := runner.LocalStatus{Available: true, Protocol: 2, Version: "0.5.0", ContextSize: 4096}
	for _, tc := range []struct {
		name   string
		status runner.LocalStatus
		want   bool
	}{
		{"measured", measured, true},
		{"absent", runner.LocalStatus{Available: true, Protocol: 2, Version: "0.5.0"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			resolved := map[runner.Model]runner.ResolvedBridge{}
			_ = resolved
			// models --model local is the narrowest surface: it needs no
			// Shortcuts discovery, so this stays offline and deterministic.
			cmd := NewRootCmd(func() runner.Runner { return &statusFixtureRunner{status: tc.status} })
			cmd.SetArgs([]string{"models", "--model", "local", "--json"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
				t.Fatalf("JSON: %v (%q)", err, out.String())
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d; want one local row", len(rows))
			}
			value, present := rows[0]["context_size"]
			if present != tc.want {
				t.Fatalf("context_size present = %v; want %v (%#v)", present, tc.want, rows[0])
			}
			if tc.want && value != float64(4096) {
				t.Fatalf("context_size = %#v; want 4096", value)
			}
			capabilities, ok := rows[0]["capabilities"].(map[string]any)
			if !ok || capabilities["guided_output"] != true {
				t.Fatalf("capabilities = %#v; want guided_output true", rows[0]["capabilities"])
			}

			human := NewRootCmd(func() runner.Runner { return &statusFixtureRunner{status: tc.status} })
			human.SetArgs([]string{"models", "--model", "local"})
			var humanOut bytes.Buffer
			human.SetOut(&humanOut)
			human.SetErr(io.Discard)
			if err := human.Execute(); err != nil {
				t.Fatal(err)
			}
			line := humanOut.String()
			if tc.want {
				if !strings.Contains(line, "context window 4096 tokens") {
					t.Fatalf("human line = %q; want the measured context window", line)
				}
			} else if strings.Contains(line, "context window") {
				t.Fatalf("human line = %q; an absent signal must be omitted, not zero-filled", line)
			}
		})
	}
}

func TestDoctorReportsMeasuredContextSize(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	status := runner.LocalStatus{Available: true, Protocol: 2, Version: "0.5.0", ContextSize: 4096}

	jsonCmd := NewRootCmd(func() runner.Runner { return &statusFixtureRunner{status: status} })
	jsonCmd.SetArgs([]string{"doctor", "--json"})
	var jsonOut bytes.Buffer
	jsonCmd.SetOut(&jsonOut)
	jsonCmd.SetErr(io.Discard)
	if err := jsonCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &report); err != nil {
		t.Fatalf("JSON: %v (%q)", err, jsonOut.String())
	}
	local, ok := report["local"].(map[string]any)
	if !ok {
		t.Fatalf("local row = %#v", report["local"])
	}
	if local["context_size"] != float64(4096) || local["helper_protocol"] != float64(2) {
		t.Fatalf("local row = %#v; want the measured context window and protocol", local)
	}

	humanCmd := NewRootCmd(func() runner.Runner { return &statusFixtureRunner{status: status} })
	humanCmd.SetArgs([]string{"doctor"})
	var humanOut bytes.Buffer
	humanCmd.SetOut(&humanOut)
	humanCmd.SetErr(io.Discard)
	if err := humanCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(humanOut.String(), "context window 4096 tokens") {
		t.Fatalf("doctor human output = %q; want the measured context window", humanOut.String())
	}
}

func TestSchemaCapabilityComesFromTheRouteTable(t *testing.T) {
	spec, ok := runner.Spec(runner.ModelLocal)
	if !ok {
		t.Fatal("local route missing from the table")
	}
	if !spec.Capabilities.Schema {
		t.Fatal("local route does not declare guided output")
	}
	for _, model := range runner.Models() {
		other, ok := runner.Spec(model)
		if !ok || other.Capabilities.Schema {
			t.Fatalf("%s declares guided output; only the native route implements it", model)
		}
	}
}
