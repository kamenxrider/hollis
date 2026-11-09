// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func updateRelease(tag string) string {
	return fmt.Sprintf(`{"tag_name":%q,"html_url":%q,"draft":false,"prerelease":false}`, tag, updateReleasesURL+"/tag/"+tag)
}

func updateFixtureFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func updateTestRoot(t *testing.T, deps updateDependencies) (*cobra.Command, *rootFlags, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	root, flags := newRootCmdWithFlags(func() runner.Runner { return &fakeRunner{} })
	old, _, err := root.Find([]string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	root.RemoveCommand(old)
	root.AddCommand(newUpdateCmdWithDependencies(flags, deps))
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(stderr)
	return root, flags, out, stderr
}

func TestUpdateReleases(t *testing.T) {
	for _, tc := range []struct {
		name, body, current, status, machine string
		httpStatus, exit                     int
	}{
		{name: "newer", body: updateRelease("v0.8.0"), status: "update_available"},
		{name: "same", body: updateRelease("v0.7.0"), status: "up_to_date"},
		{name: "older", body: updateRelease("v0.4.0"), status: "newer_than_published"},
		{name: "prerelease current", current: "0.7.0-beta.10", body: updateRelease("v0.7.0"), status: "update_available"},
		{name: "unversioned", current: "dev", body: updateRelease("v0.7.0"), status: "unknown"},
		{name: "no releases", httpStatus: 404, exit: 3, machine: "missing"},
		{name: "prerelease flag", body: strings.Replace(updateRelease("v0.8.0"), `"prerelease":false`, `"prerelease":true`, 1), exit: 3, machine: "missing"},
		{name: "prerelease tag", body: updateRelease("v0.8.0-rc.1"), exit: 3, machine: "missing"},
		{name: "draft", body: strings.Replace(updateRelease("v0.8.0"), `"draft":false`, `"draft":true`, 1), exit: 3, machine: "missing"},
		{name: "forbidden", httpStatus: 403, exit: 5, machine: "rate_limited"},
		{name: "rate limited", httpStatus: 429, exit: 5, machine: "rate_limited"},
		{name: "server error", httpStatus: 503, exit: 5, machine: "transport"},
		{name: "malformed", body: "{", exit: 5, machine: "update_response"},
		{name: "missing tag", body: "{}", exit: 5, machine: "update_response"},
		{name: "invalid version", body: updateRelease("v01.2.3"), exit: 5, machine: "update_response"},
		{name: "unexpected URL", body: `{"tag_name":"v0.8.0","html_url":"https://example.com"}`, exit: 5, machine: "update_response"},
		{name: "oversized", body: strings.Repeat(" ", (1<<20)+1), exit: 5, machine: "update_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			current := tc.current
			if current == "" {
				current = "0.7.0"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.Header.Get("User-Agent") != "hollis/"+current || r.Header.Get("Accept") != "application/vnd.github+json" {
					t.Errorf("unexpected request: %s %v", r.Method, r.Header)
				}
				if tc.httpStatus != 0 {
					w.WriteHeader(tc.httpStatus)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			got, err := checkUpdate(context.Background(), updateDependencies{client: server.Client(), endpoint: server.URL, current: current})
			if ExitCode(err) != tc.exit || calls != 1 {
				t.Fatalf("result=%v err=%v exit=%d requests=%d", got, err, ExitCode(err), calls)
			}
			if err != nil {
				if errorCode(err) != tc.machine {
					t.Fatalf("error code=%s want %s", errorCode(err), tc.machine)
				}
				return
			}
			if got["status"] != tc.status || got["update_available"] != (tc.status == "update_available") || got["version_comparable"] != (current != "dev") {
				t.Fatalf("result=%v", got)
			}
		})
	}
}

func TestUpdateTimeoutAndOffline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	client := server.Client()
	client.Timeout = 20 * time.Millisecond
	_, err := checkUpdate(context.Background(), updateDependencies{client: client, endpoint: server.URL, current: "0.7.0"})
	if ExitCode(err) != 7 || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout=%v", err)
	}
	server.Close()
	_, err = checkUpdate(context.Background(), updateDependencies{client: client, endpoint: server.URL, current: "0.7.0"})
	if ExitCode(err) != 5 || errorCode(err) != "transport" || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("offline=%v", err)
	}
}

func TestUpdateClientPolicy(t *testing.T) {
	cmd := newUpdateCmd(&rootFlags{})
	if cmd == nil || updateTimeout != 10*time.Second || updateEndpoint != "https://api.github.com/repos/kamenxrider/hollis/releases/latest" {
		t.Fatal("update policy changed")
	}
	// Exercise the production client, replacing only its transport; no public request.
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	calls := 0
	http.DefaultTransport = updateRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://example.com/"}}, Body: http.NoBody, Request: req}, nil
	})
	root := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	root.SetArgs([]string{"update"})
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); ExitCode(err) != 5 || calls != 1 {
		t.Fatalf("redirect err=%v calls=%d", err, calls)
	}
}

