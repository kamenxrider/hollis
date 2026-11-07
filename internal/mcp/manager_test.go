// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAttachListsAndCalls(t *testing.T) {
	server, dir := fixtureServer(t, "normal")
	m := attach(t, "fixture", server, interactive())
	tools := m.Tools()
	if len(tools) != 3 {
		t.Fatalf("offered %d tools; want 3", len(tools))
	}
	want := []string{"fixture__echo", "fixture__pick", "fixture__noargs"}
	for i, tool := range tools {
		if tool.Rendered != want[i] || tool.Tier != TierValidated {
			t.Errorf("tool %d = %s tier %s; want %s validated locally", i, tool.Rendered, tool.Tier, want[i])
		}
	}
	if tools[0].Description != "Echo the arguments back." {
		t.Errorf("description %q not carried", tools[0].Description)
	}
	status := m.Servers()[0]
	if status.Transport != "stdio" || status.Approval != "ask" || status.ToolsOffered != 3 || status.ProtocolVersion == "" {
		t.Errorf("status = %+v", status)
	}
	args := json.RawMessage(`{"text":"hello","n":9007199254740993}`)
	result, err := m.Call(callCtx(t), "fixture", "echo", args)
	if err != nil {
		t.Fatal(err)
	}
	// The integer is beyond float64 precision; it must reach the server as
	// the exact bytes that were approved.
	if !strings.Contains(result.Text, "9007199254740993") || result.IsError || result.Bytes != len(result.Text) {
		t.Errorf("result = %+v", result)
	}
	calls := readCalls(t, dir)
	if len(calls) != 1 || calls[0]["tool"] != "echo" {
		t.Errorf("calls = %v", calls)
	}
	records := m.Records()
	if len(records) != 1 || records[0].Decision != DecisionApproved || !records[0].Executed {
		t.Errorf("records = %+v", records)
	}
}

func TestInvalidArgumentsNeverReachServer(t *testing.T) {
	server, dir := fixtureServer(t, "normal")
	m := attach(t, "fixture", server, interactive())
	for _, args := range []string{`{"text":5}`, `{}`, `[1]`, `{"mode":"z"}`} {
		tool := "echo"
		if strings.Contains(args, "mode") {
			tool = "pick"
		}
		_, err := m.Call(callCtx(t), "fixture", tool, json.RawMessage(args))
		wantCode(t, err, CodeInvalidArguments)
	}
	assertNothingExecuted(t, dir)
}

func TestBudgetRefusalIsNotExecuted(t *testing.T) {
	server, dir := fixtureServer(t, "normal")
	m := attach(t, "fixture", server, interactive())
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if cancelled {
			cancel()
		}
		_, err := m.Call(ctx, "fixture", "noargs", json.RawMessage(`{}`))
		cancel()
		wantCode(t, err, CodeTimeout)
	}
	assertNothingExecuted(t, dir)
	for _, rec := range m.Records() {
		if rec.Executed {
			t.Errorf("budget refusal recorded as executed: %+v", rec)
		}
	}
}

func TestInvocationSchemaTextBudget(t *testing.T) {
	first, _ := fixtureServer(t, "tier2budget")
	second, _ := fixtureServer(t, "tier2budget")
	m, err := Attach(context.Background(), []Attachment{{Name: "first", Server: first}, {Name: "second", Server: second}}, interactive())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	total := 0
	for _, tool := range m.Tools() {
		total += len(tool.SchemaText)
	}
	if total > maxSchemaTextTotal || total < maxSchemaTextTotal-1800 {
		t.Fatalf("invocation schema text size = %d", total)
	}
	before := len(m.Tools())
	_, err = m.Call(callCtx(t), "first", "late", json.RawMessage(`{}`))
	wantCode(t, err, CodeUnknownTool)
	if len(m.Tools()) != before {
		t.Fatal("late tool displaced the offered snapshot")
	}
}

