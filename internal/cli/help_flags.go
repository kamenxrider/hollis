package cli

import (
	"errors"
	"strings"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/mcp"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

// Help does not require arguments or access state, but supplied flag values
// must retain their normal semantic validation.
func installSemanticHelpChecks(root *cobra.Command, flags *rootFlags) {
	for _, child := range root.Commands() {
		installSemanticHelpChecks(child, flags)
	}
	switch root.CommandPath() {
	case "hollis doctor", "hollis models", "hollis chats search", "hollis batch run", "hollis batch resume", "hollis mcp add", "hollis image generate":
		flags.checkFlagsBeforeHelp(root, semanticHelpFlags)
	}
}

func semanticHelpFlags(cmd *cobra.Command, _ []string) error {
	f := cmd.Flags()
	text := func(name string) string { value, _ := f.GetString(name); return value }
	switch cmd.CommandPath() {
	case "hollis doctor":
		if f.Changed("model") {
			return validateSetupRoute(text("model"))
		}
	case "hollis models":
		if value := text("model"); value != "" && value != "local" {
			return usageErr(errors.New("--model supports only local for native-only discovery"))
		}
	case "hollis chats search":
		limit, _ := f.GetInt("limit")
		if limit < 1 || (text("model") != "" && !runner.Model(text("model")).Valid()) {
			return usageErr(errors.New("search requires a valid --model and --limit of at least 1"))
		}
	case "hollis batch run", "hollis batch resume":
		count := 1
		if f.Changed("max-calls") {
			count, _ = f.GetInt("max-calls")
		}
		retry, _ := f.GetBool("retry-uncertain")
		skip, _ := f.GetBool("skip-uncertain")
		if err := (batch.InvocationOptions{MaxCalls: count, RetryUncertain: retry, SkipUncertain: skip}).Validate(); err != nil {
			return usageErr(err)
		}
	case "hollis image generate":
		timeout, _ := f.GetDuration("timeout")
		if err := validateImageTimeout(cmd, timeout); err != nil {
			return err
		}
		if f.Changed("output") && (strings.TrimSpace(text("output")) == "" || !strings.HasSuffix(strings.ToLower(text("output")), ".png")) {
			return usageErr(errors.New("--output requires a PNG path"))
		}
		if err := imagegen.ValidateOutputOptions(imagegen.OutputOptions{AspectRatio: text("aspect-ratio"), Size: text("size"), Fit: text("fit")}); err != nil {
			return usageErr(err)
		}
	case "hollis mcp add":
		server := mcp.Server{Command: text("command"), URL: text("url"), Dir: text("dir"), Approval: text("approval")}
		if server.Command == "" && server.URL == "" && !f.Changed("command") && !f.Changed("url") {
			server.Command = "mcp-server"
		}
		var err error
		for _, field := range []struct {
			flag string
			dest *map[string]string
		}{{"env", &server.Env}, {"env-from", &server.EnvFrom}} {
			values, _ := f.GetStringArray(field.flag)
			if *field.dest, err = keyValues("--"+field.flag, values); err != nil {
				return err
			}
		}
		headers, _ := f.GetStringArray("header")
		if server.Headers, err = parseHeaderFlags(headers); err != nil {
			return err
		}
		if auth := text("auth"); auth != "" {
			if server.Auth, err = parseAuthFlag(auth); err != nil {
				return err
			}
		}
		server.Allow, _ = f.GetStringArray("allow")
		server.Tools, _ = f.GetStringArray("tool")
		for _, field := range []struct {
			flag string
			dest *int
		}{{"call-timeout", &server.CallTimeoutMS}, {"connect-timeout", &server.ConnectTimeoutMS}} {
			value, _ := f.GetDuration(field.flag)
			if *field.dest, err = durationMS(cmd, field.flag, value); err != nil {
				return err
			}
		}
		if err := mcp.ValidateServer("server", server); err != nil {
			return usageErr(err)
		}
	}
	return nil
}
