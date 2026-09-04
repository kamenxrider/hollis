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
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
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

type imageRecordingRunner struct {
	calls      int
	model      runner.Model
	prompt     string
	imagePaths []string
}

func (r *imageRecordingRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	r.model = model
	r.prompt = prompt
	return prompt, model, nil
}

func (r *imageRecordingRunner) RunWithImages(_ context.Context, model runner.Model, prompt string, imagePaths []string) (string, runner.Model, error) {
	r.calls++
	r.model = model
	r.prompt = prompt
	r.imagePaths = append([]string(nil), imagePaths...)
	return "image fixture response", model, nil
}

func writeCLIImage(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("fixture image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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

func TestRespondRejectsOversizedArgumentAndStdinBeforeRunner(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		in   string
	}{
		{name: "argument", args: []string{"respond", strings.Repeat("x", 128<<10+1)}},
		{name: "stdin", args: []string{"respond"}, in: strings.Repeat("x", 128<<10+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recordingRunner{response: "must not run"}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs(tc.args)
			cmd.SetIn(strings.NewReader(tc.in))
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
			}
			if r.calls != 0 {
				t.Fatalf("runner calls=%d, want 0", r.calls)
			}
		})
	}
}

func TestCommandsRejectInvalidExplicitTimeouts(t *testing.T) {
	for _, args := range [][]string{
		{"respond", "--timeout", "0s", "hello"},
		{"respond", "--timeout", "121s", "hello"},
		{"chat", "--timeout", "-1s", "hello"},
		{"chat", "--timeout", "121s", "hello"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil || ExitCode(err) != 2 {
			t.Fatalf("args %v: err=%v exit=%d, want usage 2", args, err, ExitCode(err))
		}
	}
}

func TestJSONDeleteRequiresExplicitYesAndRenameRejectsBlankTitle(t *testing.T) {
	for _, args := range [][]string{
		{"chats", "delete", "some-id", "--json"},
		{"chats", "rename", "some-id", "   "},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil || ExitCode(err) != 2 {
			t.Fatalf("args %v: err=%v exit=%d, want usage 2", args, err, ExitCode(err))
		}
	}
}

func TestRespondJSONOutput(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "--model", "cloud", "hello world"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model_requested"] != "cloud" || got["response"] != "hello world" {
		t.Fatalf("unexpected JSON: %s", buf.String())
	}
}

