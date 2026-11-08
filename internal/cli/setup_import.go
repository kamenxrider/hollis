// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/subproc"
	"github.com/spf13/cobra"
)

var setupImportExecutable = os.Executable

var importRoutes = []string{"cloud", "cloud-pro", "on-device", "chatgpt", "image"}

var importNames = map[string]string{
	"cloud": "AFM Bridge - Cloud", "cloud-pro": "AFM Bridge - Cloud Pro",
	"on-device": "AFM Bridge - On-Device", "chatgpt": "AFM Bridge - ChatGPT",
	"image": imageSetupBridge,
}

const importNextStep = "Choose Add Shortcut in each window, then run hollis doctor"

type setupImportResult struct {
	SchemaVersion   int      `json:"schema_version"`
	Source          string   `json:"source"`
	Status          string   `json:"status"`
	Opened          []string `json:"opened"`
	Skipped         []string `json:"skipped"`
	ImportFolder    string   `json:"import_folder,omitempty"`
	Message         string   `json:"message"`
	InferenceTested bool     `json:"inference_tested"`
}

func selectedImportRoutes(args []string) ([]string, error) {
	if len(args) == 0 {
		return slices.Clone(importRoutes), nil
	}
	var selected []string
	for _, route := range args {
		if _, ok := importNames[route]; !ok {
			return nil, usageErr(errors.New("--import routes must be cloud, cloud-pro, on-device, chatgpt or image"))
		}
		if !slices.Contains(selected, route) {
			selected = append(selected, route)
		}
	}
	return selected, nil
}

// The release bundle and Homebrew's versioned libexec both keep the archive
// and manifest next to the real executable, not next to its public bin symlink.
func installedBridgeArchive() ([]byte, error) {
	executable, err := setupImportExecutable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(executable)
	metadata, err := readImportFile(filepath.Join(dir, "manifest.json"), 1<<20)
	if err != nil {
		return nil, errors.New("installed bundle manifest not found; install the complete Hollis bundle or Homebrew formula")
	}
	var manifest struct {
		Version string            `json:"version"`
		SHA256  map[string]string `json:"sha256"`
	}
	decoder := json.NewDecoder(bytes.NewReader(metadata))
	if err := decoder.Decode(&manifest); err != nil {
		return nil, errors.New("invalid installed bundle manifest")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) || manifest.Version != version {
		return nil, errors.New("installed bundle manifest version mismatch or trailing data")
	}
	expected := manifest.SHA256["hollis-bridges.zip"]
	if len(expected) != 64 || strings.Trim(expected, "0123456789abcdef") != "" {
		return nil, errors.New("installed bundle manifest has no valid bridge SHA-256")
	}
	data, err := readImportFile(filepath.Join(dir, "hollis-bridges.zip"), 16<<20)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != expected {
		return nil, errors.New("installed bridge archive SHA-256 mismatch; no imports opened")
	}
	return data, nil
}

func readImportFile(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("import input must be a bounded regular file, not a symlink")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("import input exceeds size limit")
	}
	return data, err
}

// Validate the entire archive before extracting even a selected member.
func checkedBridgeZip(data []byte) (map[string]*zip.File, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := make(map[string]*zip.File)
	for _, f := range archive.File {
		valid := false
		for _, name := range importNames {
			valid = valid || f.Name == name+".shortcut"
		}
		if !valid || files[f.Name] != nil || !f.Mode().IsRegular() || f.UncompressedSize64 > 8<<20 {
			return nil, errors.New("unsafe bridge archive member; expected exactly the five bundled regular Shortcut files")
		}
		files[f.Name] = f
	}
	if len(files) != len(importRoutes) {
		return nil, errors.New("bridge archive must contain exactly five bundled Shortcuts")
	}
	return files, nil
}

func runImportTool(ctx context.Context, name string, args ...string) error {
	result, err := subproc.Run(ctx, subproc.Spec{
		Name: name, Args: args, Timeout: 60 * time.Second, StdoutCap: 4096, StderrCap: 4096,
	})
	if err != nil || result.WaitErr != nil {
		return transportErr(fmt.Errorf("%s failed; no automatic retry", filepath.Base(name)))
	}
	return nil
}

