// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredImageGenerateAgentOutput(t *testing.T) {
	generator := newFakeImageGenerator(t)
	cmd, _ := newRootCmdWithImageGenerator(nil, generator)
	destination := filepath.Join(t.TempDir(), "generated.png")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--agent", "--select", "path,width,height", "image", "generate", "a red circle", "--bridge", "image fixture", "--output", destination})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Meta    map[string]any `json:"meta"`
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Results) != 3 || envelope.Results["path"] != destination || envelope.Results["width"] != float64(4) || envelope.Results["height"] != float64(3) || envelope.Meta["schema_version"] != agentContextSchemaVersion {
		t.Fatalf("unexpected agent result: %s", out.String())
	}
	if generator.calls != 1 || generator.request.Prompt != "a red circle" || generator.request.BridgeRef != "image fixture" {
		t.Fatalf("unexpected generation: %+v", generator)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("output not private: %v, %v", info, err)
	}
	// An already published output must be rejected before a second model call.
	second, _ := newRootCmdWithImageGenerator(nil, generator)
	second.SetArgs([]string{"image", "generate", "again", "--bridge", "image fixture", "--output", destination})
	if err := second.Execute(); err == nil || generator.calls != 1 {
		t.Fatalf("collision should prevent another call: err=%v calls=%d", err, generator.calls)
	}
}

func TestRegisteredImageContractsNeverGenerate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"help", []string{"image", "generate", "--help"}, 0},
		{"json help", []string{"image", "generate", "--help", "--json"}, 0},
		{"unknown child", []string{"image", "unknown"}, 2},
		{"parent agent", []string{"image", "--agent"}, 2},
		{"missing bridge", []string{"image", "generate", "draw", "--output", "out.png"}, 2},
		{"tier unsupported", []string{"image", "generate", "draw", "--bridge", "fixture", "--output", "out.png", "--model", "cloud"}, 2},
		{"ratio requires explicit fit", []string{"image", "generate", "draw", "--bridge", "fixture", "--output", "out.png", "--aspect-ratio", "16:9"}, 2},
		{"fit requires dimensions", []string{"image", "generate", "draw", "--bridge", "fixture", "--output", "out.png", "--fit", "crop"}, 2},
		{"agent contradiction", []string{"image", "generate", "draw", "--bridge", "fixture", "--output", "out.png", "--agent", "--json=false"}, 2},
		{"selection without json", []string{"image", "generate", "draw", "--bridge", "fixture", "--output", "out.png", "--select", "path"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubConfigPath(t)
			generator := &fakeImageGenerator{}
			cmd, _ := newRootCmdWithImageGenerator(nil, generator)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if ExitCode(err) != tc.wantCode || generator.calls != 0 {
				t.Fatalf("err=%v code=%d calls=%d", err, ExitCode(err), generator.calls)
			}
		})
	}
}

func TestImageAgentDiscoveryHasEffectsAndFlags(t *testing.T) {
	cmd, _ := newRootCmdWithImageGenerator(nil, &fakeImageGenerator{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"agent-context"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var contract agentContext
	if err := json.Unmarshal(out.Bytes(), &contract); err != nil {
		t.Fatal(err)
	}
	for _, parent := range contract.Commands {
		if parent.Name != "image" {
			continue
		}
		for _, command := range parent.Subcommands {
			if command.Name != "generate" {
				continue
			}
			effects := strings.Join(command.SideEffects, " ")
			if !strings.Contains(effects, "writes one private PNG") || !strings.Contains(effects, "permissions") || strings.Join(command.OutputModes, ",") != "human,json,agent" {
				t.Fatalf("incomplete image contract: %+v", command)
			}
			flags := map[string]bool{}
			for _, flag := range command.Flags {
				flags[flag.Name] = true
			}
			for _, name := range []string{"bridge", "output", "timeout", "agent", "json", "select"} {
				if !flags[name] {
					t.Errorf("missing %s", name)
				}
			}
			return
		}
	}
	t.Fatal("image generate missing from agent-context")
}
