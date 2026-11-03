// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPromptInjectionIsData(t *testing.T) {
	canary := "inject-canary-91c2"
	srv, dir := fixtureServer(t, "inject")
	srv.Env = map[string]string{"FIXTURE_CANARY": canary}
	m := attach(t, "fixture", srv, interactive())
	result, err := m.Call(callCtx(t), "fixture", "inject", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	framer, err := NewFramer()
	if err != nil {
		t.Fatal(err)
	}
	framed := framer.Frame("fixture", "inject", result)
	open := "<<<hollis tool result " + framer.Nonce()
	end := "<<<end hollis tool result " + framer.Nonce() + ">>>"
	if !strings.HasPrefix(framed, UntrustedPreamble+"\n"+open) || !strings.HasSuffix(framed, end) {
		t.Fatalf("frame boundaries wrong:\n%s", framed)
	}
	body := framed[len(UntrustedPreamble)+1 : len(framed)-len(end)]
	body = body[strings.Index(body, ">>>\n")+4:]
	if strings.Contains(body, "<<<") {
		t.Errorf("an unescaped delimiter survived inside the block:\n%s", body)
	}
	if strings.Count(framed, framer.Nonce()) != 2 {
		t.Error("the nonce appears outside its two markers")
	}
	if strings.ContainsRune(body, 0) || strings.Contains(body, "\u200b") || !strings.Contains(body, "\uFFFD") {
		t.Errorf("normalization failed: %q", body)
	}
	if !strings.Contains(body, canary) {
		t.Error("the result text was altered beyond escaping")
	}
	// The injected instruction cannot cause a call: only one execution,
	// the one that was requested.
	calls := readCalls(t, dir)
	if len(calls) != 1 || calls[0]["tool"] != "inject" {
		t.Errorf("calls = %v", calls)
	}
	meta, _ := json.Marshal(struct {
		S []ServerStatus
		R []CallRecord
	}{m.Servers(), m.Records()})
	if strings.Contains(string(meta), canary) {
		t.Error("result text reached metadata")
	}
}

func TestFramerNonceUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		f, err := NewFramer()
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Nonce()) != 32 || seen[f.Nonce()] {
			t.Fatalf("nonce %q repeated or wrong length", f.Nonce())
		}
		seen[f.Nonce()] = true
	}
}

func TestEscapeMarkers(t *testing.T) {
	for in, want := range map[string]string{
		"<<<x":       `\u003c<<x`,
		"<<<<x":      `\u003c\u003c<<x`,
		"a<<b":       "a<<b",
		"<<\u200b<x": `\u003c<<x`,
		"<<\u202e<":  `\u003c<<`,
		"<<<a<<<b":   `\u003c<<a\u003c<<b`,
		"plain":      "plain",
	} {
		if got := escapeMarkers(in); got != want {
			t.Errorf("escapeMarkers(%q) = %q; want %q", in, got, want)
		}
	}
	for n := 3; n < 100; n++ {
		if got := escapeMarkers(strings.Repeat("<", n)); strings.Contains(got, "<<<") {
			t.Fatalf("delimiter survived a run of %d: %q", n, got)
		}
	}
}

func TestRenderPrompt(t *testing.T) {
	prompt, err := RenderPrompt(Request{Server: "files", Tool: "read_file", Arguments: json.RawMessage(`{"path":"/a\u001b[31mb","n":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := "MCP tool call — server \"files\", tool \"read_file\"\nArguments:\n{\n  \"path\": \"/a\\u001b[31mb\",\n  \"n\": 1\n}\n\nRun this call? [y/N] "
	if prompt != want {
		t.Errorf("prompt =\n%q\nwant\n%q", prompt, want)
	}
	raw, _ := RenderPrompt(Request{Server: "s", Tool: "t\x1b]0;x\x07", Arguments: json.RawMessage(`{"k":"` + "\u202e" + `"}`)})
	if strings.ContainsAny(raw, "\x1b\x07\u202e") {
		t.Errorf("a terminal control character survived: %q", raw)
	}
	tier2, _ := RenderPrompt(Request{Server: "s", Tool: "t", Arguments: json.RawMessage(`{}`), ServerValidated: true})
	if !strings.Contains(tier2, "Hollis did not validate these arguments; the server will.") {
		t.Error("tier-2 line missing")
	}
	big := json.RawMessage(`{"a":"` + strings.Repeat("x", MaxApprovalArgs) + `"}`)
	_, err = RenderPrompt(Request{Server: "s", Tool: "t", Arguments: big})
	wantCode(t, err, CodeApprovalArgsLarge)
}

func TestAffirmative(t *testing.T) {
	for _, yes := range []string{"y", "Y", "yes", " YES \n", "Yes\r\n"} {
		if !Affirmative(yes) {
			t.Errorf("%q should approve", yes)
		}
	}
	for _, no := range []string{"", "\n", "n", "no", "yep", "y es", "sure", "1", "true"} {
		if Affirmative(no) {
			t.Errorf("%q should deny", no)
		}
	}
}

type fakeTTY struct {
	in  io.Reader
	out bytes.Buffer
}

func (f *fakeTTY) Read(p []byte) (int, error)  { return f.in.Read(p) }
func (f *fakeTTY) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTTY) Close() error                { return nil }

func TestTerminalApprover(t *testing.T) {
	for answer, want := range map[string]bool{"y\n": true, "yes\n": true, "yes": false, "y": false, " YES ": false, "\n": false, "": false, "no\n": false} {
		tty := &fakeTTY{in: strings.NewReader(answer)}
		a := TerminalApprover{Open: func() (io.ReadWriteCloser, error) { return tty, nil }}
		ok, _ := a.Approve(context.Background(), "PROMPT", Request{})
		if ok != want {
			t.Errorf("answer %q approved=%v; want %v", answer, ok, want)
		}
		if tty.out.String() != "PROMPT" {
			t.Errorf("prompt written as %q", tty.out.String())
		}
	}
	blocked, _ := io.Pipe()
	a := TerminalApprover{Open: func() (io.ReadWriteCloser, error) { return &fakeTTY{in: blocked}, nil }, Timeout: 20 * time.Millisecond}
	start := time.Now()
	ok, err := a.Approve(context.Background(), "PROMPT", Request{})
	if ok || err != context.DeadlineExceeded || time.Since(start) > 2*time.Second {
		t.Errorf("silence = %v, %v after %s; want a timeout denial", ok, err, time.Since(start))
	}
}

func TestRenderName(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"files", "read_file"}: "files__read_file",
		{"a-b", "x.y/z"}:       "a-b__x_y_z",
		{"s", "naïve"}:         "s__na_ve",
	} {
		if got := RenderName(in[0], in[1]); got != want {
			t.Errorf("RenderName(%q,%q) = %q; want %q", in[0], in[1], got, want)
		}
	}
}

func TestDefinitionPin(t *testing.T) {
	a := DefinitionPin("t", "d", json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`))
	b := DefinitionPin("t", "d", json.RawMessage(`{"properties":{"a":{"type":"string"}},"type":"object"}`))
	if a != b {
		t.Error("key order changed the pin")
	}
	for _, other := range []string{
		DefinitionPin("u", "d", json.RawMessage(`{"type":"object"}`)),
		DefinitionPin("t", "e", json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)),
		DefinitionPin("t", "d", json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"}}}`)),
	} {
		if other == a {
			t.Error("a changed definition kept its pin")
		}
	}
}
