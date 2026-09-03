// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/kamenxrider/hollis/internal/cli"
	"github.com/kamenxrider/hollis/internal/runner"
)

type processRunner struct{}

func (processRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	used := model
	if model == runner.ModelAuto {
		used = runner.ModelOnDevice
	}
	return "fixture response for " + prompt, used, nil
}

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HOLLIS_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i + 1
			break
		}
	}
	cmd := cli.NewRootCmd(func() runner.Runner { return processRunner{} })
	cmd.SetArgs(os.Args[separator:])
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
	os.Exit(0)
}

func TestBuiltBinaryProcessContracts(t *testing.T) {
	repo := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "hollis")
	build := exec.Command("go", "build", "-o", bin, "./cmd/hollis")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	for _, tc := range []struct {
		name     string
		args     []string
		wantExit int
	}{
		{name: "help", args: []string{"--help"}, wantExit: 0},
		{name: "version", args: []string{"version"}, wantExit: 0},
		{name: "unknown flag", args: []string{"--definitely-unknown"}, wantExit: 2},
		{name: "unknown command", args: []string{"definitely-unknown"}, wantExit: 2},
		{name: "extra version argument", args: []string{"version", "extra"}, wantExit: 2},
		{name: "extra help path", args: []string{"help", "respond", "extra"}, wantExit: 2},
		{name: "extra completion argument", args: []string{"completion", "zsh", "extra"}, wantExit: 2},
		{name: "unknown completion arguments", args: []string{"completion", "unknown", "extra"}, wantExit: 2},
		{name: "select help bypass", args: []string{"--select", "response", "--help"}, wantExit: 2},
		{name: "agent contradiction help bypass", args: []string{"--agent", "--json=false", "--help"}, wantExit: 2},
		{name: "delete extras help bypass", args: []string{"chats", "delete", "a", "b", "--help"}, wantExit: 2},
		{name: "config extras help bypass", args: []string{"config", "set", "model", "cloud", "extra", "--help"}, wantExit: 2},
		{name: "invalid timeout help bypass", args: []string{"respond", "--timeout", "0s", "--help"}, wantExit: 2},
		{name: "remote bind requires auth", args: []string{"serve", "--addr", "0.0.0.0:0"}, wantExit: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runProcess(bin, tc.args, t.TempDir(), nil)
			if result.exit != tc.wantExit {
				t.Fatalf("exit=%d want=%d\nstdout=%s\nstderr=%s", result.exit, tc.wantExit, result.stdout, result.stderr)
			}
		})
	}
}

