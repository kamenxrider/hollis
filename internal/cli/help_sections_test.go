// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func rootHelpOutput(t *testing.T) string {
	t.Helper()
	cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	cmd.SetArgs([]string{"--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	return out.String()
}

func TestRootHelpCarriesGeneratedSections(t *testing.T) {
	help := rootHelpOutput(t)
	for _, heading := range []string{"Examples:", "Environment:", "Exit codes:"} {
		if !strings.Contains(help, heading) {
			t.Fatalf("root help missing %q:\n%s", heading, help)
		}
	}
	for _, example := range agentExampleCommands {
		if !strings.Contains(help, example) {
			t.Fatalf("root help missing example %q:\n%s", example, help)
		}
	}
	for _, variable := range documentedEnvironment {
		if !strings.Contains(help, variable.Name) || !strings.Contains(help, variable.Description) {
			t.Fatalf("root help missing environment entry %q", variable.Name)
		}
	}
	for _, code := range documentedExitCodes {
		if !strings.Contains(help, code.Description) {
			t.Fatalf("root help missing exit code %d description %q", code.Code, code.Description)
		}
	}
}

func TestRootHelpSectionOrder(t *testing.T) {
	help := rootHelpOutput(t)
	previous := -1
	for _, heading := range []string{"Usage:", "Flags:", "Examples:", "Environment:", "Exit codes:"} {
		at := strings.Index(help, heading)
		if at <= previous {
			t.Fatalf("%q missing or out of order:\n%s", heading, help)
		}
		previous = at
	}
	if !strings.HasSuffix(help, rootHelpSections()+"\n") {
		t.Fatalf("generated sections are not the last content:\n%s", help)
	}
	if !strings.Contains(help, "near-miss command refusal") {
		t.Fatal("usage exit description omits near-miss command refusals")
	}
}

func TestGeneratedSectionsLeaveSubcommandHelpUnchanged(t *testing.T) {
	for _, args := range [][]string{{"respond", "--help"}, {"chat", "--help"}, {"chats", "--help"}} {
		t.Run(args[0], func(t *testing.T) {
			render := func(defaultTemplate bool) string {
				cmd := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
				if defaultTemplate {
					cmd.SetHelpTemplate((&cobra.Command{}).HelpTemplate())
				}
				cmd.SetArgs(args)
				var out bytes.Buffer
				cmd.SetOut(&out)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("help: %v", err)
				}
				return out.String()
			}
			if got, want := render(false), render(true); got != want {
				t.Fatalf("subcommand help changed:\ngot %q\nwant %q", got, want)
			}
		})
	}
}

// TestAgentContextSharesHelpData keeps the generated sections honest: the same
// tables that render root help feed the machine-readable contract.
func TestAgentContextSharesHelpData(t *testing.T) {
	ctx := buildAgentContext(NewRootCmd(func() runner.Runner { return &fakeRunner{} }))
	if want := []string{"HOLLIS_STATE_DIR", "HOLLIS_API_TOKEN"}; !reflect.DeepEqual(ctx.Auth.EnvVars, want) {
		t.Fatalf("agent env vars = %v, want %v", ctx.Auth.EnvVars, want)
	}
	wantCodes := map[string]int{
		"success": 0, "unexpected": 1, "usage": 2, "missing": 3, "transport": 5,
		"timeout": 7, "config": 10, "request_declined": 5, "shortcut_failed": 5,
	}
	if !reflect.DeepEqual(ctx.Contracts.ExitCodes, wantCodes) {
		t.Fatalf("agent exit codes = %v, want %v", ctx.Contracts.ExitCodes, wantCodes)
	}
	var names []string
	for _, variable := range documentedEnvironment {
		names = append(names, variable.Name)
	}
	if strings.Join(ctx.Auth.EnvVars, ",") != strings.Join(names, ",") {
		t.Fatalf("agent env vars = %v, help documents %v", ctx.Auth.EnvVars, names)
	}
	if strings.Join(ctx.Examples, "\x00") != strings.Join(agentExampleCommands, "\x00") {
		t.Fatalf("agent examples drift from help examples")
	}
	for _, code := range documentedExitCodes {
		if ctx.Contracts.ExitCodes[code.Name] != code.Code {
			t.Fatalf("agent exit code %s = %d, help documents %d", code.Name, ctx.Contracts.ExitCodes[code.Name], code.Code)
		}
	}
	for _, semantic := range documentedSemanticCodes {
		if ctx.Contracts.ExitCodes[semantic.Name] != semantic.Code {
			t.Fatalf("agent semantic code %s = %d, want %d", semantic.Name, ctx.Contracts.ExitCodes[semantic.Name], semantic.Code)
		}
	}
}
