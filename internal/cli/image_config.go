// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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

// A style chooses a configured fixed-style bridge. Never treat a style as a
// magic prompt instruction or as an override for an arbitrary explicit bridge.
func resolveImageBridge(style, explicitRef string) (string, error) {
	if explicitRef != "" {
		if style != "" {
			return "", usageErr(errors.New("choose --style through a configured image bridge, or an explicit bridge without a style"))
		}
		if err := validateImageBridges(map[string]string{"animation": explicitRef}); err != nil {
			return "", usageErr(err)
		}
		return explicitRef, nil
	}
	if style == "" {
		style = "animation"
	}
	if !slices.Contains(imageStyles, style) {
		return "", usageErr(fmt.Errorf("unknown image style %q: choose %s", style, strings.Join(imageStyles, ", ")))
	}
	c, err := loadConfig()
	if err != nil {
		return "", configErr(err)
	}
	ref := c.ImageBridges[style]
	if ref == "" {
		return "", usageErr(fmt.Errorf("image style %s has no configured bridge; use 'hollis config set image-bridge %s <shortcut-name>' or pass an explicit bridge", style, style))
	}
	return ref, nil
}

func newImageBridgeConfigCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "image-bridge <style> <shortcut-name>",
		Short: "Map an image style to an installed fixed-style Shortcut",
		Long:  "Map an image style to an installed fixed-style Shortcut. This saves your explicit mapping; it does not inspect the action or prove its style. An empty name clears a mapping.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return usageErr(errors.New("expected image-bridge <style> <shortcut-name>"))
			}
			if !slices.Contains(imageStyles, args[0]) {
				return usageErr(fmt.Errorf("unknown image style %q", args[0]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			style, ref := args[0], strings.TrimSpace(args[1])
			if err := updateConfig(func(c *config) error {
				if c.ImageBridges == nil {
					c.ImageBridges = map[string]string{}
				}
				if ref == "" {
					delete(c.ImageBridges, style)
				} else {
					c.ImageBridges[style] = ref
				}
				return nil
			}); err != nil {
				return configErr(err)
			}
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"ok": true, "style": style, "bridge": ref, "configured": ref != ""}, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Image style %s bridge: %s\n", style, ref)
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
				items = append(items, map[string]any{"style": style, "bridge": ref, "configured": ref != "", "runtime_verified": false})
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
