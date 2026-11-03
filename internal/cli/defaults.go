// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"os"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func parseDefaultTimeout(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 || d > runner.MaxTimeout {
		return 0, errors.New("invalid timeout: choose a duration greater than zero and no more than 2m0s (for example 45s)")
	}
	return d, nil
}

func effectiveTimeout(cmd *cobra.Command, flagValue, builtIn time.Duration) (time.Duration, error) {
	if cmd.Flags().Changed("timeout") {
		if err := validateTimeout(cmd, flagValue); err != nil {
			return 0, err
		}
		return flagValue, nil
	}
	if value, set := os.LookupEnv("HOLLIS_TIMEOUT"); set {
		d, err := parseDefaultTimeout(value)
		if err != nil {
			return 0, usageErr(errors.New("invalid HOLLIS_TIMEOUT: choose a duration greater than zero and no more than 120s"))
		}
		return d, nil
	}
	c, err := loadConfig()
	if err != nil {
		return 0, configErr(err)
	}
	if c.Timeout != "" {
		return parseDefaultTimeout(c.Timeout)
	}
	return builtIn, nil
}