func runSetupImport(cmd *cobra.Command, flags *rootFlags, args []string, yes, force bool) error {
	selected, err := selectedImportRoutes(args)
	if err != nil {
		return err
	}
	data, err := installedBridgeArchive()
	if err != nil {
		return configErr(err)
	}
	files, err := checkedBridgeZip(data)
	if err != nil {
		return configErr(err)
	}
	c, err := readSetupConfig()
	if err != nil {
		return configErr(err)
	}
	shortcuts, err := exec.LookPath("shortcuts")
	if err != nil {
		return transportErr(errors.New("Shortcuts transport not found"))
	}
	discovery := runner.New()
	discovery.ShortcutsPath = shortcuts
	names, err := discovery.ListShortcuts(cmd.Context())
	if err != nil {
		return transportErr(errors.New("could not list Shortcuts; no bridge is proven missing and no imports opened"))
	}
	resolved := runner.ResolveBridges(names, true, macosMajorVersion(), bridgeOverrides(c))
	result := setupImportResult{SchemaVersion: 1, Source: "bundle-import-v1", Status: "unchanged",
		Opened: []string{}, Skipped: []string{}, Message: importNextStep}
	var needed []string
	for _, route := range selected {
		found := slices.Contains(names, importNames[route])
		if route == "image" {
			found = found || (c.ImageBridge != "" && slices.Contains(names, c.ImageBridge))
		} else {
			found = found || resolved[runner.Model(route)].Verified
		}
		if found && !force {
			result.Skipped = append(result.Skipped, route)
		} else {
			needed = append(needed, route)
		}
	}
	configureImage := slices.Contains(selected, "image") && c.ImageBridge == ""
	var opener string
	if len(needed) > 0 {
		opener, err = exec.LookPath("open")
		if err != nil {
			return transportErr(errors.New("open command not found"))
		}
	}
	// This exact list always precedes all writes, signing and windows, even
	// with --yes or JSON output. Keep stdout a single machine-readable result.
	fmt.Fprintln(cmd.ErrOrStderr(), "Shortcut imports to open:")
	for _, route := range needed {
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s: %s\n", route, importNames[route])
	}
	if len(needed) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "  (none; selected bridges already installed)")
	}
	if configureImage {
		fmt.Fprintf(cmd.ErrOrStderr(), "Set image-bridge if still unset: %s\n", imageSetupBridge)
	}
	if !yes && (flags.noInput || flags.agent || flags.asJSON || !interactiveStdin()) {
		return usageErr(errors.New("--import requires --yes without interactive input; no imports opened"))
	}
	if len(needed) == 0 && !configureImage {
		return printSetupImport(cmd, flags, result)
	}
	if !yes {
		if len(needed) == 0 {
			fmt.Fprint(cmd.ErrOrStderr(), "Set the bundled image bridge? [y/N] ")
		} else {
			fmt.Fprint(cmd.ErrOrStderr(), "Open these Add Shortcut windows? [y/N] ")
		}
		answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if err != nil || (answer != "y" && answer != "yes") {
			return usageErr(errors.New("import canceled; no imports opened"))
		}
	}
	if len(needed) == 0 {
		if err := setDefaultImportImageBridge(); err != nil {
			return err
		}
		result.Status = "configured"
		return printSetupImport(cmd, flags, result)
	}
	stage, err := os.MkdirTemp("", "hollis-bridge-sign-")
	if err != nil {
		return configErr(err)
	}
	defer os.RemoveAll(stage)
	configFile, err := configPath()
	if err != nil {
		return configErr(err)
	}
	// Private, fresh subdirectories prevent reuse or symlink replacement of
	// a previous import. Retain signed copies for asynchronous Add dialogs.
	parent := filepath.Join(filepath.Dir(configFile), "bridge-imports")
	if err := ensureConfigDir(filepath.Dir(configFile)); err != nil {
		return configErr(err)
	}
	if err := ensureConfigDir(parent); err != nil {
		return configErr(err)
	}
	imports, err := os.MkdirTemp(parent, "bundle-")
	if err != nil {
		return configErr(err)
	}
	retain := false
	defer func() {
		if !retain {
			_ = os.RemoveAll(imports)
		}
	}()
	for _, route := range needed {
		name := importNames[route] + ".shortcut"
		f := files[name]
		reader, err := f.Open()
		if err != nil {
			return configErr(err)
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || len(content) > 8<<20 {
			return configErr(errors.New("could not read bounded bridge archive member"))
		}
		input := filepath.Join(stage, name)
		if err := os.WriteFile(input, content, 0o600); err != nil {
			return configErr(err)
		}
		output := filepath.Join(imports, name)
		if err := runImportTool(cmd.Context(), shortcuts, "sign", "--mode", "anyone", "--input", input, "--output", output); err != nil {
			return err
		}
		info, err := os.Lstat(output)
		if err != nil || !info.Mode().IsRegular() {
			return configErr(errors.New("signed bridge must be a regular file in the private import folder"))
		}
		if err := os.Chmod(output, 0o600); err != nil {
			return configErr(err)
		}
	}
	if configureImage {
		if err := setDefaultImportImageBridge(); err != nil {
			return err
		}
	}
	result.ImportFolder = imports
	// Once any window may be launched, leave all signed files available.
	retain = true
	for _, route := range needed {
		if err := runImportTool(cmd.Context(), opener, filepath.Join(imports, importNames[route]+".shortcut")); err != nil {
			return err
		}
		result.Opened = append(result.Opened, route)
	}
	result.Status = "approval_required"
	return printSetupImport(cmd, flags, result)
}

func setDefaultImportImageBridge() error {
	if err := updateConfig(func(current *config) error {
		if current.ImageBridge == "" {
			current.ImageBridge = imageSetupBridge
		}
		return nil
	}); err != nil {
		return configErr(err)
	}
	return nil
}

func printSetupImport(cmd *cobra.Command, flags *rootFlags, result setupImportResult) error {
	if flags.asJSON {
		row := map[string]any{
			"schema_version": result.SchemaVersion, "source": result.Source, "status": result.Status,
			"opened": result.Opened, "skipped": result.Skipped, "message": result.Message,
			"inference_tested": result.InferenceTested,
		}
		if result.ImportFolder != "" {
			row["import_folder"] = result.ImportFolder
		}
		return printJSONFilteredTo(cmd.OutOrStdout(), row, flags)
	}
	for _, route := range result.Skipped {
		fmt.Fprintf(cmd.OutOrStdout(), "Skipped installed bridge: %s\n", route)
	}
	fmt.Fprintln(cmd.OutOrStdout(), result.Message)
	return nil
}
