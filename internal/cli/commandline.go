// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// maxSuggestionDistance is the edit distance at which a first argument stops
// being treated as prompt text and is refused as a mistyped command instead.
// It is the boundary of the documented rule table in docs/cli.md.
const maxSuggestionDistance = 2

// ResolveCommandLine applies the root command-line rules to an argument list
// and returns the arguments the command tree should run.
//
// A first argument that names a command runs that command. A near-miss of a
// command name is left in place so the root command refuses it with a
// suggestion and exit 2; refusing it as a prompt would hide a typo inside a
// model call. Anything else is a prompt, so the whole command line becomes
// "respond <args>" and `hollis "explain closures"` behaves exactly like
// `hollis respond "explain closures"`, including respond's own flags.
//
// Tokens after "--" are always literal prompt text, and cobra's hidden "__"
// completion commands are never reinterpreted. The function is idempotent:
// a resolved command line starts with a command name and is returned as is.
func ResolveCommandLine(root *cobra.Command, args []string) []string {
	first, ok := firstCommandToken(args)
	if !ok {
		return args
	}
	if first.literal || isPrompt(root, first.text) {
		return append([]string{"respond"}, args...)
	}
	return args
}

// isPrompt reports whether a first argument should become a prompt rather than
// the name of a command. Unknown names that sit within maxSuggestionDistance of
// a real command are not prompts: they are almost always typos, and the caller
// gets a suggestion instead of an unintended model call.
func isPrompt(root *cobra.Command, token string) bool {
	if strings.HasPrefix(token, "__") {
		return false
	}
	names := childCommandNames(root)
	for _, name := range names {
		if name == token {
			return false
		}
	}
	return nearestCommand(token, names) == ""
}

// commandToken is the first non-flag argument and whether it followed "--".
type commandToken struct {
	text    string
	literal bool
}

// firstCommandToken finds the first argument that is not a flag. --select is
// the only root flag that consumes a separate value, so it is skipped together
// with that value. A token after "--" is reported as literal, because flag
// parsing has already ended there.
func firstCommandToken(args []string) (commandToken, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			if i+1 == len(args) {
				return commandToken{}, false
			}
			return commandToken{text: args[i+1], literal: true}, true
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			return commandToken{text: arg}, true
		case arg == "--select" && i+1 < len(args):
			i++
		}
	}
	return commandToken{}, false
}

// childCommandNames returns the direct subcommand names of parent, sorted.
func childCommandNames(parent *cobra.Command) []string {
	children := parent.Commands()
	names := make([]string, 0, len(children))
	for _, child := range children {
		names = append(names, child.Name())
	}
	sort.Strings(names)
	return names
}

// nearestCommand returns the command name within maxSuggestionDistance edits of
// token, preferring the smallest distance and then the first name in sorted
// order. Case is ignored, so "Chat" suggests "chat" instead of becoming a
// prompt with a surprising first letter.
func nearestCommand(token string, names []string) string {
	lower := strings.ToLower(token)
	best, bestDistance := "", maxSuggestionDistance+1
	for _, name := range names {
		distance := editDistance(lower, strings.ToLower(name))
		if distance < bestDistance {
			best, bestDistance = name, distance
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b, counted in runes.
func editDistance(a, b string) int {
	left, right := []rune(a), []rune(b)
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

// unknownRootCommandError explains both ways forward for a first argument that
// is not a command: the closest command name, and the respond form that sends
// the same text as a prompt. ResolveCommandLine routes ordinary prompts to
// respond, so this is reached for a near-miss typo.
func unknownRootCommandError(root *cobra.Command, args []string) error {
	name := args[0]
	text := strings.Join(args, " ")
	if suggestion := nearestCommand(name, childCommandNames(root)); suggestion != "" {
		return usageErr(fmt.Errorf("unknown command %q: did you mean %q? To send it as a prompt instead, run: hollis respond %q", name, suggestion, text))
	}
	return usageErr(fmt.Errorf("unknown command %q: run 'hollis --help' for the command list, or 'hollis respond %q' to send it as a prompt", name, text))
}

// unknownSubcommandError is the group-command form of the same guard, used by
// commands that dispatch their own subcommands (config, chats).
func unknownSubcommandError(group *cobra.Command, name string) error {
	if suggestion := nearestCommand(name, childCommandNames(group)); suggestion != "" {
		return usageErr(fmt.Errorf("unknown %s command %q: did you mean %q? Run '%s --help' for the command list", group.Name(), name, suggestion, group.CommandPath()))
	}
	return usageErr(fmt.Errorf("unknown %s command %q: run '%s --help'", group.Name(), name, group.CommandPath()))
}
