// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

// schemaCorpus holds the schema-tier shapes. Expected tiers are asserted in
// TestSchemaTierCorpus.
var schemaCorpus = map[string]string{
	"strict":             `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"],"additionalProperties":false}`,
	"annotated":          `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","description":"d","properties":{"q":{"type":"string","title":"Q","format":"uri","minLength":1,"default":"x"}},"additionalProperties":false}`,
	"ap_absent":          `{"type":"object","properties":{"q":{"type":"string"}}}`,
	"ap_true":            `{"type":"object","properties":{"q":{"type":"string"}},"additionalProperties":true}`,
	"nullable":           `{"type":"object","properties":{"q":{"anyOf":[{"type":"string"},{"type":"null"}]}}}`,
	"union_string_array": `{"type":"object","properties":{"q":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}}]}}}`,
	"ref":                `{"type":"object","properties":{"q":{"$ref":"#/$defs/q"}},"$defs":{"q":{"type":"string"}}}`,
	"nested_depth_4":     `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"object","properties":{},"additionalProperties":false}},"additionalProperties":false}},"additionalProperties":false}},"additionalProperties":false}`,
	"non_string_enum":    `{"type":"object","properties":{"q":{"type":"integer","enum":[1,2]}},"additionalProperties":false}`,
	"root_not_object":    `{"type":"array","items":{"type":"string"}}`,
	"pattern":            `{"type":"object","properties":{"q":{"type":"string","pattern":"^a"}}}`,
}

func props33() string {
	var b strings.Builder
	b.WriteString(`{"type":"object","additionalProperties":false,"properties":{`)
	for i := 0; i < 33; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"p` + strconv.Itoa(i) + `":{"type":"string"}`)
	}
	b.WriteString(`}}`)
	return b.String()
}

func init() {
	schemaCorpus["props_33"] = props33()
	schemaCorpus["size_70k"] = `{"type":"object","description":"` + strings.Repeat("z", 70<<10) + `","properties":{}}`
}

// sharedSubsetWidened reports whether the shared schema subset already
// accepts an object without an explicit additionalProperties.
func sharedSubsetWidened() bool {
	_, err := runner.ParseSchema([]byte(schemaCorpus["ap_absent"]))
	return err == nil
}

// fixtureServer returns a stdio server definition that runs the fixture in
// mode, plus its registry directory.
func fixtureServer(t *testing.T, mode string, extra ...string) (Server, string) {
	t.Helper()
	dir := t.TempDir()
	args := []string{"-test.run=^TestMCPFixtureServer$", "--", fixtureMarker, "mode=" + mode, "dir=" + dir}
	args = append(args, extra...)
	t.Cleanup(func() { assertGroupGone(t, dir) })
	return Server{Command: os.Args[0], Args: args}, dir
}

// assertGroupGone fails if any member of the fixture's process group, or a
// recorded grandchild, is still alive.
func assertGroupGone(t *testing.T, dir string) {
	t.Helper()
	pids := map[string]int{}
	for _, name := range []string{"pgid", "grandchild.pid"} {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				pids[name] = n
			}
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for name, pid := range pids {
		target := pid
		if name == "pgid" {
			target = -pid
		}
		for {
			err := syscall.Kill(target, 0)
			if errors.Is(err, syscall.ESRCH) {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("%s %d still alive after the manager closed", name, pid)
				_ = syscall.Kill(target, syscall.SIGKILL)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func readCalls(t *testing.T, dir string) []map[string]any {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, "calls.jsonl"))
	if err != nil {
		return nil
	}
	defer file.Close()
	var calls []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		if json.Unmarshal(scanner.Bytes(), &record) == nil {
			calls = append(calls, record)
		}
	}
	return calls
}

func executedMarkers(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, "executed"))
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func assertNothingExecuted(t *testing.T, dir string) {
	t.Helper()
	if calls := readCalls(t, dir); len(calls) != 0 {
		t.Errorf("calls.jsonl has %d records; want none", len(calls))
	}
	if markers := executedMarkers(t, dir); len(markers) != 0 {
		t.Errorf("executed markers %v; want none", markers)
	}
}

func interactive() Options {
	return Options{Interaction: Interactivity{Interactive: true}, Approver: yesApprover{}}
}

func attach(t *testing.T, name string, s Server, opts Options) *Manager {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m, err := Attach(ctx, []Attachment{{Name: name, Server: s}}, opts)
	if err != nil {
		t.Fatalf("attach %s: %v", name, err)
	}
	t.Cleanup(m.Close)
	return m
}

func attachErr(t *testing.T, name string, s Server, opts Options) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m, err := Attach(ctx, []Attachment{{Name: name, Server: s}}, opts)
	if err == nil {
		m.Close()
		t.Fatalf("attach %s succeeded; want an error", name)
	}
	return err
}

func wantCode(t *testing.T, err error, code Code) *Error {
	t.Helper()
	var mcpErr *Error
	if !errors.As(err, &mcpErr) {
		t.Fatalf("error %v (%T) is not an *mcp.Error; want %s", err, err, code)
	}
	if mcpErr.Code != code {
		t.Fatalf("code %s (%v); want %s", mcpErr.Code, err, code)
	}
	return mcpErr
}

func callCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Approver fakes.

type yesApprover struct{}

func (yesApprover) Approve(context.Context, string, Request) (bool, error) { return true, nil }

type noApprover struct{}

func (noApprover) Approve(context.Context, string, Request) (bool, error) { return false, nil }

type errApprover struct{ err error }

func (a errApprover) Approve(context.Context, string, Request) (bool, error) { return true, a.err }

type recordApprover struct {
	mu      sync.Mutex
	prompts []string
	reqs    []Request
}

func (a *recordApprover) Approve(_ context.Context, prompt string, req Request) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prompts = append(a.prompts, prompt)
	a.reqs = append(a.reqs, req)
	return true, nil
}

type countingApprover struct{ n atomic.Int64 }

func (a *countingApprover) Approve(context.Context, string, Request) (bool, error) {
	a.n.Add(1)
	return true, nil
}