func TestBuiltBinaryStructuredErrorContracts(t *testing.T) {
	repo := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "hollis")
	build := exec.Command("go", "build", "-o", bin, "./cmd/hollis")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name      string
		args      []string
		wantAgent bool
	}{
		{"json argument error", []string{"--json", "version", "extra"}, false},
		{"select without json", []string{"--select", "response", "version"}, false},
		{"agent contradiction", []string{"--agent", "--json=false", "version"}, true},
		{"agent completion is rejected", []string{"--agent", "completion", "zsh"}, true},
		{"agent server is rejected", []string{"--agent", "serve"}, true},
		{"agent root requires command", []string{"--agent"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runProcess(bin, tc.args, t.TempDir(), nil)
			if result.exit != 2 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
			}
			if tc.name == "select without json" {
				if result.stdout != "" || !strings.Contains(result.stderr, "--select requires") {
					t.Fatalf("stdout=%q stderr=%q", result.stdout, result.stderr)
				}
				return
			}
			if result.stderr != "" {
				t.Fatalf("structured error leaked human stderr: %q", result.stderr)
			}
			var body map[string]any
			decodeObject(t, result.stdout, &body)
			errorBody, ok := body["error"].(map[string]any)
			if !ok || errorBody["exit_code"] != float64(2) {
				t.Fatalf("error body=%#v", body)
			}
			_, hasMeta := body["meta"]
			if hasMeta != tc.wantAgent {
				t.Fatalf("meta present=%v want=%v body=%#v", hasMeta, tc.wantAgent, body)
			}
		})
	}

	version := runProcess(bin, []string{"--agent", "version"}, t.TempDir(), nil)
	if version.exit != 0 || version.stderr != "" {
		t.Fatalf("agent version: exit=%d stdout=%s stderr=%s", version.exit, version.stdout, version.stderr)
	}
	var versionBody map[string]any
	decodeObject(t, version.stdout, &versionBody)
	results, ok := versionBody["results"].(map[string]any)
	if !ok || results["version"] != "0.2.0" || versionBody["meta"] == nil {
		t.Fatalf("agent version body=%#v", versionBody)
	}
	versionFlag := runProcess(bin, []string{"--agent", "--version"}, t.TempDir(), nil)
	if versionFlag.exit != 0 || versionFlag.stderr != "" {
		t.Fatalf("agent --version: exit=%d stdout=%s stderr=%s", versionFlag.exit, versionFlag.stdout, versionFlag.stderr)
	}
	var versionFlagBody map[string]any
	decodeObject(t, versionFlag.stdout, &versionFlagBody)
	results, ok = versionFlagBody["results"].(map[string]any)
	if !ok || results["version"] != "0.2.0" || versionFlagBody["meta"] == nil {
		t.Fatalf("agent --version body=%#v", versionFlagBody)
	}
	for _, args := range [][]string{{"--agent", "--help"}, {"--agent", "help"}} {
		help := runProcess(bin, args, t.TempDir(), nil)
		if help.exit != 0 || help.stderr != "" {
			t.Fatalf("agent help %v: exit=%d stdout=%s stderr=%s", args, help.exit, help.stdout, help.stderr)
		}
		var helpBody map[string]any
		decodeObject(t, help.stdout, &helpBody)
		if helpBody["meta"] == nil || helpBody["results"] == nil {
			t.Fatalf("agent help %v body=%#v", args, helpBody)
		}
	}
}

func TestCLIProcessJSONAndChatLifecycle(t *testing.T) {
	state := t.TempDir()

	respond := runHelper(t, state, "respond", "--json", "--model", "auto", "quiet marker")
	if respond.exit != 0 {
		t.Fatalf("respond: exit=%d stderr=%s", respond.exit, respond.stderr)
	}
	var response map[string]any
	decodeObject(t, respond.stdout, &response)
	if response["model_requested"] != "auto" || response["model_used"] != "on-device" {
		t.Fatalf("unexpected model metadata: %#v", response)
	}

	agent := runHelper(t, state, "--agent", "respond", "quiet agent marker")
	if agent.exit != 0 {
		t.Fatalf("agent respond: exit=%d stderr=%s", agent.exit, agent.stderr)
	}
	var agentResponse map[string]any
	decodeObject(t, agent.stdout, &agentResponse)
	meta, ok := agentResponse["meta"].(map[string]any)
	if !ok || meta["schema_version"] == nil {
		t.Fatalf("agent metadata missing: %#v", agentResponse)
	}
	if results, ok := agentResponse["results"].(map[string]any); !ok || results["response"] == nil {
		t.Fatalf("agent results missing response: %#v", agentResponse)
	}

	created := runHelper(t, state, "chat", "--json", "--model", "cloud-pro", "first quiet turn")
	if created.exit != 0 {
		t.Fatalf("chat create: exit=%d stderr=%s", created.exit, created.stderr)
	}
	var chat map[string]any
	decodeObject(t, created.stdout, &chat)
	id, _ := chat["conversation_id"].(string)
	if id == "" || chat["model_used"] != "cloud-pro" {
		t.Fatalf("unexpected chat object: %#v", chat)
	}

	continued := runHelper(t, state, "chat", "--json", "--continue", id, "second quiet turn")
	if continued.exit != 0 {
		t.Fatalf("chat continue: exit=%d stderr=%s", continued.exit, continued.stderr)
	}
	var continuedChat map[string]any
	decodeObject(t, continued.stdout, &continuedChat)
	if continuedChat["conversation_id"] != id || continuedChat["model_requested"] != "cloud-pro" || continuedChat["model_used"] != "cloud-pro" {
		t.Fatalf("unexpected continued chat object: %#v", continuedChat)
	}

	listed := runHelper(t, state, "chats", "list", "--json")
	if listed.exit != 0 {
		t.Fatalf("chats list: exit=%d stderr=%s", listed.exit, listed.stderr)
	}
	var chats []map[string]any
	decodeArray(t, listed.stdout, &chats)
	if len(chats) != 1 || chats[0]["id"] != id || chats[0]["messages"] != float64(4) {
		t.Fatalf("unexpected chats list: %#v", chats)
	}

	searched := runHelper(t, state, "chats", "search", "--json", "second quiet turn")
	if searched.exit != 0 {
		t.Fatalf("chats search: exit=%d stderr=%s", searched.exit, searched.stderr)
	}
	var matches []map[string]any
	decodeArray(t, searched.stdout, &matches)
	if len(matches) != 1 || matches[0]["id"] != id {
		t.Fatalf("unexpected chats search: %#v", matches)
	}

	renamed := runHelper(t, state, "chats", "rename", "--json", id, "renamed quiet chat")
	if renamed.exit != 0 || !json.Valid([]byte(renamed.stdout)) {
		t.Fatalf("rename: exit=%d stdout=%s stderr=%s", renamed.exit, renamed.stdout, renamed.stderr)
	}

	shown := runHelper(t, state, "chats", "show", "--json", id)
	if shown.exit != 0 || !strings.Contains(shown.stdout, "renamed quiet chat") {
		t.Fatalf("show: exit=%d stdout=%s stderr=%s", shown.exit, shown.stdout, shown.stderr)
	}

	deleted := runHelper(t, state, "chats", "delete", "--json", "--yes", id)
	if deleted.exit != 0 || !json.Valid([]byte(deleted.stdout)) {
		t.Fatalf("delete: exit=%d stdout=%s stderr=%s", deleted.exit, deleted.stdout, deleted.stderr)
	}
}

