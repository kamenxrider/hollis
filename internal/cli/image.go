// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/spf13/cobra"
)

// newImageCmd uses an injected generator so tests never touch a real bridge.
func newImageCmd(flags *rootFlags, generator imagegen.Generator) *cobra.Command {
	imageCmd := &cobra.Command{
		Use:   "image",
		Short: "Experimental image generation through an explicit bridge",
		Long: `Experimental image generation through an explicit bridge.

Requires an installed Image Playground Shortcut that accepts text and returns
an image. Choose --style to select its configured bridge, or pass an explicit
name with --bridge. Style and provider choice are set in the selected Shortcut.
Run image styles to inspect mappings. No automatic backend or fallback is used.
See docs/image-generation.md for the tested setup.`,
		Args: noExtraArgs("image"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.asJSON {
				return usageErr(errors.New("image requires a subcommand in JSON or agent mode"))
			}
			return cmd.Help()
		},
	}
	imageCmd.AddCommand(newImageGenerateCmd(flags, generator))
	imageCmd.AddCommand(newImageStylesCmd(flags))
	return imageCmd
}

func newImageGenerateCmd(flags *rootFlags, generator imagegen.Generator) *cobra.Command {
	var (
		bridge        string
		style         string
		output        string
		timeout       time.Duration
		outputOptions imagegen.OutputOptions
	)
	cmd := &cobra.Command{
		Use:   "generate <prompt>",
		Short: "Generate one PNG image and save it without overwriting files",
		Long: `Generate one PNG image and save it without overwriting files.

The destination is checked before and during publication. The bridge is
explicit or selected through the configured --style. The prompt is one positional
argument, and the output path must have a .png extension. Model flags and automatic fallback are intentionally
unsupported. One invocation makes one generation attempt with no retry.

Requires an installed image Shortcut: Description = Shortcut Input, then
Stop and Output = Image. The tested setup uses Animation, no Photo, Save to
Playground Never, and Do Nothing when there is nowhere to output. Other styles
and first-run permission behavior require validation on your Mac. Hollis never
answers a macOS permission dialog; --no-input does not suppress those dialogs.`,
		Example: `  hollis image generate "A red bicycle beside a blue wall" --bridge "Hollis Image Generation Probe" --output bicycle.png`,
		Args: func(cmd *cobra.Command, args []string) error {
			// Deferred help is rendered after argument validation. Permit a
			// bare help request while preserving validation for supplied args.
			if flag := cmd.Flags().Lookup("help"); flag != nil {
				if help, ok := flag.Value.(*deferredHelpValue); ok && *help.requested && len(args) == 0 {
					return nil
				}
			}
			if len(args) != 1 {
				return usageErr(errors.New("image generate takes exactly one positional prompt"))
			}
			if strings.TrimSpace(args[0]) == "" {
				return usageErr(errors.New("image prompt is empty"))
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
			if !cmd.Flags().Changed("output") || strings.TrimSpace(output) == "" {
				return usageErr(errors.New("an --output PNG path is required"))
			}
			if err := imagegen.ValidateOutputOptions(outputOptions); err != nil {
				return usageErr(err)
			}
			if err := imagegen.PreflightDestination(output); err != nil {
				return toImageCLIError(err)
			}

			resolved, err := resolveImageBridgeRequest(style, bridge)
			if err != nil {
				return err
			}
			runCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			result, err := generator.Generate(runCtx, imagegen.Request{
				Prompt:    args[0],
				BridgeRef: resolved.Ref,
				Style:     resolved.Style,
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

			nativeWidth, nativeHeight := result.Width, result.Height
			processed, err := imagegen.TransformOutput(runCtx, result, outputOptions)
			if err != nil {
				return toImageCLIError(err)
			}
			result = processed
			published, err := imagegen.Publish(result.Path, output)
			if err != nil {
				return toImageCLIError(err)
			}

			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
					"path":         published.Path,
					"format":       published.Format,
					"bytes":        published.Bytes,
					"width":        published.Width,
					"height":       published.Height,
					"checksum":     published.SHA256,
					"native_width": nativeWidth, "native_height": nativeHeight,
					"output_processing": outputOptions,
				}, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved PNG to %s\n", published.Path)
			return nil
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageErr(fmt.Errorf("invalid image flags: %w", err))
	})
	cmd.Flags().StringVar(&bridge, "bridge", "", "Explicit image bridge reference; mutually exclusive with --style")
	cmd.Flags().StringVar(&style, "style", "", "Configured image style: any, animation (default), genmoji, illustration, sketch, chatgpt")
	cmd.Flags().StringVar(&output, "output", "", "PNG destination; an existing file or symlink is never replaced")
	cmd.Flags().StringVar(&outputOptions.AspectRatio, "aspect-ratio", "", "Output ratio W:H; requires --fit crop|pad (post-processing, not a model setting)")
	cmd.Flags().StringVar(&outputOptions.Size, "size", "", "Output size WIDTHxHEIGHT; requires --fit crop|pad, mutually exclusive with --aspect-ratio")
	cmd.Flags().StringVar(&outputOptions.Fit, "fit", "", "Explicit output processing: crop or pad; requires --aspect-ratio or --size")
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
	case imagegen.KindSessionLocked:
		return transportErr(fmt.Errorf("%w; unlock or check the active session and retry manually", imagegen.ErrSessionLocked))
	case imagegen.KindNonZeroExit:
		return transportErr(fmt.Errorf("%w\nhint: verify --bridge names an installed image-generation Shortcut; see docs/image-generation.md for setup", err))
	case imagegen.KindSpawn, imagegen.KindNoOutput,
		imagegen.KindInvalidPNG, imagegen.KindImageTooLarge, imagegen.KindCleanup,
		imagegen.KindOutputInspection:
		return transportErr(err)
	default:
		return transportErr(err)
	}
}