type updateRoundTripper func(*http.Request) (*http.Response, error)

func (f updateRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateInstallationDetection(t *testing.T) {
	home := t.TempDir()
	prefix := t.TempDir()
	brew := updateFixtureFile(t, filepath.Join(prefix, "Cellar", "hollis", "0.7.0", "libexec", "0.7.0", "hollis"))
	link := filepath.Join(prefix, "hollis")
	if err := os.Symlink(brew, link); err != nil {
		t.Fatal(err)
	}
	installer := updateFixtureFile(t, filepath.Join(home, ".local", "bin", "hollis"))
	manual := updateFixtureFile(t, filepath.Join(prefix, "bin", "hollis"))
	for _, path := range []string{brew, link} {
		if got := detectUpdateInstallation(path, home); got != "homebrew" {
			t.Fatalf("%s: %s", path, got)
		}
	}
	for _, path := range []string{installer, manual, "/nonexistent/hollis", ""} {
		if got := detectUpdateInstallation(path, home); got != "manual" {
			t.Fatalf("%s: %s", path, got)
		}
	}
	helper := updateFixtureFile(t, filepath.Join(home, ".local", "bin", "hollis-native"))
	if got := detectUpdateInstallation(installer, home); got != "installer" {
		t.Fatalf("installer=%s", got)
	}
	if err := os.Chmod(helper, 0600); err != nil {
		t.Fatal(err)
	}
	if got := detectUpdateInstallation(installer, home); got != "manual" {
		t.Fatalf("nonexecutable helper=%s", got)
	}
	other := updateFixtureFile(t, filepath.Join(prefix, "Cellar", "other", "1.0.0", "hollis"))
	if got := detectUpdateInstallation(other, home); got != "manual" {
		t.Fatalf("unrelated cellar=%s", got)
	}
	versioned := updateFixtureFile(t, filepath.Join(home, ".local", "share", "hollis", "0.7.0", "hollis"))
	if got := detectUpdateInstallation(versioned, home); got != "manual" {
		t.Fatalf("missing versioned helper=%s", got)
	}
	updateFixtureFile(t, filepath.Join(filepath.Dir(versioned), "hollis-native"))
	if err := os.Remove(installer); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versioned, installer); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{installer, versioned} {
		if got := detectUpdateInstallation(path, home); got != "installer" {
			t.Fatalf("versioned %s=%s", path, got)
		}
	}
}

func TestUpdateApply(t *testing.T) {
	for _, tc := range []struct {
		name, method, answer, tag string
		args                      []string
		terminal, wantRun, fail   bool
		exit, requests            int
	}{
		{name: "yes", method: "homebrew", args: []string{"--apply", "--yes", "--json"}, wantRun: true, requests: 1},
		{name: "agent yes", method: "homebrew", args: []string{"--apply", "--yes", "--agent"}, wantRun: true, requests: 1},
		{name: "failure", method: "homebrew", args: []string{"--apply", "--yes", "--json"}, wantRun: true, fail: true, exit: 5, requests: 1},
		{name: "installer refusal", method: "installer", args: []string{"--apply", "--yes", "--json"}, exit: 2},
		{name: "manual refusal", method: "manual", args: []string{"--apply", "--yes"}, exit: 2},
		{name: "no input", method: "homebrew", args: []string{"--apply", "--no-input"}, exit: 2},
		{name: "agent approval missing", method: "homebrew", args: []string{"--apply", "--agent"}, exit: 2},
		{name: "redirected input", method: "homebrew", args: []string{"--apply"}, exit: 2},
		{name: "json terminal", method: "homebrew", args: []string{"--apply", "--json"}, terminal: true, exit: 2},
		{name: "prompt yes", method: "homebrew", args: []string{"--apply"}, terminal: true, answer: "y\n", wantRun: true, requests: 1},
		{name: "prompt no", method: "homebrew", args: []string{"--apply"}, terminal: true, answer: "n\n", exit: 2, requests: 1},
		{name: "prompt EOF", method: "homebrew", args: []string{"--apply"}, terminal: true, exit: 2, requests: 1},
		{name: "no downgrade", method: "homebrew", tag: "v0.4.0", args: []string{"--apply", "--yes"}, requests: 1},
		{name: "same version", method: "homebrew", tag: "v0.7.0", args: []string{"--apply", "--yes"}, requests: 1},
		{name: "yes needs apply", method: "homebrew", args: []string{"--yes"}, exit: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := interactiveStdin
			interactiveStdin = func() bool { return tc.terminal }
			t.Cleanup(func() { interactiveStdin = old })
			dir, home := t.TempDir(), t.TempDir()
			log := filepath.Join(dir, "brew-args")
			t.Setenv("UPDATE_TEST_LOG", log)
			t.Setenv("PATH", dir)
			if tc.fail {
				t.Setenv("UPDATE_TEST_EXIT", "1")
			} else {
				t.Setenv("UPDATE_TEST_EXIT", "0")
			}
			if err := os.WriteFile(filepath.Join(dir, "brew"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$UPDATE_TEST_LOG\"\necho brew-output\nexit \"$UPDATE_TEST_EXIT\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			var executable string
			switch tc.method {
			case "homebrew":
				executable = updateFixtureFile(t, filepath.Join(dir, "Cellar", "hollis", "0.7.0", "bin", "hollis"))
			case "installer":
				executable = updateFixtureFile(t, filepath.Join(home, ".local", "bin", "hollis"))
				updateFixtureFile(t, filepath.Join(home, ".local", "bin", "hollis-native"))
			default:
				executable = updateFixtureFile(t, filepath.Join(dir, "hollis"))
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				tag := tc.tag
				if tag == "" {
					tag = "v0.8.0"
				}
				fmt.Fprint(w, updateRelease(tag))
			}))
			defer server.Close()
			deps := updateDependencies{client: server.Client(), endpoint: server.URL, current: "0.7.0",
				executable: func() (string, error) { return executable, nil }, home: func() (string, error) { return home, nil }}
			root, flags, out, stderr := updateTestRoot(t, deps)
			root.SetIn(strings.NewReader(tc.answer))
			root.SetArgs(append([]string{"update"}, tc.args...))
			err := executeCommand(root, flags)
			if ExitCode(err) != tc.exit || calls != tc.requests {
				t.Fatalf("err=%v exit=%d calls=%d stdout=%s stderr=%s", err, ExitCode(err), calls, out, stderr)
			}
			raw, readErr := os.ReadFile(log)
			if tc.wantRun && (readErr != nil || string(raw) != "upgrade\nkamenxrider/tap/hollis\n") || !tc.wantRun && !os.IsNotExist(readErr) {
				t.Fatalf("brew run=%v args=%q err=%v", tc.wantRun, raw, readErr)
			}
			if (flags.asJSON || flags.agent) && !json.Valid(out.Bytes()) {
				t.Fatalf("invalid JSON: %s", out)
			}
			if tc.wantRun && !strings.Contains(stderr.String(), "brew-output") {
				t.Fatalf("missing brew output on stderr: %s", stderr)
			}
			if tc.exit == 2 && tc.method == "installer" {
				if flags.asJSON || flags.agent {
					var result struct {
						Error struct {
							Suggestion string `json:"suggestion"`
						} `json:"error"`
					}
					if err := json.Unmarshal(out.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if result.Error.Suggestion != updateInstallerCommand {
						t.Fatalf("installer command=%q, want %q", result.Error.Suggestion, updateInstallerCommand)
					}
				} else if !strings.Contains(out.String(), updateInstallerCommand) {
					t.Fatalf("missing manual installer command: %s", out)
				}
			}
		})
	}
}

func TestUpdateOutputAndSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, updateRelease("v0.4.0")) }))
	defer server.Close()
	for _, args := range [][]string{{}, {"--json"}, {"--agent"}, {"--json", "--select", "status,update_available"}} {
		deps := updateDependencies{client: server.Client(), endpoint: server.URL, current: "0.7.0",
			executable: func() (string, error) { return "", nil }, home: func() (string, error) { return "", nil }}
		root, flags, out, _ := updateTestRoot(t, deps)
		root.SetArgs(append([]string{"update"}, args...))
		if err := executeCommand(root, flags); err != nil {
			t.Fatal(err)
		}
		if len(args) == 0 {
			for _, text := range []string{"Current version: 0.7.0", "Latest stable: 0.4.0", "newer than", "Release: " + updateReleasesURL + "/tag/v0.4.0", "Upgrade: " + updateReleasesURL} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("missing %q: %s", text, out)
				}
			}
		} else if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), `"update_available":false`) {
			t.Fatalf("JSON: %s", out)
		}
	}
}

func TestUpdateAgentContextGolden(t *testing.T) {
	root := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	var got agentContextCommand
	for _, cmd := range buildAgentContext(root).Commands {
		if cmd.Name == "update" {
			got = cmd
		}
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "goldens", "update_agent_context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want agentContextCommand
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("update agent contract differs:\ngot %+v\nwant %+v", got, want)
	}
}

func TestUpdateSemver(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.10.0", "2.0.0", "999999999999999999999.0.0"}
	for i, s := range ordered {
		v, ok := parseUpdateVersion(s)
		if !ok {
			t.Fatalf("invalid %s", s)
		}
		for j, other := range ordered {
			w, _ := parseUpdateVersion(other)
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := compareUpdateVersions(v, w); got != want {
				t.Fatalf("%s cmp %s=%d want %d", s, other, got, want)
			}
		}
	}
	a, _ := parseUpdateVersion("v1.2.3+build.001")
	b, _ := parseUpdateVersion("1.2.3+other")
	if compareUpdateVersions(a, b) != 0 {
		t.Fatal("metadata affected precedence")
	}
	for _, s := range []string{"1", "1.2", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-a..b", "1.2.3+", "dev", "1.2.3\n"} {
		if _, ok := parseUpdateVersion(s); ok {
			t.Fatalf("accepted invalid %q", s)
		}
	}
}
