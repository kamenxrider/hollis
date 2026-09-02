// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
)

// openStore opens the chat database; tests substitute a temp path.
var openStore = defaultOpenStore

func defaultOpenStore() (*store.Store, error) {
	path, err := store.DefaultPath()
	if err != nil {
		return nil, err
	}
	return store.Open(path)
}

// runTurn executes one conversation turn: build the replay transcript from
// stored messages, run the transport, store the user and assistant messages,
// and record run diagnostics either way (plan §12 runs table).
//
// The timeout is applied here, per turn, rather than by the caller wrapping
// its context once: the REPL passes one context across every turn, so a
// deadline set upstream would expire the whole session instead of bounding
// a single run. Zero means "no per-turn deadline", leaving the runner's own
// default in charge.
func runTurn(ctx context.Context, st *store.Store, conv store.Conversation, prompt string, newRunner newRunnerFunc, timeout time.Duration) (string, error) {
	history, err := st.Messages(conv.ID)
	if err != nil {
		return "", configErr(err)
	}
	transcript := chat.RenderTranscript(history, prompt)

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	r := newRunner()
	start := time.Now()
	text, err := r.Run(ctx, runner.Model(conv.Model), transcript)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		// Record the failed run too; never store secrets — only the error
		// class and a stderr excerpt (plan §12).
		var re *runner.Error
		if errors.As(err, &re) {
			_ = st.RecordRun(conv.ID, conv.Model, start, durationMs, re.ExitCode, string(re.Kind), excerpt(re.Stderr, 512))
		} else {
			_ = st.RecordRun(conv.ID, conv.Model, start, durationMs, -1, "unknown", "")
		}
		return "", toCLIError(err)
	}
	_ = st.RecordRun(conv.ID, conv.Model, start, durationMs, 0, "", "")

	if _, err := st.AppendMessage(conv.ID, "user", prompt); err != nil {
		return "", configErr(fmt.Errorf("store user message: %w", err))
	}
	if _, err := st.AppendMessage(conv.ID, "assistant", text); err != nil {
		return "", configErr(fmt.Errorf("store assistant message: %w", err))
	}
	return text, nil
}

func newChatCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var (
		modelFlag  string
		continueID string
		timeout    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Persistent chat: one-shot via argument/stdin, or interactive in a terminal",
		Long: `Persistent chat backed by local SQLite.

Apple's runs are stateless; hollis replays the stored transcript each turn to
create continuity (plan §11/§13, proven in results Test B/C/E).

With a prompt argument or piped stdin, sends exactly one turn. With no
argument and a terminal stdin, starts an interactive session; blank lines are
skipped and Ctrl-D ends it. --continue works in the interactive session too.

Use --continue <id> to extend an existing conversation; otherwise a new
conversation is created and auto-titled from the first message.`,
		Example: `  hollis chat
  hollis chat model cloud-pro "Two ideas for naming a CLI"
  hollis chat --continue <id> "And the downside?"
  printf 'question' | hollis chat --continue <id>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			posModel, promptArgs, hasPosModel := splitModelArgs(args)
			m, err := effectiveModel(cmd, modelFlag, posModel, hasPosModel)
			if err != nil {
				return configErr(err)
			}
			model := string(m)
			if !m.Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto (default), cloud, cloud-pro, on-device, or chatgpt", model))
			}

			// Read and validate the prompt BEFORE creating anything, so an
			// empty prompt never leaves a 0-message conversation behind
			// (a measured defect: the conversation used to be created first).
			var prompt string
			interactive := false
			switch {
			case len(promptArgs) > 0:
				prompt = strings.Join(promptArgs, " ")
			case interactiveStdin() && !flags.noInput:
				interactive = true
			case flags.noInput && interactiveStdin():
				// --no-input never waits on a terminal (measured: it would
				// otherwise block on stdin forever).
				return usageErr(errors.New("no prompt provided: pass an argument or pipe stdin (refusing to wait on a terminal in --no-input mode)"))
			default:
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return usageErr(fmt.Errorf("read prompt from stdin: %w", err))
				}
				prompt = string(b)
			}
			if !interactive && strings.TrimSpace(prompt) == "" {
				return usageErr(errors.New("empty prompt: give a prompt as an argument or pipe it via stdin"))
			}

			// Runtime bridge resolution (results/macos-26-compat.md step 1):
			// explicit tiers refuse to run when their bridge did not resolve,
			// and every turn's transport is retargeted at the resolved refs.
			resolved, err := resolveForRunner(cmd.Context(), newRunner)
			if err != nil {
				return configErr(err)
			}
			if err := checkModelAvailable(resolved, m); err != nil {
				return err
			}
			useRunner := resolvedNewRunnerFunc(newRunner, resolved)

			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()

			// Resolve or create the conversation.
			var conv store.Conversation
			if interactive {
				return runInteractiveChat(cmd.Context(), st, model, continueID, useRunner, timeout)
			}
			if continueID != "" {
				conv, err = st.GetConversation(continueID)
				if err != nil {
					return notFoundErr(err)
				}
			} else {
				conv, err = st.CreateConversation(model, "")
				if err != nil {
					return configErr(err)
				}
			}

			text, err := runTurn(cmd.Context(), st, conv, prompt, useRunner, timeout)
			if err != nil {
				return err
			}

			// Auto-title a new conversation from its first user message.
			if conv.Title == "" {
				_ = st.SetTitle(conv.ID, truncateTitle(prompt))
			}

			if flags.asJSON {
				return printJSONFiltered(map[string]any{
					"conversation_id": conv.ID,
					"model":           conv.Model,
					"response":        text,
				}, flags)
			}
			fmt.Print(text)
			if !strings.HasSuffix(text, "\n") {
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&modelFlag, "model", string(runner.ModelAuto), "Model for a new conversation: auto (default: cloud first, on-device fallback), cloud, cloud-pro, on-device, or chatgpt (see hollis models)")
	cmd.Flags().StringVar(&continueID, "continue", "", "Continue an existing conversation by id")
	cmd.Flags().DurationVar(&timeout, "timeout", runner.DefaultTimeout, "Per-turn timeout (default 30s, ceiling 120s)")
	return cmd
}

// runInteractiveChat runs the plan §18 REPL: "> " prompts, "< " responses,
// Ctrl-D ends the session. Blank lines are skipped, not treated as an exit —
// an accidental Enter should not discard a session.
//
// continueID, when set, resumes that conversation instead of starting a new
// one. It used to be ignored here: `hollis chat --continue <id>` at a
// terminal silently opened a fresh conversation, so the flag looked like it
// worked and quietly lost the thread the user asked for.
func runInteractiveChat(ctx context.Context, st *store.Store, model, continueID string, newRunner newRunnerFunc, timeout time.Duration) error {
	var conv store.Conversation
	var err error
	if continueID != "" {
		if conv, err = st.GetConversation(continueID); err != nil {
			return notFoundErr(err)
		}
		fmt.Printf("Continuing · %s · %s\n", conv.Model, conv.ID)
	} else {
		if conv, err = st.CreateConversation(model, ""); err != nil {
			return configErr(err)
		}
		fmt.Printf("New chat · %s · %s\n", model, conv.ID)
	}

	// bufio.Reader, not Scanner: Scanner caps a line at 64KB and reports the
	// overflow as EOF, so pasting a long prompt silently ended the session.
	rd := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("> ")
		raw, readErr := rd.ReadString('\n')
		if line := strings.TrimSpace(raw); line != "" {
			text, err := runTurn(ctx, st, conv, line, newRunner, timeout)
			if err != nil {
				return err
			}
			fmt.Println("<", text)
		}
		if readErr != nil {
			// EOF (Ctrl-D). Any trailing line without a newline was just
			// handled above, so it is safe to stop here.
			return nil
		}
	}
}

func newChatsCmd(flags *rootFlags, _ newRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Inspect persistent chats (list, search, show, rename, delete)",
		// Unknown subcommands are a usage error for agents, not help + exit 0.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return usageErr(fmt.Errorf("unknown chats command %q: run 'hollis chats --help'", args[0]))
		},
	}
	cmd.AddCommand(newChatsListCmd(flags))
	cmd.AddCommand(newChatsSearchCmd(flags))
	cmd.AddCommand(newChatsShowCmd(flags))
	cmd.AddCommand(newChatsRenameCmd(flags))
	cmd.AddCommand(newChatsDeleteCmd(flags))
	return cmd
}

func newChatsSearchCmd(flags *rootFlags) *cobra.Command {
	var (
		modelFilter string
		limit       int
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search chat messages and titles (FTS5)",
		Long: `Full-text search over stored chats (SQLite FTS5).

The whole query is one phrase: no operators, embedded quotes are escaped,
and hyphenated tokens like VANTA-ORBIT match verbatim. Message bodies and
conversation titles are searched; archived conversations are skipped.

Exit codes: 0 hits, 2 empty query, 3 no matches.`,
		Example: `  hollis chats search VANTA-ORBIT
  hollis chats search --model cloud-pro "gateway design"
  hollis chats search --json --limit 5 heating`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			if strings.TrimSpace(query) == "" {
				return usageErr(errors.New("empty search query"))
			}
			if modelFilter != "" && !runner.Model(modelFilter).Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto, cloud, cloud-pro, on-device, or chatgpt", modelFilter))
			}
			if limit < 1 {
				return usageErr(fmt.Errorf("invalid --limit %d: must be at least 1", limit))
			}
			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()
			matches, err := st.Search(query, modelFilter, limit)
			if err != nil {
				return configErr(err)
			}
			if len(matches) == 0 {
				return notFoundErr(fmt.Errorf("no chats match %q", query))
			}
			if flags.asJSON {
				rows := make([]map[string]any, 0, len(matches))
				for _, m := range matches {
					hits := make([]map[string]any, 0, len(m.Hits))
					for _, h := range m.Hits {
						hits = append(hits, map[string]any{"seq": h.Seq, "role": h.Role, "snippet": h.Snippet})
					}
					rows = append(rows, map[string]any{
						"id": m.ID, "title": m.Title, "model": m.Model,
						"updated_at": m.UpdatedAt, "hits": hits,
					})
				}
				return printJSONArrayFiltered(rows, flags)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%-38s  %-9s  %-17s  %s\n", "ID", "MODEL", "UPDATED", "TITLE / SNIPPET")
			for _, m := range matches {
				line := m.Title
				if len(m.Hits) > 0 {
					line = strings.ReplaceAll(m.Hits[0].Snippet, "\n", " ")
				}
				fmt.Fprintf(w, "%s  %-9s  %-17s  %s\n", m.ID, m.Model, shortTS(m.UpdatedAt), line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&modelFilter, "model", "", "Only chats on this model tier: auto, cloud, cloud-pro, on-device, or chatgpt")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum conversations to show")
	return cmd
}

// shortTS renders an RFC3339 timestamp as compact yyyy-mm-ddThh:mmZ for
// table display; unparsable values pass through unchanged.
func shortTS(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format("2006-01-02T15:04Z")
}

func newChatsListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List persistent chats (newest first)",
		Example: `  hollis chats list
  hollis chats list --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()
			convs, err := st.ListConversations(true)
			if err != nil {
				return configErr(err)
			}
			rows := make([]map[string]any, 0, len(convs))
			for _, c := range convs {
				rows = append(rows, map[string]any{
					"id":         c.ID,
					"title":      c.Title,
					"model":      c.Model,
					"messages":   c.Messages,
					"created_at": c.CreatedAt,
					"updated_at": c.UpdatedAt,
					"archived":   c.Archived,
				})
			}
			if flags.asJSON {
				return printJSONArrayFiltered(rows, flags)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%-38s  %-9s  %-9s  %s\n", "ID", "MESSAGES", "MODEL", "TITLE")
			for _, c := range convs {
				fmt.Fprintf(w, "%s  %-9d  %-9s  %s\n", c.ID, c.Messages, c.Model, c.Title)
			}
			return nil
		},
	}
}

func newChatsShowCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Print one conversation: metadata plus every message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()
			conv, err := st.GetConversation(args[0])
			if err != nil {
				return notFoundErr(err)
			}
			msgs, err := st.Messages(conv.ID)
			if err != nil {
				return configErr(err)
			}
			if flags.asJSON {
				rows := make([]map[string]any, 0, len(msgs))
				for _, m := range msgs {
					rows = append(rows, map[string]any{
						"seq": m.Seq, "role": m.Role, "content": m.Content,
					})
				}
				return printJSONFiltered(map[string]any{
					"id": conv.ID, "title": conv.Title, "model": conv.Model,
					"created_at": conv.CreatedAt, "updated_at": conv.UpdatedAt,
					"messages": rows,
				}, flags)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id: %s\nmodel: %s\ntitle: %s\n", conv.ID, conv.Model, conv.Title)
			fmt.Fprintf(w, "created: %s  updated: %s\n", conv.CreatedAt, conv.UpdatedAt)
			for _, m := range msgs {
				fmt.Fprintf(w, "\n%s:\n%s\n", strings.ToUpper(m.Role), m.Content)
			}
			return nil
		},
	}
}

func newChatsRenameCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <id> <title>",
		Short: "Rename a conversation",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()
			if err := st.SetTitle(args[0], strings.Join(args[1:], " ")); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return notFoundErr(err)
				}
				return configErr(err)
			}
			return nil
		},
	}
}

func newChatsDeleteCmd(flags *rootFlags) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a conversation, its messages, and run records",
		Example: `  hollis chats delete <id> --yes
  hollis chats delete <id>          # asks for confirmation`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageErr(errors.New("usage: hollis chats delete <id>"))
			}
			if !yes {
				if flags.noInput {
					return usageErr(errors.New("chats delete requires --yes in --no-input/agent mode"))
				}
				fmt.Fprint(os.Stderr, "Delete this conversation? [y/N] ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				answer := strings.ToLower(strings.TrimSpace(line))
				if answer != "y" && answer != "yes" {
					return nil
				}
			}
			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()
			if _, err := st.GetConversation(args[0]); err != nil {
				return notFoundErr(err)
			}
			if err := st.DeleteConversation(args[0]); err != nil {
				return configErr(err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation (for scripts and agents)")
	return cmd
}

// interactiveStdin reports whether stdin is an interactive terminal.
// Package var so tests can stub terminal-ness.
var interactiveStdin = func() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// excerpt trims s to at most max runes for diagnostic storage.
func excerpt(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "..."
}

func truncateTitle(prompt string) string {
	r := []rune(strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0]))
	if len(r) > 60 {
		return strings.TrimSpace(string(r[:57])) + "..."
	}
	return string(r)
}
