// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
)

func clearDefaultEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HOLLIS_MODEL", "HOLLIS_TIMEOUT"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func runDefaultsCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := executeCommand(cmd, flags)
	return out.String(), err
}

func TestConfigGetUnsetAndTimeout(t *testing.T) {
	stubConfigPath(t)
	initial := config{DefaultModel: "local", Timeout: "45s", Bridges: map[string]string{"cloud": "Cloud", "chatgpt": "Chat"}, ImageBridge: "General", ImageBridges: map[string]string{"sketch": "Sketch"}}
	for _, tc := range []struct {
		selector []string
		want     string
	}{
		{[]string{"model"}, "local"}, {[]string{"timeout"}, "45s"},
		{[]string{"bridge", "cloud"}, "Cloud"}, {[]string{"image-bridge"}, "General"}, {[]string{"image-bridge", "sketch"}, "Sketch"},
	} {
		t.Run(strings.Join(tc.selector, "/"), func(t *testing.T) {
			if err := saveConfig(initial); err != nil {
				t.Fatal(err)
			}
			out, err := runDefaultsCLI(t, append([]string{"config", "get"}, tc.selector...)...)
			if err != nil || out != tc.want+"\n" {
				t.Fatalf("get=%q err=%v", out, err)
			}
			out, err = runDefaultsCLI(t, append(append([]string{"config", "unset"}, tc.selector...), "--agent", "--select", "key,configured")...)
			if err != nil || !strings.Contains(out, `"configured":false`) || !strings.Contains(out, `"meta"`) {
				t.Fatalf("unset=%q err=%v", out, err)
			}
			out, err = runDefaultsCLI(t, append([]string{"config", "get"}, tc.selector...)...)
			if err != nil || out != "\n" {
				t.Fatalf("get after unset=%q err=%v", out, err)
			}
			after, err := loadConfig()
			if err != nil || after.Bridges["chatgpt"] != "Chat" {
				t.Fatalf("unrelated setting lost: %+v %v", after, err)
			}
			out, err = runDefaultsCLI(t, append([]string{"config", "unset"}, tc.selector...)...)
			if err != nil {
				t.Fatalf("unset not idempotent: %q %v", out, err)
			}
		})
	}
	if _, err := runDefaultsCLI(t, "config", "set", "timeout", "1m"); err != nil {
		t.Fatal(err)
	}
	c, _ := loadConfig()
	if c.Timeout != "1m0s" {
		t.Fatalf("timeout=%q", c.Timeout)
	}
	before, _ := os.ReadFile(mustConfigPath(t))
	for _, invalid := range []string{"", "0s", "-1s", "121s", "forever"} {
		_, err := runDefaultsCLI(t, "config", "set", "timeout", invalid)
		if ExitCode(err) != 2 {
			t.Fatalf("%q: %v", invalid, err)
		}
		after, _ := os.ReadFile(mustConfigPath(t))
		if !bytes.Equal(before, after) {
			t.Fatal("invalid set changed config")
		}
	}
}

