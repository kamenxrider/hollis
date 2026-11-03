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
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// toolFixtureRunner scripts tool rounds and records what each was sent.
type toolFixtureRunner struct {
	replies  []string
	err      error
	prompts  []string
	modes    []string
	requests []runner.ToolRoundRequest
}

func (r *toolFixtureRunner) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	panic("tool requests must use tool rounds")
}

func (r *toolFixtureRunner) RunToolRound(_ context.Context, m runner.Model, request runner.ToolRoundRequest) (runner.Completion, error) {
	r.prompts = append(r.prompts, request.Prompt)
	r.modes = append(r.modes, request.Mode)
	r.requests = append(r.requests, request)
	if r.err != nil {
		return runner.Completion{}, r.err
	}
	reply := r.replies[len(r.prompts)-1]
	return runner.Completion{Text: reply, Model: m, Usage: &runner.Usage{InputTokens: 5, OutputTokens: 2}}, nil
}

const cliTools = `[{"type":"function","function":{"name":"get_weather","description":"Get the weather",` +
	`"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]`

type pdfToolFixtureRunner struct {
	toolFixtureRunner
	extractor pdfRecordingRunner
}

func (r *pdfToolFixtureRunner) ExtractPDF(ctx context.Context, data []byte, pages []int) (runner.Extraction, error) {
	return r.extractor.ExtractPDF(ctx, data, pages)
}

func TestRespondToolsPreservesPDFExtraction(t *testing.T) {
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	tools := writeToolsFixture(t, cliTools)
	r := &pdfToolFixtureRunner{
		toolFixtureRunner: toolFixtureRunner{replies: []string{"Done."}},
		extractor:         pdfRecordingRunner{extraction: twoPages()},
	}
	_, _, err := runRespond(t, r, "--model", "local", "--tools", tools,
		"--file", path, "--pages", "2,7", "Summarize")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.extractor.extracts) != 1 || len(r.requests) != 1 ||
		len(r.requests[0].Definitions) != 1 ||
		!strings.Contains(r.requests[0].Prompt, "--- Page 2 ---\nPage two text.") {
		t.Fatalf("extracts=%d requests=%+v", len(r.extractor.extracts), r.requests)
	}
}

func TestRespondPDFToolRefusalContext(t *testing.T) {
	const canary = "PRIVATE_EXTRACTED_PAGE"
	for _, input := range []struct {
		name, body string
		pdf        bool
	}{
		{"pdf", pdfBytes, true},
		{"text", canary, false},
	} {
		for _, toolsEnabled := range []bool{false, true} {
			for _, code := range []string{"request_declined", "context_capacity"} {
				name := input.name + "/" + code
				if toolsEnabled {
					name += "/tools"
				} else {
					name += "/plain"
				}
				t.Run(name, func(t *testing.T) {
					failure := &runner.Error{Kind: runner.KindRequestDeclined, ExitCode: -1, Err: errors.New("private runner detail")}
					if code == "context_capacity" {
						failure.Kind = runner.KindContextCapacity
					}
					extraction := runner.Extraction{Media: "pdf", PageCount: 1, TextBytes: len(canary),
						Pages: []runner.ExtractedPage{{Page: 1, Text: canary, Method: "text"}}}
					plain := &pdfRecordingRunner{extraction: extraction, runErr: failure}
					tool := &pdfToolFixtureRunner{
						toolFixtureRunner: toolFixtureRunner{err: failure},
						extractor:         pdfRecordingRunner{extraction: extraction},
					}
					var r runner.Runner = plain
					args := []string{"--model", "local", "--file", writeRespondFile(t, "input", input.body), "Summarize"}
					if toolsEnabled {
						r = tool
						args = append(args, "--tools", writeToolsFixture(t, cliTools))
					}
					out, stderr, err := runRespond(t, r, args...)
					var ce *cliError
					if !errors.As(err, &ce) || ExitCode(err) != 5 || machineCode(err) != code {
						t.Fatalf("classification: %v", err)
					}
					wantNote := input.pdf && code == "request_declined"
					if got := strings.Count(err.Error(), extractedDeclinedNote); got != 0 && !wantNote || got != 1 && wantNote {
						t.Fatalf("extraction note count=%d, want note=%v: %v", got, wantNote, err)
					}
					prompt, calls, extracts := plain.prompt, plain.calls, len(plain.extracts)
					if toolsEnabled {
						calls, extracts = len(tool.requests), len(tool.extractor.extracts)
						if calls == 1 {
							prompt = tool.requests[0].Prompt
						}
					}
					if calls != 1 || !strings.Contains(prompt, canary) || (extracts == 1) != input.pdf {
						t.Fatalf("wrong dispatch: calls=%d extracts=%d", calls, extracts)
					}
					for _, diagnostic := range []string{out, stderr, err.Error()} {
						if strings.Contains(diagnostic, canary) || strings.Contains(diagnostic, "private runner detail") {
							t.Fatalf("private content leaked: %q", diagnostic)
						}
					}
				})
			}
		}
	}
}

func writeToolsFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func executeTools(t *testing.T, r runner.Runner, args ...string) (string, error) {
	t.Helper()
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	return out.String(), err
}

func TestRespondToolsPrintsValidatedCalls(t *testing.T) {
	tools := writeToolsFixture(t, cliTools)
	r := &toolFixtureRunner{replies: []string{`{"tool_calls":[{"function":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}]}`}}
	out, err := executeTools(t, r, "respond", "--model", "local", "--tools", tools, "Weather in Tokyo?")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not one JSON document: %v %q", err, out)
	}
	calls, _ := got["tool_calls"].([]any)
	if got["finish_reason"] != "tool_calls" || len(calls) != 1 || got["text"] != nil {
		t.Fatalf("document = %#v", got)
	}
	call := calls[0].(map[string]any)
	function := call["function"].(map[string]any)
	if call["id"] != "call_1_0" || call["type"] != "function" || function["name"] != "get_weather" || function["arguments"] != `{"city":"Tokyo"}` {
		t.Fatalf("call = %#v", call)
	}
	if r.modes[0] != "allowed" || len(r.requests[0].Definitions) != 1 || r.requests[0].Definitions[0].Name != "get_weather" || strings.Contains(r.prompts[0], `"name":"get_weather"`) {
		t.Fatalf("round = %v %q", r.modes, r.prompts[0])
	}
}

func TestRespondToolsPlainAnswerAndChoiceNone(t *testing.T) {
	tools := writeToolsFixture(t, cliTools)
	r := &toolFixtureRunner{replies: []string{"No tool needed."}}
	out, err := executeTools(t, r, "respond", "--model", "local", "--tools", tools, "--tool-choice", "none", "hello")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["finish_reason"] != "stop" || got["text"] != "No tool needed." || r.modes[0] != "disallowed" {
		t.Fatalf("document = %#v modes=%v", got, r.modes)
	}
	if strings.Contains(r.prompts[0], "get_weather") {
		t.Fatal("tool_choice none must keep tools out of the prompt")
	}
}