func TestCLIProcessReadOnlyAndConfigCommands(t *testing.T) {
	state := t.TempDir()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "agent context", args: []string{"agent-context"}},
		{name: "models", args: []string{"models", "--json"}},
		{name: "doctor", args: []string{"doctor", "--json"}},
		{name: "config show", args: []string{"config", "show", "--json"}},
		{name: "empty chats", args: []string{"chats", "list", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runHelper(t, state, tc.args...)
			if tc.name == "models" && result.exit == 5 {
				// Model discovery is intentionally host-dependent: a machine
				// without a working `shortcuts list` reports a transport error,
				// but must not invoke a provider just to list.
				return
			}
			if tc.name == "doctor" && (result.exit == 3 || result.exit == 5 || result.exit == 10) {
				// Doctor is intentionally host-dependent. A clean CI Mac can
				// list Shortcuts but have no Hollis bridges (3), discovery can
				// fail (5), or OS/config state can be unverified (10). Every
				// diagnostic path must still return structured JSON and must
				// never invoke a model provider.
				var report map[string]any
				decodeObject(t, result.stdout, &report)
				errorBody, ok := report["error"].(map[string]any)
				reportedExit, exitOK := errorBody["exit_code"].(float64)
				if !ok || !exitOK || int(reportedExit) != result.exit {
					t.Fatalf("doctor error body does not match exit %d: %#v", result.exit, report)
				}
				return
			}
			if result.exit != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
			}
			switch tc.name {
			case "empty chats", "models":
				var rows []map[string]any
				decodeArray(t, result.stdout, &rows)
				if tc.name == "empty chats" && len(rows) != 0 {
					t.Fatalf("empty chat list = %#v", rows)
				}
			case "agent context", "doctor", "config show":
				var object map[string]any
				decodeObject(t, result.stdout, &object)
			}
		})
	}

	setModel := runHelper(t, state, "config", "set", "model", "auto", "--json")
	if setModel.exit != 0 {
		t.Fatalf("config set model: exit=%d stderr=%s", setModel.exit, setModel.stderr)
	}
	var modelResult map[string]any
	decodeObject(t, setModel.stdout, &modelResult)
	if modelResult["ok"] != true || modelResult["key"] != "default_model" || modelResult["value"] != "auto" {
		t.Fatalf("unexpected config model result: %#v", modelResult)
	}

	setBridge := runHelper(t, state, "config", "set", "bridge", "cloud", "fixture bridge", "--json")
	if setBridge.exit != 0 {
		t.Fatalf("config set bridge: exit=%d stderr=%s", setBridge.exit, setBridge.stderr)
	}
	var bridgeResult map[string]any
	decodeObject(t, setBridge.stdout, &bridgeResult)
	if bridgeResult["ok"] != true || bridgeResult["tier"] != "cloud" || bridgeResult["configured"] != true {
		t.Fatalf("unexpected config bridge result: %#v", bridgeResult)
	}

	show := runHelper(t, state, "config", "show", "--json")
	if show.exit != 0 {
		t.Fatalf("config show after set: exit=%d stderr=%s", show.exit, show.stderr)
	}
	var shown map[string]any
	decodeObject(t, show.stdout, &shown)
	if shown["default_model"] != "auto" {
		t.Fatalf("config default_model = %#v", shown["default_model"])
	}
	bridges, ok := shown["bridges"].(map[string]any)
	if !ok || bridges["cloud"] != "fixture bridge" {
		t.Fatalf("config bridges = %#v", shown["bridges"])
	}
}

