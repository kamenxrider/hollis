// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

// config holds persisted user preferences. Kept deliberately tiny and
// stdlib-only (RECAP §3.3: no viper); lives next to the chat database in
// os.UserConfigDir()/hollis/.
type config struct {
	// DefaultModel is the tier used when neither the positional
	// `model <tier>` prefix nor the --model flag is given.
	DefaultModel string `json:"default_model"`
	// Bridges overrides the bridge reference (name or UUID) invoked per
	// tier (results/macos-26-compat.md step 1). Keys are concrete tier
	// names; "auto" is not a tier with a bridge. Overrides beat the
	// `shortcuts list` name match and the compiled UUIDs.
	Bridges map[string]string `json:"bridges,omitempty"`
}

// configPath is a package var so tests can point it at a temp dir.
var configPath = defaultConfigPath

func defaultConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hollis", "config.json"), nil
}

// loadConfig reads the config file; a missing file yields the zero
// config (built-in defaults apply).
func loadConfig() (config, error) {
	var c config
	path, err := configPath()
	if err != nil {
		return c, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// saveConfig writes the config file (creating the directory).
func saveConfig(c config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// effectiveModel resolves the model tier in precedence order:
// positional `model <tier>` > explicit --model flag > config default >
// built-in default (auto). Explicit-flag detection uses cobra's
// Changed, so the flag's static default never masks the config value.
func effectiveModel(cmd *cobra.Command, flagModel, posModel string, hasPosModel bool) (runner.Model, error) {
	if hasPosModel {
		return runner.Model(posModel), nil
	}
	if cmd.Flags().Changed("model") {
		return runner.Model(flagModel), nil
	}
	c, err := loadConfig()
	if err != nil {
		return "", err
	}
	if c.DefaultModel != "" {
		return runner.Model(c.DefaultModel), nil
	}
	return runner.ModelAuto, nil
}

func newConfigCmd(flags *rootFlags, _ newRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or change persisted defaults",
		Long: `Inspect or change persisted defaults.

Settings live in a small JSON file next to the chat database. The
default model applies whenever neither the positional ` + "`model <tier>`" + `
prefix nor the --model flag is given.`,
		Example: `  hollis config show
  hollis config set model cloud-pro`,
	}
	cmd.AddCommand(newConfigShowCmd(flags))
	cmd.AddCommand(newConfigSetCmd(flags))
	return cmd
}

func newConfigShowCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the config file path and current settings",
		Example: `  hollis config show
  hollis config show --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return configErr(err)
			}
			c, err := loadConfig()
			if err != nil {
				return configErr(err)
			}
			if flags.asJSON {
				return printJSONFiltered(map[string]any{
					"path":          path,
					"default_model": c.DefaultModel,
					"bridges":       bridgesForShow(c),
				}, flags)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "config file: %s\n", path)
			defaultModel := c.DefaultModel
			if defaultModel == "" {
				defaultModel = string(runner.ModelAuto) + " (built-in default)"
			}
			fmt.Fprintf(w, "default model: %s\n", defaultModel)
			if len(c.Bridges) == 0 {
				fmt.Fprintf(w, "bridge overrides: none\n")
			} else {
				fmt.Fprintf(w, "bridge overrides:\n")
				for _, tier := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
					if ref, ok := c.Bridges[tier]; ok {
						fmt.Fprintf(w, "  %-10s %s\n", tier, ref)
					}
				}
			}
			return nil
		},
	}
}

func newConfigSetCmd(_ *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value...>",
		Short: "Set a persisted default (keys: model, bridge)",
		Example: `  hollis config set model cloud-pro
  hollis config set model auto
  hollis config set bridge cloud "AFM Bridge - Cloud"
  hollis config set bridge cloud-pro <UUID>
  hollis config set bridge cloud ""      # clears the override`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			c, err := loadConfig()
			if err != nil {
				return configErr(err)
			}
			switch key {
			case "model":
				if len(args) != 2 {
					return usageErr(errors.New("usage: hollis config set model <tier>"))
				}
				value := args[1]
				if !runner.Model(value).Valid() {
					return usageErr(fmt.Errorf("unknown model %q: choose auto, cloud, cloud-pro, on-device, or chatgpt", value))
				}
				// Explicit tiers must resolve on this machine (results/
				// macos-26-compat.md step 2); auto always applies.
				if m := runner.Model(value); m != runner.ModelAuto {
					resolved, err := resolveBridges(cmd.Context())
					if err != nil {
						return configErr(err)
					}
					if err := checkModelAvailable(resolved, m); err != nil {
						return err
					}
				}
				c.DefaultModel = value
				if err := saveConfig(c); err != nil {
					return configErr(err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "default model set to %s\n", value)
				return nil
			case "bridge":
				if len(args) != 3 {
					return usageErr(errors.New("usage: hollis config set bridge <tier> <name-or-uuid>"))
				}
				tier, ref := runner.Model(args[1]), args[2]
				if !tier.Valid() || tier == runner.ModelAuto {
					return usageErr(fmt.Errorf("unknown bridge tier %q: choose cloud, cloud-pro, on-device, or chatgpt", tier))
				}
				if c.Bridges == nil {
					c.Bridges = map[string]string{}
				}
				if strings.TrimSpace(ref) == "" {
					delete(c.Bridges, string(tier))
					if err := saveConfig(c); err != nil {
						return configErr(err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "bridge override cleared for %s\n", tier)
					return nil
				}
				c.Bridges[string(tier)] = ref
				if err := saveConfig(c); err != nil {
					return configErr(err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "bridge for %s set to %s\n", tier, ref)
				return nil
			default:
				return usageErr(fmt.Errorf("unknown config key %q: only \"model\" and \"bridge\" are supported", key))
			}
		},
	}
	return cmd
}

// bridgesForShow renders the bridge override map for JSON output; an empty
// map marshals as {} instead of null.
func bridgesForShow(c config) map[string]string {
	out := map[string]string{}
	for tier, ref := range c.Bridges {
		out[tier] = ref
	}
	return out
}