func TestRespondJSONReportsModelUsed(t *testing.T) {
	stubConfigPath(t)
	got := respondJSON(t, "hello")
	if got["model_requested"] != "auto" {
		t.Fatalf("model_requested = %v, want auto", got["model_requested"])
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

func TestRespondImageDefaultsToCloudAndPassesPaths(t *testing.T) {
	stubConfigPath(t)
	image := writeCLIImage(t, "quiet fixture.png")
	r := &imageRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--json", "--image", image, "Describe it"})
	cmd.SetIn(&bytes.Buffer{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.model != runner.ModelCloud || r.prompt != "Describe it" || len(r.imagePaths) != 1 || r.imagePaths[0] != image {
		t.Fatalf("image runner call=%+v", r)
	}
	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["model_requested"] != "cloud" || body["model_used"] != "cloud" || body["response"] != "image fixture response" {
		t.Fatalf("unexpected JSON: %s", out.String())
	}
}

func TestRespondImageAllowsMeasuredFilesPerTier(t *testing.T) {
	first := writeCLIImage(t, "first.png")
	second := writeCLIImage(t, "second.jpg")
	for _, tc := range []struct {
		model string
		paths []string
	}{
		{model: "cloud", paths: []string{first, second}},
		{model: "cloud-pro", paths: []string{first, second}},
		{model: "chatgpt", paths: []string{first}},
	} {
		t.Run(tc.model, func(t *testing.T) {
			r := &imageRecordingRunner{}
			cmd := NewRootCmd(func() runner.Runner { return r })
			args := []string{"respond", "--model", tc.model}
			for _, path := range tc.paths {
				args = append(args, "--image", path)
			}
			args = append(args, "Describe them")
			cmd.SetArgs(args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if r.calls != 1 || len(r.imagePaths) != len(tc.paths) {
				t.Fatalf("image runner call=%+v", r)
			}
			for i, path := range tc.paths {
				if r.imagePaths[i] != path {
					t.Fatalf("image path %d=%q, want %q", i, r.imagePaths[i], path)
				}
			}
		})
	}
}

func TestRespondImageUsageErrorsDoNotCallRunner(t *testing.T) {
	oldInteractive := interactiveStdin
	interactiveStdin = func() bool { return false }
	t.Cleanup(func() { interactiveStdin = oldInteractive })

	one := writeCLIImage(t, "one.png")
	two := writeCLIImage(t, "two.jpeg")
	missing := filepath.Join(t.TempDir(), "missing.png")

	for _, tc := range []struct {
		name string
		args []string
		in   string
	}{
		{name: "explicit auto", args: []string{"respond", "--model", "auto", "--image", one, "Describe it"}},
		{name: "on-device", args: []string{"respond", "--model", "on-device", "--image", one, "Describe it"}},
		{name: "chatgpt multiple", args: []string{"respond", "--model", "chatgpt", "--image", one, "--image", two, "Compare"}},
		{name: "missing file", args: []string{"respond", "--model", "cloud", "--image", missing, "Describe it"}},
		{name: "piped stdin", args: []string{"respond", "--model", "cloud", "--image", one, "Describe it"}, in: "extra prompt"},
		{name: "no prompt argument", args: []string{"respond", "--model", "cloud", "--image", one, "--no-input"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &imageRecordingRunner{}
			cmd := NewRootCmd(func() runner.Runner { return r })
			cmd.SetArgs(tc.args)
			cmd.SetIn(strings.NewReader(tc.in))
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
			}
			if r.calls != 0 {
				t.Fatalf("runner calls=%d, want 0", r.calls)
			}
		})
	}
}

func TestRespondImageRejectsConfiguredAuto(t *testing.T) {
	stubConfigPath(t)
	if err := saveConfig(config{DefaultModel: string(runner.ModelAuto)}); err != nil {
		t.Fatal(err)
	}
	image := writeCLIImage(t, "one.png")
	r := &imageRecordingRunner{}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"respond", "--image", image, "Describe it"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || ExitCode(err) != 2 || r.calls != 0 {
		t.Fatalf("err=%v exit=%d calls=%d, want configured-auto usage error before runner", err, ExitCode(err), r.calls)
	}
}

func TestRespondDefaultsToAuto(t *testing.T) {
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"respond", "--json", "hello"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model_requested"] != "auto" {
		t.Fatalf("default model = %v, want auto", got["model_requested"])
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
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, buf.String())
	}
	if got["model_requested"] != "on-device" || got["response"] != "hello" {
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
	if got["model_requested"] != "cloud-pro" {
		t.Fatalf("configured default = %v, want cloud-pro", got["model_requested"])
	}
	// Positional prefix beats the config default.
	got = respondJSON(t, "model", "on-device", "hello")
	if got["model_requested"] != "on-device" {
		t.Fatalf("positional model = %v, want on-device", got["model_requested"])
	}
	// Explicit flag beats the config default.
	got = respondJSON(t, "--model", "chatgpt", "hello")
	if got["model_requested"] != "chatgpt" {
		t.Fatalf("flag model = %v, want chatgpt", got["model_requested"])
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
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
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
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	if strings.Contains(buf.String(), "cloud-pro") {
		t.Fatalf("unavailable pro surfaced: %s", buf.String())
	}
	for _, want := range []string{"\"cloud\"", "\"on-device\"", "\"chatgpt\"", "resolved_ref", "verified"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("models JSON missing %s: %s", want, buf.String())
		}
	}
}

func TestAutoNeverUsesUnverifiedCompiledCandidates(t *testing.T) {
	resolved := runner.ResolveBridges(nil, true, 27, nil)
	if err := checkModelAvailable(resolved, runner.ModelAuto); err == nil || ExitCode(err) != 2 {
		t.Fatalf("auto availability err=%v exit=%d, want usage 2", err, ExitCode(err))
	}
	r := runner.New()
	applyResolvedRefs(r, resolved)
	if len(r.BridgeRefs) != 0 {
		t.Fatalf("compiled candidates became runnable refs: %#v", r.BridgeRefs)
	}

	partial := runner.ResolveBridges([]string{"AFM Bridge - On-Device.signed"}, true, 27, nil)
	if err := checkModelAvailable(partial, runner.ModelAuto); err != nil {
		t.Fatalf("auto should remain usable through discovered on-device fallback: %v", err)
	}
	applyResolvedRefs(r, partial)
	if len(r.BridgeRefs) != 1 || r.BridgeRefs[runner.ModelOnDevice] == "" {
		t.Fatalf("runnable refs=%#v, want discovered on-device only", r.BridgeRefs)
	}
}

func TestDoctorJSONMacosAndStatus(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"doctor", "--json"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
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
	for _, key := range []string{"resolved_ref", "source", "status", "uuid", "name", "installed", "model", "verified"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("bridge entry missing %q: %+v", key, first)
		}
	}
	if first["status"] != "ok" || first["source"] != "shortcuts-list" {
		t.Fatalf("first bridge = %+v", first)
	}
}

func TestDoctorMissingTransportExits5(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	lookPath = func(path string) (string, error) {
		return "", fmt.Errorf("%s not found", path)
	}

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"doctor", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if ExitCode(err) != 5 || !ErrorReported(err) {
		t.Fatalf("exit=%d reported=%v err=%v output=%s", ExitCode(err), ErrorReported(err), err, out.String())
	}

	var got map[string]any
	if unmarshalErr := json.Unmarshal(out.Bytes(), &got); unmarshalErr != nil {
		t.Fatalf("invalid JSON: %v (%q)", unmarshalErr, out.String())
	}
	if got["shortcuts_cli"] != "ERROR not found at /usr/bin/shortcuts" {
		t.Fatalf("shortcuts_cli=%v", got["shortcuts_cli"])
	}
	errorBody, ok := got["error"].(map[string]any)
	if !ok || errorBody["code"] != "transport" || errorBody["exit_code"] != float64(5) {
		t.Fatalf("error=%#v", got["error"])
	}
}

func TestDoctorExitContracts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed []string
		listOK    bool
		major     int
		wantExit  int
	}{
		{"all verified on 27", allImportedNames(), true, 27, 0},
		{"missing supported bridge", []string{"AFM Bridge - Cloud.signed"}, true, 27, 3},
		{"discovery failure", nil, false, 27, 5},
		{"unknown operating system", allImportedNames(), true, 0, 10},
		{"pro informationally unsupported on 26", allImportedNames(), true, 26, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			stubResolution(t, tc.installed, tc.listOK, tc.major)
			cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
			cmd.SetArgs([]string{"doctor", "--json"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			err := cmd.Execute()
			if got := ExitCode(err); got != tc.wantExit {
				t.Fatalf("exit=%d want=%d err=%v output=%s", got, tc.wantExit, err, out.String())
			}
			if !json.Valid(out.Bytes()) {
				t.Fatalf("doctor output is not JSON: %q", out.String())
			}
		})
	}
}

func TestDoctorAgentErrorKeepsTopLevelErrorWhenResultsAreSelected(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, []string{"AFM Bridge - Cloud.signed"}, true, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"doctor", "--agent", "--select", "bridges"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if ExitCode(err) != 3 || !ErrorReported(err) {
		t.Fatalf("exit=%d reported=%v err=%v", ExitCode(err), ErrorReported(err), err)
	}
	var got map[string]any
	if json.Unmarshal(out.Bytes(), &got) != nil {
		t.Fatalf("invalid JSON: %q", out.String())
	}
	if got["meta"] == nil || got["error"] == nil {
		t.Fatalf("agent diagnostic lacks top-level meta/error: %#v", got)
	}
	results, ok := got["results"].(map[string]any)
	if !ok || len(results) != 1 || results["bridges"] == nil {
		t.Fatalf("selected results=%#v", got["results"])
	}
}

func TestDoctorDiscoveryFailureReportsSupportedBridgesUnverified(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, false, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"doctor", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if ExitCode(err) != 5 {
		t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, out.String())
	}
	var got map[string]any
	if json.Unmarshal(out.Bytes(), &got) != nil {
		t.Fatalf("invalid JSON: %q", out.String())
	}
	bridges, ok := got["bridges"].([]any)
	if !ok || len(bridges) != len(runner.Models) {
		t.Fatalf("bridges=%#v", got["bridges"])
	}
	for _, raw := range bridges {
		bridge, _ := raw.(map[string]any)
		if bridge["status"] != "unverified" {
			t.Fatalf("bridge status=%#v, want unverified", bridge)
		}
	}
}

