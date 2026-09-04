// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
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
	// tier. Keys are concrete tier
	// names; "auto" is not a tier with a bridge. Overrides beat the
	// `shortcuts list` name match and the compiled UUIDs.
	Bridges map[string]string `json:"bridges,omitempty"`
}

// configPath is a package var so tests can point it at a temp dir.
var configPath = defaultConfigPath

func defaultConfigPath() (string, error) {
	base, err := store.DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "config.json"), nil
}

// loadConfig reads the config file; a missing file yields the zero
// config (built-in defaults apply).
func loadConfig() (config, error) {
	path, err := configPath()
	if err != nil {
		return config{}, err
	}
	return loadConfigAt(path)
}

func loadConfigAt(path string) (config, error) {
	var c config
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() {
		return c, fmt.Errorf("config %s must be a regular file", path)
	}
	if err := ensureConfigDir(filepath.Dir(path)); err != nil {
		return c, err
	}
	if err := file.Chmod(0o600); err != nil {
		return c, fmt.Errorf("protect config %s: %w", path, err)
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return c, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("parse %s: expected exactly one JSON object", path)
	}
	if err := validateConfig(c); err != nil {
		return c, fmt.Errorf("validate %s: %w", path, err)
	}
	return c, nil
}

func validateConfig(c config) error {
	if c.DefaultModel != "" && !runner.Model(c.DefaultModel).Valid() {
		return fmt.Errorf("unknown default model %q", c.DefaultModel)
	}
	for tier, ref := range c.Bridges {
		model := runner.Model(tier)
		if !model.Valid() || model == runner.ModelAuto {
			return fmt.Errorf("unknown bridge tier %q", tier)
		}
		if strings.TrimSpace(ref) == "" || strings.HasPrefix(strings.TrimSpace(ref), "-") {
			return fmt.Errorf("invalid bridge reference for %s", tier)
		}
	}
	return nil
}

// saveConfig writes the config file atomically under the cross-process lock.
func saveConfig(c config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := validateConfig(c); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return withConfigLock(path, func() error {
		return atomicWriteConfig(path, append(raw, '\n'))
	})
}

// updateConfig performs a read-modify-write while holding one exclusive lock.
// Bridge discovery must happen before this helper is called; it can invoke an
// external process and should never hold the config lock while doing so.
func updateConfig(update func(*config) error) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return withConfigLock(path, func() error {
		c, err := loadConfigAt(path)
		if err != nil {
			return err
		}
		if err := update(&c); err != nil {
			return err
		}
		if err := validateConfig(c); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		return atomicWriteConfig(path, append(raw, '\n'))
	})
}

func withConfigLock(path string, fn func() error) error {
	if err := ensureConfigDir(filepath.Dir(path)); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open config lock %s: %w", lockPath, err)
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return fmt.Errorf("protect config lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock config %s: %w", path, err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func ensureConfigDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if dir != "." {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("config state path %s must be a real directory, not a symlink", dir)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func atomicWriteConfig(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensureConfigDir(dir); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".config.json-*")
	if err != nil {
		return err
	}
	tempPath := f.Name()
	defer func() {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	tempPath = ""
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return syncConfigDir(dir)
}

func syncConfigDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// effectiveModel resolves the model tier in precedence order:
// positional `model <tier>` > explicit --model flag > config default >
// built-in default (auto). Explicit-flag detection uses cobra's
// Changed, so the flag's static default never masks the config value.
func effectiveModel(cmd *cobra.Command, flagModel, posModel string, hasPosModel bool) (runner.Model, error) {
	return effectiveModelWithDefault(cmd, flagModel, posModel, hasPosModel, runner.ModelAuto)
}

// effectiveModelWithDefault keeps the normal precedence while allowing a
// command to choose a safer built-in default. Image responses use Cloud
// directly because the normal auto strategy may fall back to an on-device
// tier that did not consume pixels in the measured Shortcuts transport.
func effectiveModelWithDefault(cmd *cobra.Command, flagModel, posModel string, hasPosModel bool, builtIn runner.Model) (runner.Model, error) {
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
	return builtIn, nil
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
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return usageErr(fmt.Errorf("unknown config command %q: run 'hollis config --help'", args[0]))
		},
		// Unknown subcommands are a usage error for agents, not help + exit 0.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if flags.asJSON {
					return usageErr(errors.New("config requires a subcommand in JSON or agent mode"))
				}
				return cmd.Help()
			}
			return usageErr(fmt.Errorf("unknown config command %q: run 'hollis config --help'", args[0]))
		},
	}
	cmd.AddCommand(newConfigShowCmd(flags))
	cmd.AddCommand(newConfigSetCmd(flags))
	return cmd
}

func newConfigShowCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the config file path and current settings",
		Args:  cobra.NoArgs,
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
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
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

func newConfigSetCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value...>",
		Short: "Set a persisted default (keys: model, bridge)",
		Example: `  hollis config set model cloud-pro
  hollis config set model auto
  hollis config set bridge cloud "AFM Bridge - Cloud"
  hollis config set bridge cloud-pro <UUID>
  hollis config set bridge cloud ""      # clears the override`,
		Args: validateConfigSetArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			switch key {
			case "model":
				if len(args) != 2 {
					return usageErr(errors.New("usage: hollis config set model <tier>"))
				}
				value := args[1]
				if !runner.Model(value).Valid() {
					return usageErr(fmt.Errorf("unknown model %q: choose auto, cloud, cloud-pro, on-device, or chatgpt", value))
				}
				// Explicit tiers must resolve on this machine; auto always applies.
				if m := runner.Model(value); m != runner.ModelAuto {
					resolved, err := resolveBridges(cmd.Context())
					if err != nil && !canAttemptAfterDiscoveryFailure(resolved, m) {
						return resolutionCLIError(err)
					}
					if err := checkModelAvailable(resolved, m); err != nil {
						return err
					}
				}
				if err := updateConfig(func(c *config) error {
					c.DefaultModel = value
					return nil
				}); err != nil {
					return configErr(err)
				}
				if flags.asJSON {
					return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
						"ok": true, "key": "default_model", "value": value,
					}, flags)
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
				if strings.TrimSpace(ref) == "" {
					if err := updateConfig(func(c *config) error {
						if c.Bridges != nil {
							delete(c.Bridges, string(tier))
						}
						return nil
					}); err != nil {
						return configErr(err)
					}
					if flags.asJSON {
						return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
							"ok": true, "key": "bridge", "tier": tier, "configured": false,
						}, flags)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "bridge override cleared for %s\n", tier)
					return nil
				}
				if strings.HasPrefix(strings.TrimSpace(ref), "-") {
					return usageErr(errors.New("bridge reference must not begin with '-'"))
				}
				if err := updateConfig(func(c *config) error {
					if c.Bridges == nil {
						c.Bridges = map[string]string{}
					}
					c.Bridges[string(tier)] = ref
					return nil
				}); err != nil {
					return configErr(err)
				}
				if flags.asJSON {
					return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
						"ok": true, "key": "bridge", "tier": tier, "configured": true, "ref": ref,
					}, flags)
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

func validateConfigSetArgs(_ *cobra.Command, args []string) error {
	if len(args) < 1 {
		return usageErr(errors.New("config set requires a key: model or bridge"))
	}
	switch args[0] {
	case "model":
		if len(args) != 2 {
			return usageErr(errors.New("usage: hollis config set model <tier>"))
		}
		if !runner.Model(args[1]).Valid() {
			return usageErr(fmt.Errorf("unknown model %q: choose auto, cloud, cloud-pro, on-device, or chatgpt", args[1]))
		}
	case "bridge":
		if len(args) != 3 {
			return usageErr(errors.New("usage: hollis config set bridge <tier> <name-or-uuid>"))
		}
		tier := runner.Model(args[1])
		if !tier.Valid() || tier == runner.ModelAuto {
			return usageErr(fmt.Errorf("unknown bridge tier %q: choose cloud, cloud-pro, on-device, or chatgpt", tier))
		}
		if strings.HasPrefix(strings.TrimSpace(args[2]), "-") {
			return usageErr(errors.New("bridge reference must not begin with '-'"))
		}
	default:
		return usageErr(fmt.Errorf("unknown config key %q: only \"model\" and \"bridge\" are supported", args[0]))
	}
	return nil
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
