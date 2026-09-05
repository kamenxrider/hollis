// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/spf13/cobra"
)

var imageStyles = []string{"any", "animation", "genmoji", "illustration", "sketch", "chatgpt"}

func validateImageBridges(bridges map[string]string) error {
	for style, ref := range bridges {
		if !slices.Contains(imageStyles, style) {
			return fmt.Errorf("unknown image style %q", style)
		}
		if strings.TrimSpace(ref) == "" || strings.HasPrefix(strings.TrimSpace(ref), "-") || strings.ContainsRune(ref, '\x00') {
			return fmt.Errorf("invalid image bridge reference for %s", style)
		}
	}
	return nil
}

type resolvedImageBridge struct {
	Ref      string
	Style    string
	Protocol string
}

func resolveImageBridgeRequest(style, explicitRef string) (resolvedImageBridge, error) {
	if explicitRef != "" {
		if style != "" {
			return resolvedImageBridge{}, usageErr(errors.New("choose --style through a configured image bridge, or an explicit bridge without a style"))
		}
		if err := validateImageBridges(map[string]string{"animation": explicitRef}); err != nil {
			return resolvedImageBridge{}, usageErr(err)
		}
		return resolvedImageBridge{Ref: explicitRef, Protocol: "text"}, nil
	}
	if style == "" {
		style = "animation"
	}
	if !slices.Contains(imageStyles, style) {
		return resolvedImageBridge{}, usageErr(fmt.Errorf("unknown image style %q: choose %s", style, strings.Join(imageStyles, ", ")))
	}
	c, err := loadConfig()
	if err != nil {
		return resolvedImageBridge{}, configErr(err)
	}
	ref := c.ImageBridges[style]
	if ref != "" {
		return resolvedImageBridge{Ref: ref, Protocol: "text"}, nil
	}
	if c.ImageBridge != "" {
		return resolvedImageBridge{Ref: c.ImageBridge, Style: style, Protocol: "json"}, nil
	}
	return resolvedImageBridge{}, usageErr(fmt.Errorf("image style %s has no configured bridge; use 'hollis config set image-bridge <shortcut-name>' or configure a fixed-style override", style))
}

// resolveImageBridge preserves the existing helper contract for callers that
// only need the resolved Shortcut reference.
func resolveImageBridge(style, explicitRef string) (string, error) {
	resolved, err := resolveImageBridgeRequest(style, explicitRef)
	return resolved.Ref, err
}

func newImageBridgeConfigCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "image-bridge <shortcut-name> | <style> <shortcut-name>",
		Short: "Configure one JSON image Shortcut or a fixed-style override",
		Long:  "With one argument, configure one parameterized Shortcut accepting prompt and style as JSON. With two arguments, preserve a fixed-style plain-text override. An empty name clears the selected setting.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 && len(args) != 2 {
				return usageErr(errors.New("expected image-bridge <shortcut-name> or image-bridge <style> <shortcut-name>"))
			}
			if len(args) == 2 && !slices.Contains(imageStyles, args[0]) {
				return usageErr(fmt.Errorf("unknown image style %q", args[0]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			style, ref := "", strings.TrimSpace(args[0])
			protocol := "json"
			if len(args) == 2 {
				style, ref = args[0], strings.TrimSpace(args[1])
				protocol = "text"
			}
			if err := updateConfig(func(c *config) error {
				if style == "" {
					c.ImageBridge = ref
				} else {
					if c.ImageBridges == nil {
						c.ImageBridges = map[string]string{}
					}
					if ref == "" {
						delete(c.ImageBridges, style)
					} else {
						c.ImageBridges[style] = ref
					}
				}
				return nil
			}); err != nil {
				return configErr(err)
			}
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"ok": true, "style": style, "bridge": ref, "protocol": protocol, "configured": ref != ""}, flags)
			}
			if style == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Image bridge (JSON): %s\n", ref)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Image style %s bridge: %s\n", style, ref)
			}
			return nil
		},
	}
}

func newImageStylesCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use: "styles", Short: "List known styles and explicit bridge mappings without running a model", Args: noExtraArgs("image styles"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := loadConfig()
			if err != nil {
				return configErr(err)
			}
			items := make([]map[string]any, 0, len(imageStyles))
			for _, style := range imageStyles {
				ref := c.ImageBridges[style]
				protocol := "text"
				if ref == "" {
					ref, protocol = c.ImageBridge, "json"
				}
				label, _ := imagegen.StyleLabel(style)
				items = append(items, map[string]any{"style": style, "native_style": label, "bridge": ref, "protocol": protocol, "configured": ref != "", "runtime_verified": false})
				if !flags.asJSON {
					status := "not configured"
					if ref != "" {
						status = ref
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%-14s %s\n", style, status)
				}
			}
			if flags.asJSON {
				return printJSONArrayFilteredTo(cmd.OutOrStdout(), items, flags)
			}
			return nil
		},
	}
}
