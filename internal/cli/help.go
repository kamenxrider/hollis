// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"strings"
)

// The tables in this file are the single source for the sections root help and
// agent-context both publish. Help text generated from the same data cannot
// drift from the machine-readable contract (a measured risk: exit codes lived
// only in the docs and in agent-context, so `hollis --help` never named them).

// environmentDoc describes one environment variable the CLI reads.
type environmentDoc struct {
	Name        string
	Description string
}

var documentedEnvironment = []environmentDoc{
	{Name: "HOLLIS_STATE_DIR", Description: "absolute state directory (default: ~/Library/Application Support/hollis)"},
	{Name: "HOLLIS_API_TOKEN", Description: "bearer token for hollis serve (alternative to --token-file)"},
}

// exitCodeDoc is one numeric exit code. Semantic codes below share the
// transport exit but name the cause in JSON and agent output.
type exitCodeDoc struct {
	Code        int
	Name        string
	Description string
}

var documentedExitCodes = []exitCodeDoc{
	{Code: 0, Name: "success", Description: "success"},
	{Code: 1, Name: "unexpected", Description: "unexpected internal error"},
	{Code: 2, Name: "usage", Description: "usage error: bad flag, unknown model, empty prompt, or invalid arguments"},
	{Code: 3, Name: "missing", Description: "missing resource: unknown conversation, no search hits, or a bridge that is not installed"},
	{Code: 5, Name: "transport", Description: "discovery, transport, Shortcut or native failure, declined request, or rate limit"},
	{Code: 7, Name: "timeout", Description: "the run exceeded its deadline (also --code when there is no code to print)"},
	{Code: 10, Name: "config", Description: "config or database error"},
}

// semanticCodeDoc is a machine code that shares a numeric exit.
type semanticCodeDoc struct {
	Name string
	Code int
}

var documentedSemanticCodes = []semanticCodeDoc{
	{Name: "request_declined", Code: 5},
	{Name: "shortcut_failed", Code: 5},
}

// agentExampleCommands is rendered in root help and published by agent-context,
// so an agent reads the same examples a person sees.
var agentExampleCommands = []string{
	`hollis respond "Explain closures in Go in three sentences"`,
	`hollis respond "Summarize this document" --file notes.md`,
	`printf 'Explain a race condition' | hollis respond`,
	`hollis chat`,
	`hollis chat --continue <id> "And the downside?"`,
	`hollis respond --code "Write a Go function that reverses a slice" > reverse.go`,
	`hollis respond --model local --schema person.json "Invent one person"`,
	`hollis doctor`,
	`hollis serve --token-file hollis.token`,
}

// exitCodeMap is the agent-context shape of the numeric and semantic tables.
func exitCodeMap() map[string]int {
	codes := make(map[string]int, len(documentedExitCodes)+len(documentedSemanticCodes))
	for _, code := range documentedExitCodes {
		codes[code.Name] = code.Code
	}
	for _, code := range documentedSemanticCodes {
		codes[code.Name] = code.Code
	}
	return codes
}

// environmentNames is the agent-context shape of the environment table.
func environmentNames() []string {
	names := make([]string, 0, len(documentedEnvironment))
	for _, variable := range documentedEnvironment {
		names = append(names, variable.Name)
	}
	return names
}

// rootLongHelp appends the sections generated from the agent-context tables to
// the root description.
func rootLongHelp(description string) string {
	var help strings.Builder
	help.WriteString(description)
	help.WriteString("\n\nExamples:\n")
	for _, example := range agentExampleCommands {
		fmt.Fprintf(&help, "  %s\n", example)
	}
	help.WriteString("\nEnvironment:\n")
	width := 0
	for _, variable := range documentedEnvironment {
		if len(variable.Name) > width {
			width = len(variable.Name)
		}
	}
	for _, variable := range documentedEnvironment {
		fmt.Fprintf(&help, "  %-*s  %s\n", width, variable.Name, variable.Description)
	}
	help.WriteString("\nExit codes:\n")
	for _, code := range documentedExitCodes {
		fmt.Fprintf(&help, "  %-2d  %s\n", code.Code, code.Description)
	}
	return strings.TrimRight(help.String(), "\n")
}

// replHelp is printed by /help inside an interactive chat.
const replHelp = `Commands:
  /help            show this help
  /image <prompt>  generate an image (needs --output and a style or bridge)
  quit, exit       leave the chat (Ctrl-D also leaves)

Anything else is sent to the model. Use --continue <id> to resume a conversation.`
