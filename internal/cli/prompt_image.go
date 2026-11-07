// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/kamenxrider/hollis/internal/promptimage"
	"github.com/spf13/cobra"
)

func adjustImagePrompt(cmd *cobra.Command, flags *rootFlags, prompt string, images int, keep bool) (string, bool) {
	if flags.quiet {
		return promptimage.Adjust(prompt, images, keep)
	}
	tty := interactiveStdin() && terminalOutput(cmd.OutOrStdout()) && !flags.noInput && !flags.asJSON && !flags.agent
	return offerImagePrompt(prompt, images, keep, tty, cmd.InOrStdin(), cmd.ErrOrStderr())
}

func offerImagePrompt(prompt string, images int, keep, tty bool, in io.Reader, out io.Writer) (string, bool) {
	adjusted, changed := promptimage.Adjust(prompt, images, keep)
	if !changed {
		return prompt, false
	}
	if tty {
		fmt.Fprintf(out, "hollis: one image is attached, but the prompt uses plural image wording.\nSuggested prompt: %s\nUse this wording? [Y/n] ", sanitizeTerminalText(adjusted))
		line, err := bufio.NewReader(in).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if err != nil || (answer != "" && answer != "y" && answer != "yes") {
			fmt.Fprintln(out, "hollis: keeping the original prompt")
			return prompt, false
		}
	}
	fmt.Fprintln(out, "hollis: adjusted plural image wording for one attached image; use --keep-prompt to send the original")
	return adjusted, true
}

type inputImageTurnKey struct{}

type inputImageTurn struct {
	paths    []string
	adjusted bool
}
