// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

// stubConfigPath points the config loader at a fresh temp dir for the
// duration of the test, so tests never read or write the real config.
func stubConfigPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := configPath
	configPath = func() (string, error) {
		return filepath.Join(dir, "config.json"), nil
	}
	t.Cleanup(func() { configPath = old })
}

// respondJSON runs `respond --json args...` and returns the parsed JSON.
func respondJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs(append([]string{"respond", "--json"}, args...))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	return got
}

// fakeRunner returns canned responses/errors without touching the transport.
type fakeRunner struct {
	err error
}

func (f *fakeRunner) Run(_ context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	if f.err != nil {
		return "", m, f.err
	}
	return prompt, m, nil
}

func TestRespondEmptyPromptExitsUsage2(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "   "})
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want error for empty prompt")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestRespondJSONOutput(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "--model", "cloud", "hello world"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	// printJSONFiltered writes to os.Stdout; swap it for a pipe.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model"] != "cloud" || got["response"] != "hello world" {
		t.Fatalf("unexpected JSON: %s", buf.String())
	}
}

func TestRespondJSONReportsModelUsed(t *testing.T) {
	stubConfigPath(t)
	got := respondJSON(t, "hello")
	if got["model"] != "auto" {
		t.Fatalf("model = %v, want the requested tier (auto)", got["model"])
	}
	if _, ok := got["model_used"]; !ok {
		t.Fatal("model_used missing: auto must report which tier answered")
	}
}

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		kind runner.Kind
		want int
	}{
		{runner.KindEmptyPrompt, 2},
		{runner.KindUsage, 2},
		{runner.KindShortcutMissing, 3},
		{runner.KindNoOutput, 5},
		{runner.KindSIGABRT, 5},
		{runner.KindTransport, 5},
		{runner.KindTimeout, 7},
	}
	for _, tc := range cases {
		err := toCLIError(&runner.Error{Kind: tc.kind, ExitCode: -1, Err: errors.New("x")})
		if got := ExitCode(err); got != tc.want {
			t.Fatalf("kind %s: exit = %d, want %d", tc.kind, got, tc.want)
		}
	}
}

func TestUnknownModelFlagExitsUsage(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--model", "nope", "hello"})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestRespondNewModelsAccepted(t *testing.T) {
	for _, model := range []string{"on-device", "chatgpt"} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs([]string{"respond", "--model", model, "hello"})
		cmd.SetOut(&bytes.Buffer{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("respond --model %s: %v", model, err)
		}
	}
}

func TestRespondDefaultsToAuto(t *testing.T) {
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "hello"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model"] != "auto" {
		t.Fatalf("default model = %v, want auto", got["model"])
	}
}

func TestSplitModelArgs(t *testing.T) {
	tier, rest, has := splitModelArgs([]string{"model", "cloud-pro", "Draft", "a", "reply"})
	if !has || tier != "cloud-pro" || strings.Join(rest, " ") != "Draft a reply" {
		t.Fatalf("splitModelArgs = (%q, %v, %v)", tier, rest, has)
	}
	// "model" followed by a non-tier is a literal prompt.
	tier, rest, has = splitModelArgs([]string{"model", "not-a-tier", "hello"})
	if has || tier != "" || strings.Join(rest, " ") != "model not-a-tier hello" {
		t.Fatalf("splitModelArgs literal-prompt case = (%q, %v, %v)", tier, rest, has)
	}
	// No prefix at all.
	tier, rest, has = splitModelArgs([]string{"just", "a", "prompt"})
	if has || tier != "" || strings.Join(rest, " ") != "just a prompt" {
		t.Fatalf("splitModelArgs no-prefix case = (%q, %v, %v)", tier, rest, has)
	}
}

func TestRespondPositionalModelSelectsTier(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "model", "on-device", "hello"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model"] != "on-device" || got["response"] != "hello" {
		t.Fatalf("unexpected JSON: %s", buf.String())
	}
}