func TestConfigUpdatesSerializeAcrossProcesses(t *testing.T) {
	state := t.TempDir()
	tiers := []string{"cloud", "cloud-pro", "on-device", "chatgpt"}
	results := make(chan processResult, len(tiers))
	var wg sync.WaitGroup
	for _, tier := range tiers {
		tier := tier
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- runHelper(t, state, "config", "set", "bridge", tier, "fixture-"+tier, "--json")
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.exit != 0 {
			t.Fatalf("concurrent config writer: exit=%d stderr=%s", result.exit, result.stderr)
		}
	}

	show := runHelper(t, state, "config", "show", "--json")
	if show.exit != 0 {
		t.Fatalf("config show: exit=%d stderr=%s", show.exit, show.stderr)
	}
	var shown map[string]any
	decodeObject(t, show.stdout, &shown)
	bridges, ok := shown["bridges"].(map[string]any)
	if !ok || len(bridges) != len(tiers) {
		t.Fatalf("concurrent config preserved %d/%d bridge writes: %#v", len(bridges), len(tiers), shown)
	}
	for _, tier := range tiers {
		if bridges[tier] != "fixture-"+tier {
			t.Errorf("bridge %s=%#v", tier, bridges[tier])
		}
	}
}

type processResult struct {
	exit           int
	stdout, stderr string
}

func runHelper(t *testing.T, state string, args ...string) processResult {
	t.Helper()
	cmdArgs := []string{"-test.run=^TestCLIHelperProcess$", "--"}
	cmdArgs = append(cmdArgs, args...)
	return runProcess(os.Args[0], cmdArgs, state, []string{"GO_WANT_HOLLIS_HELPER=1"})
}

func runProcess(bin string, args []string, state string, extraEnv []string) processResult {
	cmd := exec.Command(bin, args...)
	cmd.Env = isolatedHollisEnv(state, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return processResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func isolatedHollisEnv(state string, extraEnv ...string) []string {
	environment := make([]string, 0, len(os.Environ())+len(extraEnv)+1)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "HOLLIS_API_TOKEN=") || strings.HasPrefix(value, "HOLLIS_STATE_DIR=") {
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment, extraEnv...)
	return append(environment, "HOLLIS_STATE_DIR="+state)
}

func decodeObject(t *testing.T, raw string, dst *map[string]any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(dst); err != nil {
		t.Fatalf("decode JSON %q: %v", raw, err)
	}
}

func decodeArray(t *testing.T, raw string, dst *[]map[string]any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(dst); err != nil {
		t.Fatalf("decode JSON array %q: %v", raw, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
