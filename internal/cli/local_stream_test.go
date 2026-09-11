// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

type localFixtureRunner struct {
	calls, streams int
	prompt, text   string
	err            error
}

func (r *localFixtureRunner) Run(ctx context.Context, m runner.Model, p string) (string, runner.Model, error) {
	c, e := r.RunComplete(ctx, m, p)
	return c.Text, c.Model, e
}
func (r *localFixtureRunner) RunComplete(_ context.Context, m runner.Model, p string) (runner.Completion, error) {
	r.calls++
	r.prompt = p
	return runner.Completion{Text: r.text, Model: m, Usage: &runner.Usage{InputTokens: 7, OutputTokens: 3}}, r.err
}
func (r *localFixtureRunner) Stream(_ context.Context, m runner.Model, p string, emit func(string) error) (runner.Completion, error) {
	r.streams++
	r.prompt = p
	for _, b := range []byte(r.text) {
		if err := emit(string([]byte{b})); err != nil {
			return runner.Completion{}, err
		}
	}
	return runner.Completion{Text: r.text, Model: m}, r.err
}
func TestNativeOutputSelection(t *testing.T) {
	for _, tc := range []struct {
		name           string
		tty            bool
		args           []string
		streams, calls int
		wantErr        bool
	}{
		{"terminal", true, nil, 1, 0, false}, {"redirected", false, nil, 0, 1, false},
		{"explicit redirect", false, []string{"--stream"}, 1, 0, false}, {"disabled terminal", true, []string{"--stream=false"}, 0, 1, false},
		{"json terminal", true, []string{"--json"}, 0, 1, false}, {"agent terminal", true, []string{"--agent"}, 0, 1, false},
		{"json explicit", true, []string{"--json", "--stream"}, 0, 0, true}, {"agent explicit", true, []string{"--agent", "--stream"}, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			old := terminalOutput
			terminalOutput = func(io.Writer) bool { return tc.tty }
			t.Cleanup(func() { terminalOutput = old })
			r := &localFixtureRunner{text: "Hello"}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs(append([]string{"respond", "--model", "local", "hi"}, tc.args...))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err %v", err)
			}
			if r.calls != tc.calls || r.streams != tc.streams {
				t.Fatalf("complete %d streaming %d", r.calls, r.streams)
			}
			if !tc.wantErr && strings.Contains(strings.Join(tc.args, " "), "--json") {
				var value map[string]any
				if err := json.Unmarshal(out.Bytes(), &value); err != nil {
					t.Fatal(err)
				}
				if value["response"] != "Hello" || value["usage"] == nil {
					t.Fatalf("%v", value)
				}
			}
			if !tc.wantErr && len(tc.args) == 0 && out.String() != "Hello\n" {
				t.Fatalf("duplicated/missing output %q", out.String())
			}
		})
	}
}
func TestTerminalStreamEscapesEveryByteBoundary(t *testing.T) {
	raw := "é😀\x1b[2J\x1b[H\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\\x1b]52;c;abc\a\r\u009b31m\x7f\n\t"
	want := sanitizeTerminalText(raw)
	for split := 0; split <= len(raw); split++ {
		var out bytes.Buffer
		w := terminalStreamWriter{out: &out}
		if _, err := w.Write([]byte(raw[:split])); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(raw[split:])); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Fatalf("split %d: %q want %q", split, out.String(), want)
		}
	}
	var out bytes.Buffer
	w := terminalStreamWriter{out: &out}
	w.Write([]byte{0xc2})
	w.Flush()
	if out.String() != `\xc2` {
		t.Fatalf("incomplete UTF8: %q", out.String())
	}
}
func TestLocalStreamingFailureDoesNotPersistOrShortenHistory(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	r := &localFixtureRunner{text: "First raw \x1b[2J"}
	factory := func() runner.Runner { return r }
	_, conv, err := runFirstTurn(context.Background(), st, runner.ModelLocal, "Remember all of this", factory, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.Messages(conv.ID)
	r.err = &runner.Error{Kind: runner.KindContextCapacity, Stderr: "local context capacity exceeded"}
	r.text = "partial"
	var out bytes.Buffer
	ctx := context.WithValue(context.Background(), streamOutputKey{}, io.Writer(&out))
	_, err = runTurnResult(ctx, st, conv, "followup", factory, 0)
	if err == nil {
		t.Fatal("expected capacity failure")
	}
	after, _ := st.Messages(conv.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed turn changed history")
	}
	if r.calls != 1 || r.streams != 1 {
		t.Fatalf("retry detected %d %d", r.calls, r.streams)
	}
	if r.prompt != chat.RenderTranscript(before, "followup") {
		t.Fatal("conversation shortened or changed")
	}
}
func TestLocalOversizedHistoryRejectsBeforeDispatch(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	r := &localFixtureRunner{text: strings.Repeat("x", chat.MaxRenderedPromptBytes)}
	factory := func() runner.Runner { return r }
	_, conv, err := runFirstTurn(context.Background(), st, runner.ModelLocal, "first", factory, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.Messages(conv.ID)
	_, err = runTurnResult(context.Background(), st, conv, "second", factory, 0)
	if err == nil {
		t.Fatal("expected oversized transcript error")
	}
	after, _ := st.Messages(conv.ID)
	if !reflect.DeepEqual(before, after) || r.calls != 1 {
		t.Fatal("history modified or dispatched oversized conversation")
	}
}
func TestLocalBypassesShortcutDiscovery(t *testing.T) {
	stubConfigPath(t)
	old := listInstalledShortcuts
	listInstalledShortcuts = func(context.Context) ([]string, error) {
		t.Fatal("local attempted Shortcuts discovery")
		return nil, errors.New("absent")
	}
	t.Cleanup(func() { listInstalledShortcuts = old })
	// A routed runner is recognized as real, unlike generic fixture runners.
	resolved, err := resolveForModel(context.Background(), func() runner.Runner { return runner.NewRouted() }, runner.ModelLocal)
	if err != nil || resolved != nil {
		t.Fatalf("%v %v", resolved, err)
	}
}

func TestLocalOneShotChatOutputAndRawPersistence(t *testing.T) {
	for _, mode := range []string{"human", "json", "agent"} {
		t.Run(mode, func(t *testing.T) {
			stubConfigPath(t)
			st := openTempStore(t)
			oldOpen := openStore
			openStore = func() (*store.Store, error) { return st, nil }
			t.Cleanup(func() { openStore = oldOpen })
			oldTTY := terminalOutput
			terminalOutput = func(io.Writer) bool { return true }
			t.Cleanup(func() { terminalOutput = oldTTY })
			raw := "é\x1b[2Jhello"
			r := &localFixtureRunner{text: raw}
			cmd := NewRootCmd(func() runner.Runner { return r })
			args := []string{"chat", "--model", "local", "hello"}
			if mode != "human" {
				args = append(args, "--"+mode)
			}
			cmd.SetArgs(args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if mode == "human" {
				if r.streams != 1 || out.String() != sanitizeTerminalText(raw)+"\n" {
					t.Fatalf("wrong human stream %q streams=%d", out.String(), r.streams)
				}
			} else {
				if r.streams != 0 || r.calls != 1 {
					t.Fatal("machine chat streamed")
				}
				var value map[string]any
				if err := json.Unmarshal(out.Bytes(), &value); err != nil {
					t.Fatal(err)
				}
				if mode == "agent" {
					value = value["results"].(map[string]any)
				}
				if value["response"] != raw {
					t.Fatal("machine response altered")
				}
			}
		})
	}
}

func TestLocalNativeOnlyModelsDoesNotDiscoverShortcuts(t *testing.T) {
	stubConfigPath(t)
	old := listInstalledShortcuts
	listInstalledShortcuts = func(context.Context) ([]string, error) {
		t.Fatal("native status discovered Shortcuts")
		return nil, nil
	}
	t.Cleanup(func() { listInstalledShortcuts = old })
	cmd := NewRootCmd(func() runner.Runner { return &localFixtureRunner{} })
	cmd.SetArgs([]string{"models", "--model", "local", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["model"] != "local" || rows[0]["inference_verified"] != false {
		t.Fatalf("%v", rows)
	}
}

func TestLocalInteractiveStreamsOnceAndStoresRaw(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	raw := "hello\x1b[2Jé"
	r := &localFixtureRunner{text: raw}
	var out bytes.Buffer
	ctx := context.WithValue(context.Background(), streamOutputKey{}, io.Writer(&out))
	if err := runInteractiveChat(ctx, st, "local", "", func() runner.Runner { return r }, 0, strings.NewReader("first\nsecond\n"), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if r.streams != 2 || r.calls != 0 || strings.Count(out.String(), sanitizeTerminalText(raw)) != 2 {
		t.Fatalf("streams %d calls %d output %q", r.streams, r.calls, out.String())
	}
	convs, err := st.ListConversations(true)
	if err != nil || len(convs) != 1 {
		t.Fatalf("%v %v", convs, err)
	}
	messages, err := st.Messages(convs[0].ID)
	if err != nil || len(messages) != 4 {
		t.Fatalf("%v %v", messages, err)
	}
	if messages[1].Content != raw || messages[3].Content != raw {
		t.Fatal("stored response was sanitized")
	}
}
func TestLocalConfigUsesNoBridge(t *testing.T) {
	stubConfigPath(t)
	old := listInstalledShortcuts
	listInstalledShortcuts = func(context.Context) ([]string, error) { t.Fatal("config local discovered Shortcuts"); return nil, nil }
	t.Cleanup(func() { listInstalledShortcuts = old })
	for _, tc := range []struct {
		args    []string
		wantErr bool
	}{
		{[]string{"config", "set", "model", "local"}, false},
		{[]string{"config", "set", "bridge", "local", "some-bridge"}, true},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &localFixtureRunner{} })
		cmd.SetArgs(tc.args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if (err != nil) != tc.wantErr {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
	cfg, err := loadConfig()
	if err != nil || cfg.DefaultModel != "local" {
		t.Fatalf("%v %v", cfg, err)
	}
}