func TestConfigSetChangesRespondDefault(t *testing.T) {
	stubConfigPath(t)
	// `config set model <tier>` resolves bridges directly rather than through
	// the runner factory, so without this stub it reads the real sw_vers and
	// the real `shortcuts list`. On any host below macOS 27 the Cloud Pro gate
	// then refuses the write and the test fails — which is why it passed only
	// on the development Mac and was red on GitHub's macos-latest.
	stubResolution(t, allImportedNames(), true, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"config", "set", "model", "cloud-pro"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}
	got := respondJSON(t, "hello")
	if got["model"] != "cloud-pro" {
		t.Fatalf("configured default = %v, want cloud-pro", got["model"])
	}
	// Positional prefix beats the config default.
	got = respondJSON(t, "model", "on-device", "hello")
	if got["model"] != "on-device" {
		t.Fatalf("positional model = %v, want on-device", got["model"])
	}
	// Explicit flag beats the config default.
	got = respondJSON(t, "--model", "chatgpt", "hello")
	if got["model"] != "chatgpt" {
		t.Fatalf("flag model = %v, want chatgpt", got["model"])
	}
}

func TestConfigSetRejectsUnknownModel(t *testing.T) {
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"config", "set", "model", "nope"})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestRespondNoInputFailsFastOnTerminalStdin(t *testing.T) {
	stubConfigPath(t)
	old := interactiveStdin
	interactiveStdin = func() bool { return true }
	t.Cleanup(func() { interactiveStdin = old })

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--no-input"})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want fail-fast error, got success")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "no prompt provided") {
		t.Fatalf("error should tell the user what to do: %v", err)
	}
}

func TestChatNoInputDoesNotEnterREPL(t *testing.T) {
	stubConfigPath(t)
	old := interactiveStdin
	interactiveStdin = func() bool { return true }
	t.Cleanup(func() { interactiveStdin = old })
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return openTempStore(t), nil }
	t.Cleanup(func() { openStore = oldOpen })

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"chat", "--no-input"})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want fail-fast error, got success (REPL or hang)")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "no prompt provided") {
		t.Fatalf("error should tell the user what to do: %v", err)
	}
}

