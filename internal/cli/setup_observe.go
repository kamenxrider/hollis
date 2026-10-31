// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/setup"
)

const imageSetupBridge = "Hollis Image - Reference Input v2"

// Setup observations must not repair file permissions, create state or take a
// write lock. Keep the normal configuration loader's maintenance separate.
func readSetupConfig() (config, error) {
	path, err := configPath()
	if err != nil {
		return config{}, err
	}
	if info, err := os.Lstat(filepath.Dir(path)); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return config{}, errors.New("config directory must not be a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return config{}, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return config{}, nil
	}
	if err != nil {
		return config{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return config{}, errors.New("configuration must be a regular file no larger than 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	decoder.DisallowUnknownFields()
	var c config
	if err := decoder.Decode(&c); err != nil {
		return config{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return config{}, errors.New("configuration must contain one object")
	}
	return c, validateConfig(c)
}

func observeSetupRoute(ctx context.Context, route string, newRunner newRunnerFunc) (setup.Observations, map[string]any, error) {
	o, report, err := collectSetupFacts(ctx, route, newRunner)
	p := setup.Diagnose(o)
	for key, value := range planRow(p) {
		report[key] = value
	}
	if err == nil {
		err = setupPlanError(p)
	}
	return o, report, err
}

func collectSetupFacts(ctx context.Context, route string, newRunner newRunnerFunc) (setup.Observations, map[string]any, error) {
	o := setup.Observations{Route: route, Platform: "unknown", Transport: "unknown",
		Bridge: setup.BridgeFacts{Discovery: "not_attempted"}}
	report := map[string]any{"route": route, "artifact_verification": "unknown", "inference_tested": false}
	major := macosMajorVersion()
	if major > 0 {
		o.Platform = "supported"
	}
	if (route == "cloud-pro" || route == "local") && major > 0 && major < runner.MeasuredOSMajor {
		o.Platform = "unsupported"
	}
	if o.Platform != "supported" {
		return o, report, nil
	}
	if route == "local" {
		r := newRunner()
		_, checked := r.(runner.NativeStatusRunner)
		status, err := probeNativeStatus(ctx, r)
		o.Native = setup.NativeFacts{Checked: checked, Available: status.Available, Failed: err != nil, Version: status.Version, Protocol: status.Protocol}
		report["local"] = localModelRow(status, err)
		if err != nil {
			return o, report, toCLIError(err)
		}
		return o, report, nil
	}
	c, err := readSetupConfig()
	if err != nil {
		return o, report, nil
	}
	o.ConfigKnown = true
	if route == "image" {
		o.Bridge.Reference, o.Bridge.Configured = c.ImageBridge, c.ImageBridge != ""
		if o.Bridge.Reference == "" {
			o.Bridge.Reference = imageSetupBridge
		}
	} else {
		o.Bridge.Reference = runner.ResolveBridges(nil, false, major, bridgeOverrides(c))[runner.Model(route)].Ref
		o.Bridge.Configured = c.Bridges[route] != ""
	}
	if _, err := lookPath(runner.DefaultShortcutsPath); err != nil {
		o.Transport = "missing"
		return o, report, transportErr(errors.New("Shortcuts transport not found"))
	}
	o.Transport = "available"
	names, err := listInstalledShortcuts(ctx)
	if err != nil {
		o.Bridge.Discovery = "failed"
		return o, report, transportErr(errors.New("could not list Shortcuts; no bridge is proven missing"))
	}
	o.Bridge.Discovery = "completed"
	if route == "image" {
		o.Bridge.Discovered = slices.Contains(names, o.Bridge.Reference)
	} else {
		model := runner.Model(route)
		rb := runner.ResolveBridges(names, true, major, bridgeOverrides(c))[model]
		o.Bridge.Reference, o.Bridge.Configured, o.Bridge.Discovered = rb.Ref, c.Bridges[route] != "", rb.Verified
	}
	return o, report, nil
}

func bridgeFactsRow(b setup.BridgeFacts) map[string]any {
	return map[string]any{"discovery": b.Discovery, "configured": b.Configured, "reference": b.Reference, "discovered": b.Discovered}
}
