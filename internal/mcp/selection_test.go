// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestToolSelectionConfigRoundTrip(t *testing.T) {
	for _, names := range [][]string{nil, {}, {"echo", "noargs"}} {
		path := filepath.Join(t.TempDir(), FileName)
		cfg := Config{SchemaVersion: SchemaVersion, Servers: map[string]Server{
			"fixture": {Command: "fixture", Tools: names},
		}}
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		s := got.Servers["fixture"]
		if !slices.Equal(s.Tools, names) || s.Selected("pick") != (len(names) == 0) || !s.Selected("echo") {
			t.Fatalf("selection did not round-trip: %+v", s)
		}
	}
}

func TestToolSelectionConfigRefusals(t *testing.T) {
	for _, names := range [][]string{{""}, {"echo", "echo"}, {"*"}, {"echo,pick"}, {"echo\npick"}, make([]string, MaxToolsPerServer+1)} {
		err := ValidateServer("fixture", Server{Command: "fixture", Tools: names})
		e := wantCode(t, err, CodeConfig)
		if !strings.Contains(e.Message, "servers.fixture.tools") {
			t.Fatalf("wrong field: %s", e.Message)
		}
	}
}

func TestToolSelectionFiltersOffersAndRejectsCalls(t *testing.T) {
	for _, nonInteractive := range []bool{false, true} {
		server, dir := fixtureServer(t, "normal")
		server.Tools = []string{"echo"}
		opts := interactive()
		if nonInteractive {
			opts.Interaction = NonInteractive("selection test")
		}
		m := attach(t, "fixture", server, opts)
		if tools := m.Tools(); len(tools) != 1 || tools[0].Name != "echo" {
			t.Fatalf("offered tools = %+v", tools)
		}
		if _, ok := m.Lookup("fixture__pick"); ok {
			t.Fatal("filtered tool found by rendered name")
		}
		status := m.Servers()[0]
		if status.ToolsOffered != 1 || status.ToolsFiltered != 2 || status.ToolsUnsupported != 0 {
			t.Fatalf("counts = %+v", status)
		}
		if len(m.ServerTools("fixture")) != 3 {
			t.Fatal("inspection lost filtered tools")
		}
		for _, name := range []string{"pick", "invented"} {
			_, err := m.Call(callCtx(t), "fixture", name, json.RawMessage(`{}`))
			e := wantCode(t, err, CodeUnknownTool)
			if !strings.Contains(e.Message, "tools filter") {
				t.Fatal(e.Message)
			}
		}
		assertNothingExecuted(t, dir)
		for _, rec := range m.Records() {
			if rec.Executed || rec.Decision != DecisionRefused {
				t.Fatalf("filtered call record = %+v", rec)
			}
		}
		if !nonInteractive {
			if _, err := m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"selected"}`)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestToolSelectionUnknownFailsAtConnect(t *testing.T) {
	server, dir := fixtureServer(t, "normal")
	server.Tools = []string{"echo", "missing"}
	m, err := Attach(context.Background(), []Attachment{{Name: "fixture", Server: server}}, interactive())
	if m != nil {
		m.Close()
		t.Fatal("unknown filter attached")
	}
	e := wantCode(t, err, CodeConfig)
	if !strings.Contains(e.Message, `"missing"`) || !strings.Contains(e.Message, "tools/list did not return it") {
		t.Fatal(e.Message)
	}
	assertNothingExecuted(t, dir)
}

func TestToolSelectionCannotBypassAutoPolicy(t *testing.T) {
	server, dir := fixtureServer(t, "normal")
	server.Tools, server.Allow = []string{"echo"}, []string{"pick"}
	server.Approval = ApprovalAuto
	m := attach(t, "fixture", server, Options{Interaction: NonInteractive("selection test")})
	_, err := m.Call(callCtx(t), "fixture", "pick", json.RawMessage(`{"mode":"a"}`))
	wantCode(t, err, CodeUnknownTool)
	_, err = m.Call(callCtx(t), "fixture", "echo", json.RawMessage(`{"text":"selected but not allowed"}`))
	if err == nil {
		t.Fatal("selection granted automatic approval")
	}
	assertNothingExecuted(t, dir)
}
