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
	"strings"
	"syscall"
	"testing"
	"time"

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

// deepArraySchema builds the array chain a reviewer used to show that neither
// parser bounded array nesting.
func deepArraySchema(levels int) string {
	schema := `{"type":"string"}`
	for index := 0; index < levels; index++ {
		schema = `{"type":"array","items":` + schema + `}`
	}
	return `{"type":"object","additionalProperties":false,"properties":{"p":` + schema + `}}`
}

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
	// The reviewer's shape: hundreds of nested arrays. Neither parser bounded
	// them, so the CLI accepted the schema and the helper's decoder failed
	// after the request had spawned.
	deepArrays := writeSchemaFixture(t, deepArraySchema(600))
	unicodeName := writeSchemaFixture(t, `{"type":"object","additionalProperties":false,`+
		`"properties":{"caf\u00e9":{"type":"string"}}}`)
	// Foundation's decoder accepts a repeated key, a BOM and a trailing comma;
	// Go does not. All three are usage errors before any transport call.
	duplicateKey := writeSchemaFixture(t, `{"type":"object","additionalProperties":false,"type":"array"}`)
	bom := writeSchemaFixture(t, "\ufeff"+`{"type":"object","additionalProperties":false}`)
	trailingComma := writeSchemaFixture(t, `{"type":"object","additionalProperties":false,}`)
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
		{"600 nested arrays", []string{"respond", "--model", "local", "--schema", deepArrays, "hi"}, "schema nesting exceeds depth 16", 2},
		{"non-ascii property name", []string{"respond", "--model", "local", "--schema", unicodeName, "hi"}, "property names must be printable ASCII", 2},
		{"not json", []string{"respond", "--model", "local", "--schema", notJSON, "hi"}, "schema is not valid JSON", 2},
		{"duplicate key", []string{"respond", "--model", "local", "--schema", duplicateKey, "hi"}, "/type: schema must not repeat an object key", 2},
		{"byte order mark", []string{"respond", "--model", "local", "--schema", bom, "hi"}, "schema is not valid JSON", 2},
		{"trailing comma", []string{"respond", "--model", "local", "--schema", trailingComma, "hi"}, "schema is not valid JSON", 2},
		{"missing file", []string{"respond", "--model", "local", "--schema", filepath.Join(t.TempDir(), "absent.json"), "hi"}, "schema file \"absent.json\"", 2},
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