func mustConfigPath(t *testing.T) string {
	t.Helper()
	p, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigGetDoesNotCreateOrChmod(t *testing.T) {
	stubConfigPath(t)
	p := mustConfigPath(t)
	out, err := runDefaultsCLI(t, "config", "get", "model", "--json")
	if err != nil || !strings.Contains(out, `"configured":false`) {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("get created config")
	}
	if err := os.WriteFile(p, []byte(`{"default_model":"local"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runDefaultsCLI(t, "config", "get", "model"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatal("get changed mode")
	}
	for _, args := range [][]string{{"get", "unknown"}, {"unset", "bridge", "auto"}, {"get", "image-bridge", "unknown"}, {"unset", "model", "extra"}} {
		if _, err := runDefaultsCLI(t, append([]string{"config"}, args...)...); ExitCode(err) != 2 {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestModelAndTimeoutPrecedence(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	if err := saveConfig(config{DefaultModel: "cloud-pro", Timeout: "45s"}); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("model", "auto", "")
	cmd.Flags().Duration("timeout", 30*time.Second, "")
	check := func(wantModel runner.Model, wantTimeout time.Duration) {
		t.Helper()
		m, err := effectiveModel(cmd, "on-device", "", false)
		d, terr := effectiveTimeout(cmd, 80*time.Second, runner.DefaultTimeout)
		if err != nil || terr != nil || m != wantModel || d != wantTimeout {
			t.Fatalf("model=%s timeout=%s errors=%v,%v", m, d, err, terr)
		}
	}
	check(runner.ModelCloudPro, 45*time.Second)
	t.Setenv("HOLLIS_MODEL", "cloud")
	t.Setenv("HOLLIS_TIMEOUT", "60s")
	check(runner.ModelCloud, time.Minute)
	if err := cmd.Flags().Set("model", "on-device"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("timeout", "80s"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOLLIS_MODEL", "invalid")
	t.Setenv("HOLLIS_TIMEOUT", "")
	check(runner.ModelOnDevice, 80*time.Second)
	m, err := effectiveModel(cmd, "on-device", "local", true)
	if err != nil || m != runner.ModelLocal {
		t.Fatalf("positional model=%s %v", m, err)
	}
}

func TestInvalidEnvironmentStopsBeforeInference(t *testing.T) {
	for _, name := range []string{"HOLLIS_MODEL", "HOLLIS_TIMEOUT"} {
		for _, value := range []string{"", "invalid"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				stubConfigPath(t)
				clearDefaultEnvironment(t)
				t.Setenv(name, value)
				r := &recordingRunner{response: "fixture"}
				cmd := NewRootCmd(func() runner.Runner { return r })
				cmd.SetArgs([]string{"respond", "--no-input", "hello"})
				cmd.SetOut(&bytes.Buffer{})
				err := cmd.Execute()
				if ExitCode(err) != 2 || r.calls != 0 {
					t.Fatalf("err=%v calls=%d", err, r.calls)
				}
			})
		}
	}
}

func TestImageDefaultsKeepExplicitAutoRefused(t *testing.T) {
	for _, source := range []string{"saved", "environment", "flag", "positional"} {
		t.Run(source, func(t *testing.T) {
			stubConfigPath(t)
			clearDefaultEnvironment(t)
			if err := saveConfig(config{DefaultModel: "auto"}); err != nil {
				t.Fatal(err)
			}
			r := &imageRecordingRunner{}
			cmd := NewRootCmd(func() runner.Runner { return r })
			args := []string{"respond", "--image", writeCLIImage(t, "input.png"), "--no-input"}
			switch source {
			case "environment":
				t.Setenv("HOLLIS_MODEL", "auto")
			case "flag":
				args = append(args, "--model", "auto")
			case "positional":
				args = append(args, "model", "auto")
			}
			cmd.SetArgs(append(args, "describe"))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(""))
			err := cmd.Execute()
			if source == "saved" {
				if err != nil || r.model != runner.ModelCloud || r.calls != 1 {
					t.Fatalf("saved auto: %v %+v", err, r)
				}
			} else if ExitCode(err) != 2 || r.calls != 0 {
				t.Fatalf("explicit auto: %v %+v", err, r)
			}
		})
	}
}

func TestTimeoutDefaultsAndSavedInvalidDuration(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	cmd := &cobra.Command{}
	cmd.Flags().Duration("timeout", 30*time.Second, "")
	for _, builtIn := range []time.Duration{30 * time.Second, 120 * time.Second} {
		got, err := effectiveTimeout(cmd, 30*time.Second, builtIn)
		if err != nil || got != builtIn {
			t.Fatalf("default=%s %v", got, err)
		}
	}
	if err := os.WriteFile(mustConfigPath(t), []byte(`{"timeout":"121s"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := effectiveTimeout(cmd, 30*time.Second, runner.DefaultTimeout); ExitCode(err) != 10 {
		t.Fatalf("saved invalid timeout: %v", err)
	}
}

type deadlineRunner struct {
	remaining time.Duration
	requested runner.Model
}

func (r *deadlineRunner) Run(ctx context.Context, m runner.Model, _ string) (string, runner.Model, error) {
	deadline, _ := ctx.Deadline()
	r.remaining, r.requested = time.Until(deadline), m
	return "fixture", m, nil
}

func TestTimeoutReachesRespondChatAndImage(t *testing.T) {
	for _, command := range []string{"respond", "chat", "image"} {
		t.Run(command, func(t *testing.T) {
			stubConfigPath(t)
			clearDefaultEnvironment(t)
			t.Setenv("HOLLIS_TIMEOUT", "47s")
			t.Setenv("HOLLIS_STATE_DIR", t.TempDir())
			g := newFakeImageGenerator(t)
			r := &deadlineRunner{}
			cmd, flags := newRootCmdWithImageGenerator(func() runner.Runner { return r }, g)
			args := []string{command, "--no-input", "hello"}
			if command == "image" {
				args = []string{"image", "generate", "--bridge", "Fixture", "--output", filepath.Join(t.TempDir(), "image.png"), "hello"}
			}
			cmd.SetArgs(args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(""))
			if err := executeCommand(cmd, flags); err != nil {
				t.Fatal(err)
			}
			if command == "image" {
				if g.request.Timeout != 47*time.Second {
					t.Fatalf("image timeout=%s", g.request.Timeout)
				}
			} else if r.remaining < 46*time.Second || r.remaining > 47*time.Second {
				t.Fatalf("deadline=%s", r.remaining)
			}
		})
	}
}

func TestContinuationIgnoresEnvironmentModel(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	t.Setenv("HOLLIS_STATE_DIR", t.TempDir())
	st, err := defaultOpenStore()
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversationWithTurn("cloud", "fixture", "hello", "answer", store.RunRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOLLIS_MODEL", "invalid")
	r := &recordingRunner{response: "fixture"}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"chat", "--continue", conv.ID, "--no-input", "follow up"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.requested, []runner.Model{runner.ModelCloud}) {
		t.Fatal(r.requested)
	}
}

func TestOverviewVersusSelectedGateAndSetupHelp(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, nil, true, 27)
	for _, mode := range []string{"--json", "--agent"} {
		out, err := runDefaultsCLI(t, "doctor", mode, "--select", "bridges.model")
		if err != nil {
			t.Fatal(err)
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(out), &row); err != nil {
			t.Fatal(err)
		}
		if mode == "--agent" {
			row = row["results"].(map[string]any)
		}
		if row["overview"] != true || row["readiness"] != "not_assessed" || row["inference_tested"] != false {
			t.Fatal(row)
		}
		if _, err := runDefaultsCLI(t, "doctor", "--model", "cloud", mode); ExitCode(err) != 3 {
			t.Fatalf("selected gate=%v", err)
		}
	}
	out, err := runDefaultsCLI(t, "setup")
	if err != nil || !strings.Contains(out, "Choose a route") {
		t.Fatalf("%s %v", out, err)
	}
	out, err = runDefaultsCLI(t, "setup", "--model", "local")
	if ExitCode(err) != 10 || !strings.Contains(out, "does not mean a helper or bridge is missing") {
		t.Fatalf("%s %v", out, err)
	}
}