func TestDoctorConfiguredUUIDIsUnverifiedState(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	if err := saveConfig(config{Bridges: map[string]string{"cloud": runner.BridgeUUIDCloud}}); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"doctor", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	if ExitCode(err) != 10 || !strings.Contains(out.String(), `"status":"unverified"`) {
		t.Fatalf("exit=%d err=%v output=%s", ExitCode(err), err, out.String())
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

func TestGeneratedHelpAndCompletionRejectUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{
		{"help", "respond", "extra"},
		{"completion", "zsh", "extra"},
		{"completion", "unknown", "extra"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.Execute()
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d, want usage 2", args, err, ExitCode(err))
		}
	}
}

func TestHelpDoesNotBypassGlobalFlagContracts(t *testing.T) {
	for _, args := range [][]string{
		{"--select", "response", "--help"},
		{"--agent", "--json=false", "--help"},
		{"--agent", "--no-input=false", "respond", "--help"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.Execute()
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d, want usage 2", args, err, ExitCode(err))
		}
	}

	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"--json", "--select", "command", "respond", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("valid selected JSON help: %v", err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), `"command":"hollis respond"`) {
		t.Fatalf("selected JSON help=%q", out.String())
	}
}

func TestHelpDoesNotBypassCommandValidation(t *testing.T) {
	for _, args := range [][]string{
		{"bogus", "--help"},
		{"--version", "extra", "--help"},
		{"chats", "bogus", "--help"},
		{"config", "bogus", "--help"},
		{"chats", "delete", "a", "b", "--help"},
		{"chats", "rename", "a", "", "--help"},
		{"chats", "search", "--limit", "0", "query", "--help"},
		{"chats", "search", "--model", "nope", "query", "--help"},
		{"config", "set", "model", "cloud", "extra", "--help"},
		{"config", "set", "bridge", "cloud", "--help"},
		{"config", "set", "nonsense", "value", "--help"},
		{"config", "set", "model", "nope", "--help"},
		{"respond", "--timeout", "0s", "--help"},
		{"respond", "--model", "nope", "--help"},
		{"chat", "--timeout", "0s", "--help"},
		{"chat", "--model", "nope", "--help"},
		{"serve", "--max-concurrency", "0", "--help"},
		{"serve", "--addr", "not-an-address", "--help"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.Execute()
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d, want usage 2", args, err, ExitCode(err))
		}
	}
}

