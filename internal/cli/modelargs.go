// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import "github.com/kamenxrider/hollis/internal/runner"

// splitModelArgs implements the positional `model <tier>` prefix used by
// respond and chat: `hollis respond model cloud-pro "Draft a reply"`.
//
// The word "model" is only a selector when the next token is a valid
// tier, so prompts that merely start with the word "model" keep working.
// The --model flag remains as the escape hatch for the rare case where
// the prompt really should begin with "model <tier>".
//
// Returns the selected tier ("" when the prefix is absent, in which case
// the caller falls back to the --model flag), the remaining prompt
// arguments, and whether the prefix matched.
func splitModelArgs(args []string) (tier string, rest []string, hasModel bool) {
	if len(args) >= 2 && args[0] == "model" && runner.Model(args[1]).Valid() {
		return args[1], args[2:], true
	}
	return "", args, false
}