func TestRespondToolsRefusalsHappenBeforeTheRunner(t *testing.T) {
	tools := writeToolsFixture(t, cliTools)
	badTools := writeToolsFixture(t, `[{"type":"function","function":{"name":"x","description":"d","parameters":{"type":"object","properties":{},"additionalProperties":true}}}]`)
	schema := writeSchemaFixture(t, cliGuidedSchema)
	image := writeCLIImage(t, "fixture.png")
	cases := []struct {
		name   string
		args   []string
		wantIn string
	}{
		{"cloud route", []string{"respond", "--model", "cloud", "--tools", tools, "hi"}, "--tools requires --model local"},
		{"auto route", []string{"respond", "--tools", tools, "hi"}, "--tools requires --model local"},
		{"stream", []string{"respond", "--model", "local", "--tools", tools, "--stream", "hi"}, "complete-only"},
		{"schema", []string{"respond", "--model", "local", "--tools", tools, "--schema", schema, "hi"}, "--tools cannot be combined with --schema"},
		{"image", []string{"respond", "--model", "cloud", "--tools", tools, "--image", image, "hi"}, "--tools cannot be combined with --image"},
		{"native image", []string{"respond", "--model", "local", "--tools", tools, "--image", image, "hi"}, "--tools cannot be combined with --image"},
		{"code", []string{"respond", "--model", "local", "--tools", tools, "--code", "hi"}, "--tools cannot be combined with --code"},
		{"json", []string{"respond", "--model", "local", "--tools", tools, "--json", "hi"}, "remove --json/--agent"},
		{"agent", []string{"respond", "--model", "local", "--tools", tools, "--agent", "hi"}, "remove --json/--agent"},
		{"named choice", []string{"respond", "--model", "local", "--tools", tools, "--tool-choice", "get_weather", "hi"}, "--tool-choice accepts only auto or none"},
		{"required choice", []string{"respond", "--model", "local", "--tools", tools, "--tool-choice", "required", "hi"}, "--tool-choice accepts only auto or none"},
		{"choice without tools", []string{"respond", "--model", "local", "--tool-choice", "none", "hi"}, "--tool-choice requires --tools"},
		{"bad schema", []string{"respond", "--model", "local", "--tools", badTools, "hi"}, "--tools[0].function.parameters: schema at /additionalProperties"},
		{"missing file", []string{"respond", "--model", "local", "--tools", filepath.Join(t.TempDir(), "absent.json"), "hi"}, "tools file \"absent.json\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &toolFixtureRunner{replies: []string{"Done."}}
			_, err := executeTools(t, r, tc.args...)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("err=%v exit=%d; want exit 2 containing %q", err, ExitCode(err), tc.wantIn)
			}
			if len(r.prompts) != 0 {
				t.Fatal("refused request reached the runner")
			}
		})
	}
}

func TestRespondToolsLoopFaultsAreExitFive(t *testing.T) {
	tools := writeToolsFixture(t, cliTools)
	bad := `{"tool_calls":[{"function":"get_weather","arguments":"{}"}]}`
	r := &toolFixtureRunner{replies: []string{bad, bad}}
	_, err := executeTools(t, r, "respond", "--model", "local", "--tools", tools, "weather")
	var classified *cliError
	if !errors.As(err, &classified) || ExitCode(err) != 5 || classified.machineCode != "tool_call_invalid" {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "round 1") || strings.Contains(err.Error(), "{}") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestRespondToolsMalformedAttemptCorrection(t *testing.T) {
	tools := writeToolsFixture(t, cliTools)
	bad := `{"tool_calls":[{"function":{"name":"get_weather","arguments":{"city":<ctrl46>Tokyo<ctrl46>}}}]}`
	for _, corrected := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid", true: "corrected"}[corrected], func(t *testing.T) {
			second := bad
			if corrected {
				second = `{"tool_calls":[{"function":"get_weather","arguments":{"city":"Tokyo"}}]}`
			}
			r := &toolFixtureRunner{replies: []string{bad, second}}
			out, err := executeTools(t, r, "respond", "--model", "local", "--tools", tools, "Weather in Tokyo?")
			if len(r.prompts) != 2 || strings.Contains(out, "ctrl46") {
				t.Fatalf("rounds=%d output=%q", len(r.prompts), out)
			}
			if !corrected {
				var failure *cliError
				if !errors.As(err, &failure) || failure.machineCode != "tool_call_invalid" || ExitCode(err) != 5 || out != "" {
					t.Fatalf("error=%v output=%q", err, out)
				}
				return
			}
			var got map[string]any
			if err != nil || json.Unmarshal([]byte(out), &got) != nil {
				t.Fatalf("error=%v output=%q", err, out)
			}
			if got["finish_reason"] != "tool_calls" || got["rounds"] != float64(2) {
				t.Fatalf("output=%#v", got)
			}
			call := got["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
			if call["arguments"] != `{"city":"Tokyo"}` {
				t.Fatalf("unvalidated call=%#v", call)
			}
		})
	}
}
