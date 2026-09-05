// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/spf13/cobra"
)

// newImageCmd builds the experimental image command without registering it on
// the root command. The coordinator owns root registration and public feature
// claims; the generator is injected so tests never touch Shortcuts or a bridge.
func newImageCmd(flags *rootFlags, generator imagegen.Generator) *cobra.Command {
	imageCmd := &cobra.Command{
		Use:   "image",
		Short: "Experimental image generation through an explicit bridge",
		Long: `Experimental image generation through an explicit bridge.

This command is offline scaffolding and is not registered on the root command
while the image Shortcut feasibility gate is unresolved. It has no automatic
bridge discovery, model selection, or fallback. The generator is injected by
the caller, which keeps this surface provider-free for tests.`,
	}
	imageCmd.AddCommand(newImageGenerateCmd(flags, generator))
	return imageCmd
}

func newImageGenerateCmd(flags *rootFlags, generator imagegen.Generator) *cobra.Command {
	var (
		bridge  string
		output  string
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "generate <prompt>",
		Short: "Generate one PNG image and save it without overwriting files",
		Long: `Generate one PNG image and save it without overwriting files.

The destination is checked before and during publication. The bridge is
explicit, the prompt is one positional argument, and the output path must
have a .png extension. Model flags and automatic fallback are intentionally
unsupported. This command remains experimental until an unattended bridge
has been proven on the target system.`,
		Example: `  hollis image generate "A red bicycle beside a blue wall" --bridge "Synthetic Image Bridge" --output bicycle.png`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageErr(errors.New("image generate takes exactly one positional prompt"))
			}
			if strings.TrimSpace(args[0]) == "" {
				return usageErr(errors.New("image prompt is empty"))
			}
			if !cmd.Flags().Changed("bridge") || strings.TrimSpace(bridge) == "" {
				return usageErr(errors.New("an explicit --bridge is required"))
			}
			if !cmd.Flags().Changed("output") || strings.TrimSpace(output) == "" {
				return usageErr(errors.New("an --output PNG path is required"))
			}
			return validateImageTimeout(cmd, timeout)
		},
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			if err := validateImageTimeout(cmd, timeout); err != nil {
				return err
			}
			if !cmd.Flags().Changed("bridge") || strings.TrimSpace(bridge) == "" {
				return usageErr(errors.New("an explicit --bridge is required"))
			}
			if !cmd.Flags().Changed("output") || strings.TrimSpace(output) == "" {
				return usageErr(errors.New("an --output PNG path is required"))
			}
			if err := imagegen.PreflightDestination(output); err != nil {
				return toImageCLIError(err)
			}

			result, err := generator.Generate(cmd.Context(), imagegen.Request{
				Prompt:    args[0],
				BridgeRef: bridge,
				Timeout:   timeout,
			})
			if err != nil {
				return toImageCLIError(err)
			}
			defer func() {
				if cleanupErr := result.Cleanup(); cleanupErr != nil {
					// The staged file is owned by the result; a cleanup
					// failure must not disappear silently.
					if runErr == nil {
						runErr = toImageCLIError(cleanupErr)
					}
				}
			}()

			published, err := imagegen.Publish(result.Path, output)
			if err != nil {
				return toImageCLIError(err)
			}

			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
					"path":     published.Path,
					"format":   published.Format,
					"bytes":    published.Bytes,
					"width":    published.Width,
					"height":   published.Height,
					"checksum": published.SHA256,
				}, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved PNG to %s\n", published.Path)
			return nil
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageErr(fmt.Errorf("invalid image flags: %w", err))
	})
	cmd.Flags().StringVar(&bridge, "bridge", "", "Explicit image bridge reference passed to Shortcuts")
	cmd.Flags().StringVar(&output, "output", "", "PNG destination; an existing file or symlink is never replaced")
	cmd.Flags().DurationVar(&timeout, "timeout", imagegen.MaxTimeout, "Per-call timeout (default 120s, ceiling 120s)")
	return cmd
}

func validateImageTimeout(cmd *cobra.Command, timeout time.Duration) error {
	if !cmd.Flags().Changed("timeout") {
		return nil
	}
	if timeout <= 0 || timeout > imagegen.MaxTimeout {
		return usageErr(fmt.Errorf("invalid --timeout %s: choose a duration greater than zero and no more than %s", timeout, imagegen.MaxTimeout))
	}
	return nil
}

func toImageCLIError(err error) error {
	var imageErr *imagegen.Error
	if !errors.As(err, &imageErr) {
		if err == nil {
			return nil
		}
		return transportErr(err)
	}
	switch imageErr.Kind {
	case imagegen.KindUsage, imagegen.KindEmptyPrompt, imagegen.KindInvalidPrompt, imagegen.KindMissingBridge:
		return usageErr(err)
	case imagegen.KindTimeout:
		return timeoutErr(err)
	case imagegen.KindCanceled:
		return transportErr(err)
	case imagegen.KindSpawn, imagegen.KindNonZeroExit, imagegen.KindNoOutput,
		imagegen.KindInvalidPNG, imagegen.KindImageTooLarge, imagegen.KindCleanup,
		imagegen.KindOutputInspection:
		return transportErr(err)
	default:
		return transportErr(err)
	}
}