func TestInitialListingBlockedWriteDeadline(t *testing.T) {
	server, _ := fixtureServer(t, "cursorstall")
	server.ConnectTimeoutMS = 1000
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		m, err := Attach(context.Background(), []Attachment{{Name: "fixture", Server: server}}, interactive())
		if m != nil {
			m.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		wantCode(t, err, CodeTimeout)
	case <-time.After(4 * time.Second):
		t.Fatal("initial tools/list blocked past connect deadline")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("connect deadline took %s", time.Since(start))
	}
}

// TestAskFailsClosedNonInteractive covers every non-interactive condition.
// Nothing runs, nothing is asked, the server sees no request for the call,
// and the refusal names the tool.
func TestAskFailsClosedNonInteractive(t *testing.T) {
	cases := []struct {
		name                 string
		jsonOut, noIn, batch bool
		tty                  bool
	}{
		{name: "json", jsonOut: true, tty: true},
		{name: "agent", jsonOut: true, noIn: true, tty: true},
		{name: "no-input", noIn: true, tty: true},
		{name: "no terminal", tty: false},
		{name: "batch", batch: true, tty: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, dir := fixtureServer(t, "normal")
			approver := &countingApprover{}
			interaction := DetectInteractivity(tc.jsonOut, tc.noIn, tc.batch, func() bool { return tc.tty })
			if interaction.Interactive {
				t.Fatal("interaction should be refused")
			}
			m := attach(t, "fixture", server, Options{Interaction: interaction, Approver: approver})
			start := time.Now()
			_, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
			e := wantCode(t, err, CodeApprovalRequired)
			if e.Tool != "echo" || !strings.Contains(err.Error(), `"echo"`) || !strings.Contains(err.Error(), "Nothing was run") {
				t.Errorf("message %q does not name the tool", err)
			}
			if time.Since(start) > 5*time.Second {
				t.Errorf("refusal took %s", time.Since(start))
			}
			if approver.n.Load() != 0 {
				t.Error("an approver was consulted in a non-interactive run")
			}
			assertNothingExecuted(t, dir)
			if m.Records()[0].Decision != DecisionApprovalRequired || m.Records()[0].Executed {
				t.Errorf("record = %+v", m.Records()[0])
			}
		})
	}
}

// A pipe on stdin whose bytes spell "y" must never approve: the answer is
// read only from the terminal the approver opened.
func TestPipedYesNeverApproves(t *testing.T) {
	interaction := DetectInteractivity(false, false, false, func() bool { return false })
	if interaction.Interactive {
		t.Fatal("no terminal must mean non-interactive even with piped stdin")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	_, _ = writer.WriteString("y\n")
	writer.Close()
	savedStdin := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = savedStdin }()
	approver := TerminalApprover{Open: func() (io.ReadWriteCloser, error) { return nil, errors.New("no terminal") }}
	ok, err := approver.Approve(context.Background(), "prompt", Request{})
	if ok || err == nil {
		t.Fatalf("approve = %v, %v; want a denial with an error", ok, err)
	}
	server, dir := fixtureServer(t, "normal")
	m := attach(t, "fixture", server, Options{Interaction: interaction, Approver: approver})
	_, err = m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
	wantCode(t, err, CodeApprovalRequired)
	assertNothingExecuted(t, dir)
}

func TestDenialStopsRun(t *testing.T) {
	for name, approver := range map[string]Approver{
		"no":      noApprover{},
		"eof":     errApprover{io.EOF},
		"timeout": errApprover{context.DeadlineExceeded},
		"error":   errApprover{errors.New("boom")},
	} {
		t.Run(name, func(t *testing.T) {
			server, dir := fixtureServer(t, "normal")
			m := attach(t, "fixture", server, Options{Interaction: Interactivity{Interactive: true}, Approver: approver})
			result, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
			wantCode(t, err, CodeDenied)
			if result.Text != "" {
				t.Errorf("a denied call returned text %q", result.Text)
			}
			assertNothingExecuted(t, dir)
			if r := m.Records()[0]; r.Decision != DecisionDenied || r.Executed {
				t.Errorf("record = %+v", r)
			}
		})
	}
}