// TestOldHelperGuidedRefusalIsAProtocolFailure pins the exit-code contract for
// the pre-spawn refusal. An older paired helper cannot serve an operation this
// route supports, which is the same class of failure as a higher protocol or a
// protocol-1 event on plain completion: exit 5 with machine code
// native_protocol. Reporting it as a usage error would tell a script that its
// invocation was wrong when only the installed component is stale.
//
// The error comes from the transport rather than a hand-written literal, so
// this test also fails if the refusal's kind or message changes there.
func TestOldHelperGuidedRefusalIsAProtocolFailure(t *testing.T) {
	stubConfigPath(t)
	schema := writeSchemaFixture(t, cliGuidedSchema)
	r := &schemaFixtureRunner{err: runner.GuidedOutputRefusal()}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--model", "local", "--schema", schema, "produce"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 5 {
		t.Fatalf("err=%v exit=%d; want exit 5", err, ExitCode(err))
	}
	if code := errorCode(err); code != "native_protocol" {
		t.Fatalf("machine code = %q; want native_protocol", code)
	}
	if !strings.Contains(err.Error(), "does not support guided output") {
		t.Fatalf("error = %q; want the refusal message", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q; a refusal must not print a partial object", out.String())
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

func TestRejectedStatusNeverReportsAvailable(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	// A status read that failed may still hand back the partial value it had
	// built. Both diagnostics must report the failure rather than the leftover
	// availability, and the measured detail belongs to a successful read only.
	status := runner.LocalStatus{Available: true, Protocol: 2, Version: "0.5.0", ContextSize: 4096}
	statusErr := &runner.Error{Kind: runner.KindNativeProtocol, ExitCode: -1, Err: errors.New("invalid native context size")}

	models := NewRootCmd(func() runner.Runner { return &statusFixtureRunner{status: status, err: statusErr} })
	models.SetArgs([]string{"models", "--model", "local", "--json"})
	var modelsOut bytes.Buffer
	models.SetOut(&modelsOut)
	models.SetErr(io.Discard)
	if err := models.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(modelsOut.Bytes(), &rows); err != nil {
		t.Fatalf("JSON: %v (%q)", err, modelsOut.String())
	}
	if len(rows) != 1 || rows[0]["available"] != false {
		t.Fatalf("models row = %#v; a failed status must not report available", rows)
	}
	if _, present := rows[0]["context_size"]; present {
		t.Fatalf("models row = %#v; a failed status has no measured context window", rows[0])
	}
	if got, _ := rows[0]["status"].(string); !strings.HasPrefix(got, "unavailable: ") {
		t.Fatalf("models status = %q; want the failure", got)
	}

	humanModels := NewRootCmd(func() runner.Runner { return &statusFixtureRunner{status: status, err: statusErr} })
	humanModels.SetArgs([]string{"models", "--model", "local"})
	var humanOut bytes.Buffer
	humanModels.SetOut(&humanOut)
	humanModels.SetErr(io.Discard)
	if err := humanModels.Execute(); err != nil {
		t.Fatal(err)
	}
	if line := humanOut.String(); strings.Contains(line, "available\n") || strings.Contains(line, "context window") {
		t.Fatalf("human models line = %q; want the failure with no measured detail", line)
	}

	doctor := NewRootCmd(func() runner.Runner { return &statusFixtureRunner{status: status, err: statusErr} })
	doctor.SetArgs([]string{"doctor", "--json"})
	var doctorOut bytes.Buffer
	doctor.SetOut(&doctorOut)
	doctor.SetErr(io.Discard)
	if err := doctor.Execute(); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(doctorOut.Bytes(), &report); err != nil {
		t.Fatalf("JSON: %v (%q)", err, doctorOut.String())
	}
	local, ok := report["local"].(map[string]any)
	if !ok || local["available"] != false {
		t.Fatalf("doctor local row = %#v; want available false", report["local"])
	}
	if _, present := local["context_size"]; present {
		t.Fatalf("doctor local row = %#v; want no context size", local)
	}
}

// TestUnavailableStatusReportsNoContextWindow pins the two diagnostic surfaces
// to the same rule. A helper can report an unavailable model and a window in
// the same status; the JSON row omits the window there, so the human sentence
// must not append one either — otherwise `models` and `doctor` would describe
// the same status two different ways, and the human surface would claim a
// measurement for a model it just called unavailable.
func TestUnavailableStatusReportsNoContextWindow(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	// A successful read, an unavailable model, and a window: the shape a
	// helper produces for an ineligible device that still knows its capacity.
	status := runner.LocalStatus{Available: false, Reason: "device_not_eligible",
		Protocol: 2, Version: "0.5.0", ContextSize: 8192}
	runnerFixture := func() runner.Runner { return &statusFixtureRunner{status: status} }

	modelsJSON := NewRootCmd(runnerFixture)
	modelsJSON.SetArgs([]string{"models", "--model", "local", "--json"})
	var modelsOut bytes.Buffer
	modelsJSON.SetOut(&modelsOut)
	modelsJSON.SetErr(io.Discard)
	if err := modelsJSON.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(modelsOut.Bytes(), &rows); err != nil {
		t.Fatalf("JSON: %v (%q)", err, modelsOut.String())
	}
	if len(rows) != 1 || rows[0]["available"] != false {
		t.Fatalf("models row = %#v; want available false", rows)
	}
	if _, present := rows[0]["context_size"]; present {
		t.Fatalf("models row = %#v; an unavailable model reports no window", rows[0])
	}

	cases := []struct {
		name string
		args []string
	}{
		{"models", []string{"models", "--model", "local"}},
		{"doctor", []string{"doctor"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd(runnerFixture)
			cmd.SetArgs(tc.args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			line := out.String()
			if !strings.Contains(line, "unavailable") {
				t.Fatalf("output = %q; want the unavailable label", line)
			}
			if strings.Contains(line, "context window") {
				t.Fatalf("output = %q; an unavailable model must not report a window", line)
			}
		})
	}
}

// TestSchemaFileReadErrorsNeverCarryTheLocalPath pins the safe-input contract
// on the new --schema surface: a rejected path is named by basename only, and a
// FIFO cannot stall the command waiting for a writer.
func TestSchemaFileReadErrorsNeverCarryTheLocalPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent-schema.json")
	_, err := readSchemaFile(missing)
	if err == nil {
		t.Fatal("readSchemaFile accepted a missing file")
	}
	if ExitCode(err) != 2 {
		t.Fatalf("exit code = %d; want usage 2", ExitCode(err))
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("error = %q; leaked the local directory", err.Error())
	}
	if !strings.Contains(err.Error(), "absent-schema.json") {
		t.Fatalf("error = %q; want the basename for the caller to recognise", err.Error())
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v; want os.ErrNotExist to survive classification", err)
	}

	fifo := filepath.Join(dir, "schema.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readSchemaFile(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "must be a regular file") {
			t.Fatalf("readSchemaFile(FIFO) = %v; want the regular-file rejection", err)
		}
		if strings.Contains(err.Error(), dir) {
			t.Fatalf("error = %q; leaked the local directory", err.Error())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("readSchemaFile blocked on a FIFO")
	}
}

func TestGuidedHumanOutputStripsTerminalControls(t *testing.T) {
	stubConfigPath(t)
	old := terminalOutput
	terminalOutput = func(io.Writer) bool { return true }
	t.Cleanup(func() { terminalOutput = old })
	schema := writeSchemaFixture(t, cliGuidedSchema)
	// C1 controls survive JSON encoding as raw UTF-8, and 0x9b is an 8-bit CSI
	// introducer on some terminals. ESC and BEL arrive as \u escapes and are
	// already inert, but the renderer has to leave them inert in both forms.
	structured := json.RawMessage("{\"name\":\"a\u0085b\u009bc\\u001bd\\u0007e\"}")
	r := &schemaFixtureRunner{structured: structured}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--model", "local", "--schema", schema, "produce"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	printed := out.String()
	if strings.ContainsAny(printed, "\x1b\x07") {
		t.Fatalf("output = %q; a C0 control reached the writer", printed)
	}
	if strings.Contains(printed, "\u0085") || strings.Contains(printed, "\u009b") {
		t.Fatalf("output = %q; a C1 control reached the writer", printed)
	}
	if !strings.Contains(printed, `\u0085`) || !strings.Contains(printed, `\u009b`) {
		t.Fatalf("output = %q; want the C1 controls rendered as escapes", printed)
	}
	// A pipe or file keeps the exact payload bytes: only a terminal gets the
	// escaped render, and the JSON surface is never rewritten.
	for _, wantTTY := range []bool{true, false} {
		terminalOutput = func(io.Writer) bool { return wantTTY }
		r := &schemaFixtureRunner{structured: structured}
		cmd := NewRootCmd(func() runner.Runner { return r })
		cmd.SetArgs([]string{"respond", "--model", "local", "--schema", schema, "--json", "produce"})
		var piped bytes.Buffer
		cmd.SetOut(&piped)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(piped.Bytes(), &envelope); err != nil {
			t.Fatalf("JSON output: %v (%q)", err, piped.String())
		}
		if string(envelope["response"]) != string(structured) {
			t.Fatalf("tty=%v response = %s; want the exact payload %s", wantTTY, envelope["response"], structured)
		}
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

// TestLocalCapabilitiesMirrorsTheRouteSpec feeds the renderer a route with no
// capabilities at all. A literal true for guided output (or any other field)
// would survive a table with the real flags, so the empty case is what makes
// the derivation observable.
func TestLocalCapabilitiesMirrorsTheRouteSpec(t *testing.T) {
	empty := localCapabilities(runner.RouteSpec{})
	for name, value := range empty {
		if value {
			t.Fatalf("%s reported true for a route with no capabilities", name)
		}
	}
	if len(empty) != 6 {
		t.Fatalf("capability map = %#v; want the six published names", empty)
	}
	full := localCapabilities(runner.RouteSpec{Capabilities: runner.CapabilitySet{
		Images: true, MaxImages: 1, Streaming: true, StreamingUsage: true, Usage: true, Schema: true,
	}})
	for _, name := range []string{"streaming", "complete_usage", "stream_usage", "images", "guided_output"} {
		if !full[name] {
			t.Fatalf("%s reported false for a route that declares it", name)
		}
	}
	if full["tools"] {
		t.Fatal("tools is not implemented on any route")
	}
}

// TestEveryNonLocalRouteRefusesGuidedOutput walks the whole table rather than
// the two routes a fallback would pick, so a new non-native route cannot be
// added without inheriting the refusal.
func TestEveryNonLocalRouteRefusesGuidedOutput(t *testing.T) {
	stubConfigPath(t)
	schema := writeSchemaFixture(t, cliGuidedSchema)
	checked := 0
	for _, model := range append(runner.Models(), runner.ModelAuto) {
		if model == runner.ModelLocal {
			continue
		}
		if !model.Valid() {
			t.Fatalf("%q is not a valid model", model)
		}
		r := &schemaFixtureRunner{}
		cmd := NewRootCmd(func() runner.Runner { return r })
		cmd.SetArgs([]string{"respond", "--model", string(model), "--schema", schema, "produce"})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("%s: err=%v exit=%d; want a usage refusal", model, err, ExitCode(err))
		}
		if !strings.Contains(err.Error(), "--schema requires --model local") {
			t.Fatalf("%s: error = %v", model, err)
		}
		if r.calls != 0 {
			t.Fatalf("%s reached the runner %d times", model, r.calls)
		}
		checked++
	}
	// Models() is every Shortcut-backed route (local is native, not listed
	// there), so the loop covers those plus auto.
	if checked != len(runner.Models())+1 {
		t.Fatalf("checked %d routes, want %d", checked, len(runner.Models())+1)
	}
}