func TestBridgeCheckMarshalsRealObjects(t *testing.T) {
	// Regression guard: unexported fields once made
	// `doctor --json` emit "bridges":[{},{},{},{}].
	b, err := json.Marshal(bridgeCheck{Model: "cloud", UUID: "X", Name: "n", Installed: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"model":"cloud"`, `"uuid":"X"`, `"installed":true`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("bridgeCheck JSON missing %s: %s", want, b)
		}
	}
}

func TestChatsListShowsModelColumn(t *testing.T) {
	st := openTempStore(t)
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = oldOpen })
	st.CreateConversation("cloud-pro", "t1")
	st.CreateConversation("on-device", "t2")

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"chats", "list"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("chats list: %v", err)
	}
	for _, want := range []string{"MODEL", "cloud-pro", "on-device"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("chats list output missing %q: %q", want, out.String())
		}
	}
}

func TestChatsSearchHumanOutput(t *testing.T) {
	st := openTempStore(t)
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = oldOpen })
	conv, err := st.CreateConversation("cloud", "orbit chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "codeword VANTA-ORBIT-7319 stands"); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"chats", "search", "VANTA-ORBIT-7319"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("chats search: %v", err)
	}
	for _, want := range []string{"TITLE / SNIPPET", conv.ID, "cloud", "VANTA-ORBIT-7319"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("chats search output missing %q: %q", want, out.String())
		}
	}
}

func TestChatsSearchEmptyQueryExit2(t *testing.T) {
	st := openTempStore(t)
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = oldOpen })

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"chats", "search", "   "})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestChatsSearchNoHitsExit3(t *testing.T) {
	// Each Execute opens and closes its own store, so hand out a fresh
	// empty one per invocation instead of sharing one instance.
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return openTempStore(t), nil }
	t.Cleanup(func() { openStore = oldOpen })

	for _, query := range [][]string{
		{"chats", "search", "nothing-matches-this"},
		{"chats", "search", "--json", "nothing-matches-this"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(query)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.Execute()
		if got := ExitCode(err); got != 3 {
			t.Fatalf("query %v: exit code = %d, want 3", query, got)
		}
	}
}

func TestChatsSearchJSONShape(t *testing.T) {
	st := openTempStore(t)
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = oldOpen })
	conv, err := st.CreateConversation("cloud-pro", "json shape chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "heating poll summary q"); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"chats", "search", "--json", "heating poll"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if len(got) != 1 {
		t.Fatalf("JSON matches = %d, want 1", len(got))
	}
	row := got[0]
	if row["id"] != conv.ID || row["model"] != "cloud-pro" {
		t.Fatalf("row = %+v", row)
	}
	hits, ok := row["hits"].([]any)
	if !ok || len(hits) != 1 {
		t.Fatalf("hits = %+v, want one entry", row["hits"])
	}
	hit, _ := hits[0].(map[string]any)
	if hit["seq"] != float64(0) || hit["role"] != "user" {
		t.Fatalf("hit = %+v", hit)
	}
	if s, _ := hit["snippet"].(string); !strings.Contains(s, "heating poll") {
		t.Fatalf("snippet = %q", s)
	}
}

func TestChatsSearchValidatesFlags(t *testing.T) {
	st := openTempStore(t)
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = oldOpen })

	for _, args := range [][]string{
		{"chats", "search", "--model", "nope", "x"},
		{"chats", "search", "--limit", "0", "x"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.Execute()
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args %v: exit code = %d, want 2", args, got)
		}
	}
}

func TestChatEmptyPromptLeavesNoConversation(t *testing.T) {
	// Regression guard: the conversation used to be created before the
	// prompt was validated, leaving 0-message rows.
	stubConfigPath(t)
	old := interactiveStdin
	interactiveStdin = func() bool { return false }
	t.Cleanup(func() { interactiveStdin = old })
	st := openTempStore(t)
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = oldOpen })

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"chat", "--json"})
	cmd.SetIn(&bytes.Buffer{}) // empty piped stdin
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	convs, err := st.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 0 {
		t.Fatalf("empty prompt left %d conversation(s) behind, want 0", len(convs))
	}
}

func TestRespondUnavailableModelExits2(t *testing.T) {
	// cloud-pro without a usable bridge
	// is a clean usage error, not a missing-shortcut transport failure.
	// The md's 27 test points Pro at a fake name in config; the real
	// runner is required so the gate engages, the listing is stubbed.
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c.Bridges = map[string]string{"cloud-pro": "Fake Pro Bridge"}
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd(func() runner.Runner { return runner.New() })
	cmd.SetArgs([]string{"respond", "--model", "cloud-pro", "Reply with OK"})
	cmd.SetOut(&bytes.Buffer{})
	err = cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	for _, want := range []string{"cloud-pro is not available", "Cloud Pro requires macOS 27"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestRespondAvailableModelStillRuns(t *testing.T) {
	// The same gate must not break available tiers (the fake runner runs;
	// the gate is skipped for fakes, mirroring prod where the transport is
	// real and resolves).
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--model", "cloud", "hello"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("respond with fake runner: %v", err)
	}
}

func TestConfigSetModelUnavailableExits2(t *testing.T) {
	stubConfigPath(t)
	// A 26 machine imports the 26 profile: three bridges, no Pro.
	names := []string{"AFM Bridge - Cloud.signed", "AFM Bridge - On-Device.signed", "AFM Bridge - ChatGPT.signed"}
	stubResolution(t, names, true, 26)
	cmd := NewRootCmd(func() runner.Runner { return runner.New() })
	cmd.SetArgs([]string{"config", "set", "model", "cloud-pro"})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "cloud-pro is not available") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigSetBridgePersistsAndClears(t *testing.T) {
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"config", "set", "bridge", "cloud", "AFM Bridge - Custom"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set bridge: %v", err)
	}
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Bridges["cloud"] != "AFM Bridge - Custom" {
		t.Fatalf("bridges = %+v", c.Bridges)
	}
	// Empty value clears the override.
	cmd = NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"config", "set", "bridge", "cloud", ""})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set bridge clear: %v", err)
	}
	c, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Bridges["cloud"]; ok {
		t.Fatalf("override survived clear: %+v", c.Bridges)
	}
	// auto is not a bridge tier.
	cmd = NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"config", "set", "bridge", "auto", "x"})
	err = cmd.Execute()
	if got := ExitCode(err); got != 2 {
		t.Fatalf("bridge auto: exit code = %d, want 2", got)
	}
}

func TestModelsOmitUnavailablePro(t *testing.T) {
	// the catalog is what resolves.
	// A fake Pro config name removes cloud-pro from models without
	// touching the other tiers.
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c.Bridges = map[string]string{"cloud-pro": "Fake Pro Bridge"}
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"models", "--json"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	var buf bytes.Buffer
	oldOut := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = oldOut
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "cloud-pro") {
		t.Fatalf("unavailable pro surfaced: %s", buf.String())
	}
	for _, want := range []string{"\"cloud\"", "\"on-device\"", "\"chatgpt\"", "resolved_ref"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("models JSON missing %s: %s", want, buf.String())
		}
	}
}

func TestDoctorJSONMacosAndStatus(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"doctor", "--json"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	var buf bytes.Buffer
	oldOut := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = oldOut
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON: %v (%q)", err, buf.String())
	}
	if got["macos"] != "27.0" {
		t.Fatalf("macos = %v", got["macos"])
	}
	bridges, ok := got["bridges"].([]any)
	if !ok || len(bridges) != 4 {
		t.Fatalf("bridges = %+v", got["bridges"])
	}
	first, _ := bridges[0].(map[string]any)
	for _, key := range []string{"resolved_ref", "source", "status", "uuid", "name", "installed", "model"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("bridge entry missing %q: %+v", key, first)
		}
	}
	if first["status"] != "ok" || first["source"] != "shortcuts-list" {
		t.Fatalf("first bridge = %+v", first)
	}
}

func TestUnknownSubcommandExits2(t *testing.T) {
	// results review blocker 9: unknown subcommands must be a stable usage
	// error for agents, not parent help with exit 0.
	for _, args := range [][]string{
		{"bogus"},
		{"chats", "bogus"},
		{"config", "bogus"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.Execute()
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args %v: exit code = %d, want 2", args, got)
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("args %v: error = %v", args, err)
		}
	}
}

func TestAgentContextSchemaPresent(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"agent-context"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent-context: %v", err)
	}
}

// allImportedNames returns the shortcut names the measured 27 machine
// shows after the standard signed import (make-bridge.py + sign).
func allImportedNames() []string {
	return []string{
		"AFM Bridge - Cloud.signed",
		"AFM Bridge - Cloud Pro.signed",
		"AFM Bridge - On-Device.signed",
		"AFM Bridge - ChatGPT.signed",
	}
}

// stubResolution points bridge resolution at an in-memory machine: the
// given installed names and macOS major. Restores the package vars after.
func stubResolution(t *testing.T, installed []string, listOK bool, major int) {
	t.Helper()
	oldList := listInstalledShortcuts
	listInstalledShortcuts = func(_ context.Context) ([]string, error) {
		if !listOK {
			return nil, errors.New("shortcuts list failed")
		}
		return installed, nil
	}
	oldMajor := macosMajorVersion
	macosMajorVersion = func() int { return major }
	oldVersion := macosVersion
	macosVersion = func() string { return fmt.Sprintf("%d.0", major) }
	t.Cleanup(func() {
		listInstalledShortcuts = oldList
		macosMajorVersion = oldMajor
		macosVersion = oldVersion
	})
}

func TestModelsCommandListsAllTiers(t *testing.T) {
	stubResolution(t, allImportedNames(), true, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"models"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("models: %v", err)
	}
	for _, tier := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		if !bytes.Contains(out.Bytes(), []byte(tier)) {
			t.Fatalf("models output missing tier %q: %q", tier, out.String())
		}
	}
	// Internal Shortcuts jargon stays out of the human surface; it lives
	// in --json and the README.
	if bytes.Contains(out.Bytes(), []byte("WFLLMModel")) {
		t.Fatalf("human output must not leak internal parameter names: %q", out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("pcc-")) || bytes.Contains(out.Bytes(), []byte("gpt-")) {
		t.Fatalf("models output must not contain fabricated backend model IDs: %q", out.String())
	}
}

func TestModelsCommandJSONShape(t *testing.T) {
	stubResolution(t, allImportedNames(), true, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"models", "--json"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	w.Close()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if len(got) != 5 {
		t.Fatalf("models JSON rows = %d, want 5 (auto + 4 tiers)", len(got))
	}
	// auto leads the list: selectable, but a strategy without a bridge —
	// the same shape GET /v1/models reports.
	if got[0]["model"] != "auto" {
		t.Fatalf("first row = %v, want auto", got[0])
	}
	for _, row := range got[1:] {
		for _, field := range []string{"model", "wfllm_model", "apple_model", "resolved_ref", "source"} {
			if _, ok := row[field]; !ok {
				t.Fatalf("models JSON row missing %q: %v", field, row)
			}
		}
	}
}

func TestVersionFlag(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"--version"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("hollis")) {
		t.Fatalf("version output missing name: %q", out.String())
	}
}