func TestEveryCallAsksAgain(t *testing.T) {
	server, dir := fixtureServer(t, "normal")
	approver := &countingApprover{}
	m := attach(t, "fixture", server, Options{Interaction: Interactivity{Interactive: true}, Approver: approver})
	for i := 0; i < 3; i++ {
		if _, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if approver.n.Load() != 3 || len(readCalls(t, dir)) != 3 {
		t.Errorf("approvals %d, calls %d; want 3 and 3", approver.n.Load(), len(readCalls(t, dir)))
	}
}

func pinned(t *testing.T, mode string, allow ...string) (Server, string) {
	t.Helper()
	server, dir := fixtureServer(t, mode)
	// Pins are taken from a first attach, the way 'hollis mcp allow' does.
	probe := attach(t, "fixture", server, interactive())
	server.Approval, server.Allow, server.Pins = ApprovalAuto, allow, map[string]string{}
	for _, name := range allow {
		for _, tool := range probe.ServerTools("fixture") {
			if tool.Name == name {
				server.Pins[name] = tool.Pin
			}
		}
	}
	probe.Close()
	return server, dir
}

// Auto runs an allowlisted tool on every call and in every round; a name
// outside the list never runs.
func TestAutoAllowlistIsTheOnlyAuthority(t *testing.T) {
	server, dir := pinned(t, "normal", "echo")
	m := attach(t, "fixture", server, Options{Interaction: NonInteractive("test"), Approver: noApprover{}})
	for i := 0; i < 3; i++ {
		if _, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	_, err := m.Call(callCtx(t), "fixture", "noargs", json.RawMessage(`{}`))
	e := wantCode(t, err, CodeNotAllowlisted)
	if !strings.Contains(e.Message, "hollis mcp allow fixture noargs") {
		t.Errorf("message %q does not name the remedy", e.Message)
	}
	if n := len(readCalls(t, dir)); n != 3 {
		t.Errorf("executed %d calls; want 3", n)
	}
	if r := m.Records(); r[0].Decision != DecisionAllowlisted || r[3].Decision != DecisionNotAllowlisted {
		t.Errorf("records = %+v", r)
	}
}

func TestAskIgnoresAllowList(t *testing.T) {
	server, _ := fixtureServer(t, "normal")
	server.Allow = []string{"echo"}
	approver := &countingApprover{}
	m := attach(t, "fixture", server, Options{Interaction: Interactivity{Interactive: true}, Approver: approver})
	if _, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if approver.n.Load() != 1 {
		t.Error("ask mode skipped the prompt for an allowlisted tool")
	}
}

func TestNewToolAppearsIsNotAutoRun(t *testing.T) {
	t.Run("auto", func(t *testing.T) {
		server, dir := pinned(t, "newtool", "echo", "pick")
		// Even an allowlist that names the late tool cannot cover it: the
		// entry was not reviewed against a definition that existed.
		server.Allow = append(server.Allow, "late")
		m := attach(t, "fixture", server, Options{Interaction: NonInteractive("test")})
		if _, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`)); err != nil {
			t.Fatal(err)
		}
		for _, tool := range m.Tools() {
			if tool.Name == "late" {
				t.Fatal("a late tool was offered")
			}
		}
		_, err := m.Call(callCtx(t), "fixture", "late", json.RawMessage(`{}`))
		wantCode(t, err, CodeNotAllowlisted)
		if n := len(readCalls(t, dir)); n != 1 {
			t.Errorf("executed %d calls; want 1", n)
		}
	})
	t.Run("ask", func(t *testing.T) {
		server, dir := fixtureServer(t, "newtool")
		approver := &recordApprover{}
		m := attach(t, "fixture", server, Options{Interaction: Interactivity{Interactive: true}, Approver: approver})
		if _, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Call(callCtx(t), "fixture", "late", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("approved late call: %v", err)
		}
		if len(approver.reqs) != 2 || !approver.reqs[1].Late || !strings.Contains(approver.prompts[1], "appeared after the session started") {
			t.Errorf("late call prompt = %q", approver.prompts)
		}
		if n := len(readCalls(t, dir)); n != 2 {
			t.Errorf("executed %d calls; want 2", n)
		}
		_, err := m.Call(callCtx(t), "fixture", "never", json.RawMessage(`{}`))
		wantCode(t, err, CodeUnknownTool)
	})
}

// A tool whose definition changes under the same name is not auto-run.
func TestChangedDefinitionIsNotAutoRun(t *testing.T) {
	server, dir := pinned(t, "normal", "echo")
	server.Args = append(server.Args, "variant=drifted")
	m := attach(t, "fixture", server, Options{Interaction: NonInteractive("test")})
	_, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
	wantCode(t, err, CodeDefinitionChanged)
	assertNothingExecuted(t, dir)

	unpinned, _ := fixtureServer(t, "normal")
	unpinned.Approval, unpinned.Allow = ApprovalAuto, []string{"echo"}
	m2 := attach(t, "fixture", unpinned, Options{Interaction: NonInteractive("test")})
	_, err = m2.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
	wantCode(t, err, CodeNotAllowlisted)
}

func TestArgumentDisplayCapRefuses(t *testing.T) {
	server, dir := fixtureServer(t, "normal")
	approver := &countingApprover{}
	m := attach(t, "fixture", server, Options{Interaction: Interactivity{Interactive: true}, Approver: approver})
	big, _ := json.Marshal(map[string]string{"text": strings.Repeat("a", MaxApprovalArgs)})
	_, err := m.Call(callCtx(t), "fixture", "echo", big)
	e := wantCode(t, err, CodeApprovalArgsLarge)
	if !strings.Contains(e.Message, "32768") {
		t.Errorf("message %q does not state the cap", e.Message)
	}
	if approver.n.Load() != 0 {
		t.Error("a prompt was shown for oversized arguments")
	}
	assertNothingExecuted(t, dir)
}

func TestCallDeadlineKillsSession(t *testing.T) {
	server, dir := fixtureServer(t, "slow")
	opts := interactive()
	opts.CallTimeout = 200 * time.Millisecond
	m := attach(t, "fixture", server, opts)
	start := time.Now()
	_, err := m.Call(callCtx(t), "fixture", "hang", json.RawMessage(`{}`))
	wantCode(t, err, CodeTimeout)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("deadline took %s", elapsed)
	}
	_, err = m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
	wantCode(t, err, CodeUnavailable)
	if n := len(readCalls(t, dir)); n != 1 {
		t.Errorf("calls = %d; want 1 (no retry)", n)
	}
	if r := m.Records()[0]; r.ErrorCode != CodeTimeout || !r.Executed {
		t.Errorf("record = %+v", r)
	}
}

func TestRemainingBudgetClamps(t *testing.T) {
	server, _ := fixtureServer(t, "slow")
	m := attach(t, "fixture", server, interactive())
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Call(ctx, "fixture", "hang", json.RawMessage(`{}`))
	e := wantCode(t, err, CodeTimeout)
	if !strings.Contains(e.Message, "--timeout") || time.Since(start) > 5*time.Second {
		t.Errorf("message %q after %s", e.Message, time.Since(start))
	}
	short, cancelShort := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelShort()
	server2, dir2 := fixtureServer(t, "normal")
	m2 := attach(t, "other", server2, interactive())
	_, err = m2.Call(short, "other", "echo", json.RawMessage(`{"text":"x"}`))
	e = wantCode(t, err, CodeTimeout)
	if !strings.Contains(e.Message, "not started") {
		t.Errorf("message %q", e.Message)
	}
	assertNothingExecuted(t, dir2)
}

// TestConnectDeadline: a server that never answers the handshake is bounded
// by Hollis's own connect deadline.
func TestConnectDeadline(t *testing.T) {
	for _, mode := range []string{"nodiscover", "grandchild"} {
		t.Run(mode, func(t *testing.T) {
			server, _ := fixtureServer(t, mode)
			server.ConnectTimeoutMS = 1000
			start := time.Now()
			err := attachErr(t, "fixture", server, interactive())
			wantCode(t, err, CodeTimeout)
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("connect took %s", elapsed)
			}
		})
	}
}

func TestToolResultCapsNoTruncation(t *testing.T) {
	server, _ := fixtureServer(t, "oversized")
	m := attach(t, "fixture", server, interactive())
	result, err := m.Call(callCtx(t), "fixture", "big_result", json.RawMessage(`{}`))
	e := wantCode(t, err, CodeResultTooLarge)
	if result.Text != "" || !strings.Contains(e.Message, "2097152") || !strings.Contains(e.Message, "1048576") {
		t.Errorf("result %d bytes, message %q", len(result.Text), e.Message)
	}

	server2, _ := fixtureServer(t, "oversized")
	m2 := attach(t, "fixture", server2, interactive())
	result, err = m2.Call(callCtx(t), "fixture", "huge_frame", json.RawMessage(`{}`))
	e = wantCode(t, err, CodeProtocol)
	if result.Text != "" || !strings.Contains(e.Message, "4194304") {
		t.Errorf("result %d bytes, message %q", len(result.Text), e.Message)
	}
}

func TestCrashMidCall(t *testing.T) {
	for _, tc := range []struct{ mode, tool string }{{"crash", "crash"}, {"silentclose", "echo"}} {
		t.Run(tc.mode, func(t *testing.T) {
			server, _ := fixtureServer(t, tc.mode)
			opts := interactive()
			opts.CallTimeout = 3 * time.Second
			m := attach(t, "fixture", server, opts)
			args := json.RawMessage(`{}`)
			if tc.tool == "echo" {
				args = json.RawMessage(`{"text":"x"}`)
			}
			start := time.Now()
			_, err := m.Call(callCtx(t), "fixture", tc.tool, args)
			code := CodeOf(err)
			if tc.mode == "crash" {
				// This fixture emits a truncated JSON-RPC object before exiting.
				if code != CodeProtocol {
					t.Fatalf("malformed frame classified as %s: %v", code, err)
				}
			} else if code != CodeUnavailable && code != CodeTimeout {
				t.Fatalf("code %s (%v); want mcp_unavailable", code, err)
			}
			if time.Since(start) > 6*time.Second {
				t.Errorf("crash took %s", time.Since(start))
			}
		})
	}
}

func TestProtocolFailuresAtAttach(t *testing.T) {
	for _, tc := range []struct {
		mode string
		code Code
		text string
	}{
		{"badjson", CodeUnavailable, ""},
		{"notools", CodeProtocol, "does not offer tools"},
		{"paginated-forever", CodeProtocol, "after 10 pages"},
		{"duplicate", CodeProtocol, "more than once"},
		{"many", CodeProtocol, "limit is 64"},
		{"collide", CodeProtocol, "collides"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			server, _ := fixtureServer(t, tc.mode)
			server.ConnectTimeoutMS = 3000
			err := attachErr(t, "fixture", server, interactive())
			if tc.mode == "badjson" {
				if c := CodeOf(err); c != CodeUnavailable && c != CodeProtocol && c != CodeTimeout {
					t.Fatalf("code %s (%v)", c, err)
				}
				return
			}
			e := wantCode(t, err, tc.code)
			if !strings.Contains(e.Message, tc.text) {
				t.Errorf("message %q; want %q", e.Message, tc.text)
			}
		})
	}
}

func TestToolsPagination(t *testing.T) {
	server, _ := fixtureServer(t, "paginated")
	m := attach(t, "fixture", server, interactive())
	if n := len(m.Tools()); n != 6 {
		t.Fatalf("offered %d tools across 3 pages; want 6", n)
	}
}

func TestClientExcludedToolsReported(t *testing.T) {
	server, _ := fixtureServer(t, "badheader")
	m := attach(t, "fixture", server, interactive())
	status := m.Servers()[0]
	if status.ToolsExcluded != 1 || status.ToolsOffered != 1 {
		t.Errorf("status = %+v; want 1 excluded, 1 offered", status)
	}
}

// Rendered names over the loop's 64-character rule make the tool
// unsupported at attach, never truncated.
func TestLongRenderedNameUnsupported(t *testing.T) {
	server, _ := fixtureServer(t, "longname")
	m := attach(t, "fixture", server, interactive())
	if len(m.Tools()) != 1 {
		t.Fatalf("offered %d tools; want 1", len(m.Tools()))
	}
	all := m.ServerTools("fixture")
	long := all[1]
	if long.Tier != TierUnsupported || !strings.Contains(long.Reason, "64") {
		t.Errorf("long tool = tier %s reason %q", long.Tier, long.Reason)
	}
	if m.Servers()[0].ToolsUnsupported != 1 {
		t.Errorf("status = %+v", m.Servers()[0])
	}
	_, err := m.Call(callCtx(t), "fixture", long.Name, json.RawMessage(`{}`))
	wantCode(t, err, CodeUnknownTool)
}

func TestResultMapping(t *testing.T) {
	server, _ := fixtureServer(t, "multiblock")
	m := attach(t, "fixture", server, interactive())
	result, err := m.Call(callCtx(t), "fixture", "structured", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "first\nsecond\n" + structuredLabel + "\n" + `{"answer":42}`
	if result.Text != want {
		t.Errorf("text = %q; want %q", result.Text, want)
	}
	server2, _ := fixtureServer(t, "multiblock")
	m2 := attach(t, "fixture", server2, interactive())
	result, err = m2.Call(callCtx(t), "fixture", "image", json.RawMessage(`{}`))
	e := wantCode(t, err, CodeProtocol)
	if result.Text != "" || !strings.Contains(e.Message, "image") {
		t.Errorf("message %q", e.Message)
	}
}

func TestSchemaTierCorpus(t *testing.T) {
	want := map[string]Tier{
		"strict": TierValidated, "annotated": TierValidated, "ap_absent": TierValidated,
		"nullable": TierValidated, "pattern": TierValidated,
		"ap_true": TierServerValidated, "union_string_array": TierServerValidated, "ref": TierServerValidated,
		"nested_depth_4": TierServerValidated, "non_string_enum": TierServerValidated, "props_33": TierServerValidated,
		"root_not_object": TierUnsupported, "size_70k": TierUnsupported,
	}
	server, _ := fixtureServer(t, "corpus")
	m := attach(t, "fixture", server, interactive())
	got := map[string]Tool{}
	for _, tool := range m.ServerTools("fixture") {
		got[tool.Name] = tool
	}
	for name, tier := range want {
		tool, ok := got[name]
		if !ok {
			t.Errorf("%s missing", name)
			continue
		}
		if tool.Tier != tier && !widenedPending(name, tool.Tier) {
			t.Errorf("%s: tier %s (%s at %q); want %s", name, tool.Tier, tool.Reason, tool.Pointer, tier)
		}
		if tool.Tier == TierServerValidated && (tool.SchemaText == "" || len(tool.SchemaText) > maxSchemaTextBytes) {
			t.Errorf("%s: schema text %d bytes", name, len(tool.SchemaText))
		}
	}
	if !strings.Contains(got["root_not_object"].Reason, "root schema must be an object") {
		t.Errorf("root reason %q", got["root_not_object"].Reason)
	}
}

// widenedPending accepts tier 2 for the shapes that only the widened shared
// subset admits, so this suite runs before and after that subset lands.
func widenedPending(name string, got Tier) bool {
	switch name {
	case "annotated", "ap_absent", "nullable", "pattern":
		return got == TierServerValidated && !sharedSubsetWidened()
	}
	return false
}

func TestTierTwoIsAskOnly(t *testing.T) {
	server, _ := fixtureServer(t, "corpus")
	server.Approval, server.Allow = ApprovalAuto, []string{"ref"}
	err := attachErr(t, "fixture", server, interactive())
	e := wantCode(t, err, CodeSchemaUnvalidated)
	if !strings.Contains(e.Message, "hollis mcp mode fixture ask") || !strings.Contains(e.Message, "hollis mcp deny fixture ref") {
		t.Errorf("message %q lacks remedies", e.Message)
	}

	server2, _ := fixtureServer(t, "corpus")
	approver := &recordApprover{}
	m := attach(t, "fixture", server2, Options{Interaction: Interactivity{Interactive: true}, Approver: approver})
	if _, err := m.Call(callCtx(t), "fixture", "ref", json.RawMessage(`{"q":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(approver.prompts[0], "Hollis did not validate these arguments; the server will.") {
		t.Errorf("prompt %q lacks the delegation line", approver.prompts[0])
	}
}

func TestProcessGroupKilled(t *testing.T) {
	server, dir := fixtureServer(t, "noisyexit")
	m := attach(t, "fixture", server, interactive())
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "grandchild.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("grandchild never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.Close()
	m.Close()
	assertGroupGone(t, dir)
}

func TestIgnoreStdinBoundedShutdown(t *testing.T) {
	server, _ := fixtureServer(t, "ignorestdin")
	opts := interactive()
	opts.CallTimeout = 300 * time.Millisecond
	m := attach(t, "fixture", server, opts)
	_, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
	wantCode(t, err, CodeTimeout)
	start := time.Now()
	m.Close()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("close took %s", elapsed)
	}
}

func TestAttachIsAllOrNothing(t *testing.T) {
	good, goodDir := fixtureServer(t, "normal")
	bad, _ := fixtureServer(t, "notools")
	ctx := callCtx(t)
	_, err := Attach(ctx, []Attachment{{Name: "good", Server: good}, {Name: "bad", Server: bad}}, interactive())
	e := wantCode(t, err, CodeProtocol)
	if e.Server != "bad" {
		t.Errorf("failure names %q; want bad", e.Server)
	}
	assertGroupGone(t, goodDir)
}

func TestStartFailureClassified(t *testing.T) {
	err := attachErr(t, "missing", Server{Command: "/nonexistent/hollis-mcp-server"}, interactive())
	e := wantCode(t, err, CodeUnavailable)
	if !strings.Contains(e.Message, "program not found") {
		t.Errorf("message %q", e.Message)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "noexec")
	_ = os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600)
	err = attachErr(t, "noexec", Server{Command: path}, interactive())
	e = wantCode(t, err, CodeUnavailable)
	if !strings.Contains(e.Message, "permission denied") {
		t.Errorf("message %q", e.Message)
	}
}

// Concurrent Tools/Call/Close under -race. Calls serialize; Close during a
// call fails that call instead of racing it.
func TestConcurrentUse(t *testing.T) {
	server, _ := fixtureServer(t, "newtool")
	m := attach(t, "fixture", server, interactive())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
		}()
		go func() {
			defer wg.Done()
			_ = m.Tools()
			_ = m.Servers()
			_ = m.Records()
		}()
	}
	wg.Wait()
	var closers sync.WaitGroup
	for i := 0; i < 4; i++ {
		closers.Add(1)
		go func() {
			defer closers.Done()
			m.Close()
		}()
	}
	closers.Wait()
	_, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"x"}`))
	wantCode(t, err, CodeUnavailable)
}
