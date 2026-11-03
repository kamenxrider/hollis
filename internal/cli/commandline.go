// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ResolveCommandLine applies the root command-line rules without parsing flags.
// Only a single positional argument can be refused as a typo. Flag values do
// not count as positional arguments. Root help/version retain their strict
// argument validation, and tokens after "--" are literal prompt text.
func ResolveCommandLine(root *cobra.Command, args []string) ([]string, error) {
	positionals, respondFlags, rootControl := commandTokens(root, args)
	if len(positionals) == 0 {
		if respondFlags && !rootControl {
			return append([]string{"respond"}, args...), nil
		}
		return args, nil
	}
	first := positionals[0]
	names := childCommandNames(root)
	for _, name := range names {
		if !first.literal && name == first.text {
			// Cobra cannot skip a child-only flag's separate value while
			// finding the child. Put the known command before leading flags.
			resolved := []string{first.text}
			resolved = append(resolved, args[:first.index]...)
			resolved = append(resolved, args[first.index+1:]...)
			return resolved, nil
		}
	}
	if (strings.HasPrefix(first.text, "__") && !first.literal) || rootControl {
		return args, nil
	}
	if !first.literal && len(positionals) == 1 && nearestCommand(first.text, names) != "" {
		return nil, nearMissCommandError(root, args, first)
	}
	return append([]string{"respond"}, args...), nil
}

type commandToken struct {
	text    string
	index   int
	literal bool
}

// commandTokens consults flag metadata without mutating flag values.
// Unknown and malformed flags are left for Cobra's normal validation.
func commandTokens(root *cobra.Command, args []string) (tokens []commandToken, respondFlags, rootControl bool) {
	respond, _, _ := root.Find([]string{"respond"})
	lookup := func(name string, shorthand bool) *pflag.Flag {
		for _, set := range []*pflag.FlagSet{root.PersistentFlags(), root.Flags(), respond.Flags()} {
			var flag *pflag.Flag
			if shorthand {
				flag = set.ShorthandLookup(name)
			} else {
				flag = set.Lookup(name)
			}
			if flag != nil {
				if flag.Name == "help" || flag.Name == "version" {
					rootControl = true
				} else if set == respond.Flags() {
					respondFlags = true
				}
				return flag
			}
		}
		return nil
	}
	literal := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case literal:
			tokens = append(tokens, commandToken{text: arg, index: i, literal: true})
		case arg == "--":
			literal = true
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			tokens = append(tokens, commandToken{text: arg, index: i})
		case strings.HasPrefix(arg, "--"):
			name, _, equals := strings.Cut(arg[2:], "=")
			if flag := lookup(name, false); flag != nil && flag.NoOptDefVal == "" && !equals {
				i++
			}
		default:
			for j := 1; j < len(arg); j++ {
				flag := lookup(arg[j:j+1], true)
				if flag == nil {
					break
				}
				if flag.NoOptDefVal == "" {
					if j+1 == len(arg) {
						i++
					}
					break
				}
				if j+1 < len(arg) && arg[j+1] == '=' {
					break
				}
			}
		}
	}
	return tokens, respondFlags, rootControl
}

func childCommandNames(parent *cobra.Command) []string {
	children := parent.Commands()
	names := make([]string, 0, len(children))
	for _, child := range children {
		names = append(names, child.Name())
	}
	sort.Strings(names)
	return names
}

// nearestCommand permits one edit, or two for names of at least seven runes.
// Ties use sorted command order; comparison is case-insensitive.
func nearestCommand(token string, names []string) string {
	lower := strings.ToLower(token)
	best, bestDistance := "", 3
	for _, name := range names {
		limit := 1
		if len([]rune(name)) >= 7 {
			limit = 2
		}
		distance := editDistance(lower, strings.ToLower(name))
		if distance <= limit && distance < bestDistance {
			best, bestDistance = name, distance
		}
	}
	return best
}

// editDistance counts insertions, deletions, substitutions and adjacent swaps
// in runes (optimal string alignment distance).
func editDistance(a, b string) int {
	left, right := []rune(a), []rune(b)
	beforePrevious := make([]int, len(right)+1)
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
			if i > 1 && j > 1 && left[i-1] == right[j-2] && left[i-2] == right[j-1] {
				current[j] = min(current[j], beforePrevious[j-2]+1)
			}
		}
		beforePrevious, previous, current = previous, current, beforePrevious
	}
	return previous[len(right)]
}

func unknownRootCommandError(_ *cobra.Command, args []string) error {
	return usageErr(fmt.Errorf("unknown command %q: run 'hollis --help' for the command list", args[0]))
}

func nearMissCommandError(root *cobra.Command, args []string, first commandToken) error {
	command := []string{"hollis"}
	for _, arg := range args[:first.index] {
		command = append(command, shellArgument(arg))
	}
	prompt := fmt.Sprintf("%q", first.text)
	prompt = strings.NewReplacer("$", `\$`, "`", "\\`").Replace(prompt)
	command = append(command, "respond", prompt)
	for _, arg := range args[first.index+1:] {
		command = append(command, shellArgument(arg))
	}
	return usageErr(fmt.Errorf("unknown command %q: did you mean %q? To send it as a prompt instead, run: %s", first.text, nearestCommand(first.text, childCommandNames(root)), strings.Join(command, " ")))
}

func shellArgument(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_=.,:/", r)
	}) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

// unknownSubcommandError is used by commands that dispatch their own
// subcommands (config, chats).
func unknownSubcommandError(group *cobra.Command, name string) error {
	if suggestion := nearestCommand(name, childCommandNames(group)); suggestion != "" {
		return usageErr(fmt.Errorf("unknown %s command %q: did you mean %q? Run '%s --help' for the command list", group.Name(), name, suggestion, group.CommandPath()))
	}
	return usageErr(fmt.Errorf("unknown %s command %q: run '%s --help'", group.Name(), name, group.CommandPath()))
}
