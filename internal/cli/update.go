// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const updateEndpoint = "https://api.github.com/repos/kamenxrider/hollis/releases/latest"
const updateReleasesURL = "https://github.com/kamenxrider/hollis/releases"
const updateBrewCommand = "brew upgrade kamenxrider/tap/hollis"
const updateInstallerCommand = "curl -fsSLo install-hollis.sh https://raw.githubusercontent.com/kamenxrider/hollis/main/scripts/install.sh && bash install-hollis.sh"
const updateTimeout = 10 * time.Second

type updateDependencies struct {
	client     *http.Client
	endpoint   string
	current    string
	executable func() (string, error)
	home       func() (string, error)
}

func newUpdateCmd(flags *rootFlags) *cobra.Command {
	return newUpdateCmdWithDependencies(flags, updateDependencies{
		client: &http.Client{
			Timeout: updateTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoint: updateEndpoint, current: version, executable: os.Executable, home: os.UserHomeDir,
	})
}

func newUpdateCmdWithDependencies(flags *rootFlags, deps updateDependencies) *cobra.Command {
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a newer stable release; upgrade Homebrew only with --apply",
		Long: `Check GitHub for the latest stable Hollis release (10s timeout).
Only this explicit command checks for updates; there are no automatic checks.
Print the current and latest versions, release URL and upgrade command.
An available update is success (exit 0), not an error.

--apply runs only 'brew upgrade kamenxrider/tap/hollis' for a detected
Homebrew installation, after confirmation. Non-interactive, --json,
--no-input and --agent invocations require --yes. Installer and manual
installations are never changed; their upgrade command or link is printed.
Close running Hollis chat and serve processes before upgrading.`,
		Args: noExtraArgs("update"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if yes && !apply {
				return usageErr(errors.New("--yes requires --apply"))
			}
			executable, _ := deps.executable()
			home, _ := deps.home()
			method := detectUpdateInstallation(executable, home)
			upgrade := updateUpgradeCommand(method)
			if apply && method != "homebrew" {
				return &cliError{code: 2, err: fmt.Errorf("--apply supports Homebrew installations only; no changes made\nUpgrade: %s", upgrade), suggestion: upgrade}
			}
			if apply && !yes && (flags.noInput || flags.agent || flags.asJSON || !interactiveStdin()) {
				return usageErr(errors.New("--apply requires explicit --yes without interactive input; no changes made"))
			}
			result, err := checkUpdate(cmd.Context(), deps)
			if err != nil {
				return err
			}
			result["install_method"] = method
			result["upgrade_command"] = upgrade
			result["applied"] = false
			if apply && result["update_available"] == true {
				if !yes {
					fmt.Fprintf(cmd.ErrOrStderr(), "Run %s? Close running Hollis chat and serve processes first. [y/N] ", updateBrewCommand)
					answer, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if answer = strings.TrimSpace(answer); !strings.EqualFold(answer, "yes") && !strings.EqualFold(answer, "y") {
						return usageErr(errors.New("upgrade canceled; no changes made"))
					}
				}
				// No shell, installer execution or fallback. Keep brew output off
				// stdout so JSON remains exactly one Hollis result.
				brew := exec.CommandContext(cmd.Context(), "brew", "upgrade", "kamenxrider/tap/hollis")
				brew.Stdin = cmd.InOrStdin()
				brew.Stdout, brew.Stderr = cmd.ErrOrStderr(), cmd.ErrOrStderr()
				if err := brew.Run(); err != nil {
					return executionErr("update_apply_failed", "Homebrew upgrade failed; inspect stderr and run '"+updateBrewCommand+"' manually if appropriate")
				}
				result["applied"] = true
			}
			if flags.asJSON || flags.agent {
				return printJSONFilteredTo(cmd.OutOrStdout(), result, flags)
			}
			return printUpdateHuman(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "Upgrade a detected Homebrew installation after confirmation")
	cmd.Flags().BoolVar(&yes, "yes", false, "Approve --apply without prompting")
	return cmd
}

// The installer leaves no receipt. Its default directory plus executable
// paired helper is a conservative layout heuristic, not provenance verification.
// Custom --bin-dir and standalone ~/.local/bin copies remain manual.
func detectUpdateInstallation(executable, home string) string {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(resolved) {
		return "manual"
	}
	parts := strings.Split(filepath.Clean(resolved), string(filepath.Separator))
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "Cellar" && parts[i+1] == "hollis" && parts[i+2] != "" {
			return "homebrew"
		}
	}
	// macOS temporary homes may use /var while executable resolution returns
	// /private/var. Resolve both sides before comparing the default layout.
	resolvedHome, homeErr := filepath.EvalSymlinks(home)
	if home != "" && homeErr == nil && resolved == filepath.Join(resolvedHome, ".local", "bin", "hollis") {
		helper, err := os.Stat(filepath.Join(filepath.Dir(resolved), "hollis-native"))
		if err == nil && helper.Mode().IsRegular() && helper.Mode().Perm()&0111 != 0 {
			return "installer"
		}
	}
	return "manual"
}

func updateUpgradeCommand(method string) string {
	switch method {
	case "homebrew":
		return updateBrewCommand
	case "installer":
		return updateInstallerCommand
	default:
		return updateReleasesURL
	}
}

func checkUpdate(ctx context.Context, deps updateDependencies) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deps.endpoint, nil)
	if err != nil {
		return nil, transportErr(errors.New("could not prepare GitHub update check"))
	}
	req.Header.Set("User-Agent", "hollis/"+deps.current)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := deps.client.Do(req)
	if err != nil {
		return nil, updateNetworkError(err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotFound:
		return nil, notFoundErr(errors.New("no stable Hollis release is available on GitHub (or the repository is unavailable)"))
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, executionErr("rate_limited", "GitHub update check was rate-limited or access was denied (HTTP "+fmt.Sprint(response.StatusCode)+"); wait before checking again; no retry was made")
	case http.StatusOK:
	default:
		return nil, transportErr(fmt.Errorf("GitHub update check failed (HTTP %d); no retry was made", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, updateNetworkError(err)
	}
	var release struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if len(body) > 1<<20 || json.Unmarshal(body, &release) != nil {
		return nil, executionErr("update_response", "GitHub returned an invalid release response")
	}
	latest, valid := parseUpdateVersion(release.Tag)
	if !valid {
		return nil, executionErr("update_response", "GitHub returned a release without a valid semantic version")
	}
	if release.Draft || release.Prerelease || len(latest.pre) != 0 {
		return nil, notFoundErr(errors.New("GitHub returned a draft or prerelease; no stable release was selected"))
	}
	// Use only the canonical repository URL, never a server-supplied command.
	releaseURL := updateReleasesURL + "/tag/" + release.Tag
	if release.URL != releaseURL {
		return nil, executionErr("update_response", "GitHub returned an unexpected release URL")
	}
	current, comparable := parseUpdateVersion(deps.current)
	status, available := "unknown", false
	if comparable {
		switch compareUpdateVersions(current, latest) {
		case -1:
			status, available = "update_available", true
		case 0:
			status = "up_to_date"
		case 1:
			status = "newer_than_published"
		}
	}
	return map[string]any{
		"current_version": deps.current, "latest_version": strings.TrimPrefix(release.Tag, "v"),
		"update_available": available, "version_comparable": comparable, "status": status,
		"release_url": releaseURL,
	}, nil
}

func updateNetworkError(err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
		return timeoutErr(errors.New("GitHub update check timed out (10s maximum); no retry was made"))
	}
	return transportErr(errors.New("could not reach GitHub for the update check; check your connection; no retry was made"))
}

func printUpdateHuman(out io.Writer, result map[string]any) error {
	messages := map[string]string{
		"update_available":     "Update available.",
		"up_to_date":           "Already up to date.",
		"newer_than_published": "This build is newer than the published release; no downgrade is suggested.",
		"unknown":              "Current version is not semantic; availability could not be compared.",
	}
	_, err := fmt.Fprintf(out, "Current version: %s\nLatest stable: %s\n%s\nRelease: %s\nInstall method: %s\nUpgrade: %s\n",
		result["current_version"], result["latest_version"], messages[result["status"].(string)],
		result["release_url"], result["install_method"], result["upgrade_command"])
	if err == nil && result["applied"] == true {
		_, err = fmt.Fprintln(out, "Homebrew upgrade completed. Run 'hollis version' to check the installed version.")
	}
	return err
}