func TestAgentContextSchemaPresent(t *testing.T) {
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"agent-context"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent-context: %v", err)
	}
	var got agentContext
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("agent-context JSON: %v (%q)", err, out.String())
	}
	if got.SchemaVersion != "2" || got.Contracts.ExitCodes["unexpected"] != 1 || got.Contracts.ExitCodes["config"] != 10 {
		t.Fatalf("agent contract header = %+v", got)
	}
	byPath := map[string]agentContextCommand{}
	var walk func(string, []agentContextCommand)
	walk = func(parent string, commands []agentContextCommand) {
		for _, command := range commands {
			path := strings.TrimSpace(parent + " " + command.Name)
			byPath[path] = command
			walk(path, command.Subcommands)
		}
	}
	walk("hollis", got.Commands)
	for _, path := range []string{
		"hollis agent-context", "hollis chat", "hollis chats", "hollis chats delete",
		"hollis chats list", "hollis chats rename", "hollis chats search", "hollis chats show",
		"hollis completion", "hollis config", "hollis config set", "hollis config show",
		"hollis doctor", "hollis help", "hollis models", "hollis respond", "hollis serve", "hollis version",
	} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("agent-context missing %s", path)
		}
	}
	respond := byPath["hollis respond"]
	counts := map[string]int{}
	for _, flag := range respond.Flags {
		counts[flag.Name]++
	}
	for _, inherited := range []string{"agent", "json", "no-input", "select"} {
		if counts[inherited] != 1 {
			t.Errorf("respond flag %s occurs %d times, want once: %+v", inherited, counts[inherited], respond.Flags)
		}
	}
	if len(respond.SideEffects) == 0 || strings.Join(respond.OutputModes, ",") != "human,json,agent" {
		t.Errorf("respond contract incomplete: %+v", respond)
	}
	if modes := strings.Join(byPath["hollis completion"].OutputModes, ","); modes != "human" {
		t.Errorf("completion output modes = %q", modes)
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
	oldLookPath := lookPath
	lookPath = func(path string) (string, error) { return path, nil }
	t.Cleanup(func() {
		listInstalledShortcuts = oldList
		macosMajorVersion = oldMajor
		macosVersion = oldVersion
		lookPath = oldLookPath
	})
}

func TestModelsCommandListsAllTiers(t *testing.T) {
	stubConfigPath(t)
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
	stubConfigPath(t)
	stubResolution(t, allImportedNames(), true, 27)
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"models", "--json"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	execErr := cmd.Execute()
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
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
