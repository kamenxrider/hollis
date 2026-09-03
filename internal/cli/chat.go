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

type turnResult struct {
	Text           string
	ModelUsed      runner.Model
	FallbackReason string
}

// executeTurn builds and runs one turn, but does not persist it. Keeping the
// transport step separate lets a first chat run before a conversation row is
// visible, so failed first runs cannot leave empty conversations behind.
func executeTurn(ctx context.Context, history []store.Message, requested runner.Model, prompt string, newRunner newRunnerFunc, timeout time.Duration) (turnResult, store.RunRecord, error) {
	transcript := chat.RenderTranscript(history, prompt)
	if err := chat.ValidateTranscript(history, transcript); err != nil {
		return turnResult{}, store.RunRecord{}, usageErr(err)
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	r := newRunner()
	start := time.Now()
	var fallback runner.Fallback
	var text string
	var used runner.Model
	var runErr error
	if rich, ok := r.(runner.FallbackRunner); ok {
		text, used, fallback, runErr = rich.RunWithFallback(ctx, requested, transcript)
	} else {
		text, used, runErr = r.Run(ctx, requested, transcript)
		if requested == runner.ModelAuto && used != "" && used != runner.ModelCloud {
			fallback = runner.Fallback{Used: true, From: runner.ModelCloud, To: used, Reason: runner.KindTransport}
		}
	}
	durationMs := time.Since(start).Milliseconds()
	if used == "" {
		used = requested
	}
	record := store.RunRecord{
		ModelRequested: string(requested),
		ModelUsed:      string(used),
		StartedAt:      start,
		DurationMs:     durationMs,
		ExitCode:       0,
		RequestBytes:   len(transcript),
		ResponseBytes:  len(text),
	}
	if fallback.Used {
		record.FallbackReason = string(fallback.Reason)
	}
	if runErr != nil {
		record.ExitCode = -1
		record.ErrorClass = "unknown"
		var re *runner.Error
		if errors.As(runErr, &re) {
			record.ExitCode = re.ExitCode
			record.ErrorClass = string(re.Kind)
		}
		// Never persist prompt, response, or raw stderr. The stable class and
		// exit code are enough for private diagnostics.
		return turnResult{}, record, toCLIError(runErr)
	}
	return turnResult{
		Text:           text,
		ModelUsed:      used,
		FallbackReason: fallbackReason(requested, used, fallback),
	}, record, nil
}

// runTurn executes one existing-conversation turn. The timeout is applied per
// turn, rather than by the caller wrapping one context around the whole REPL.
func runTurnResult(ctx context.Context, st *store.Store, conv store.Conversation, prompt string, newRunner newRunnerFunc, timeout time.Duration) (turnResult, error) {
	turnCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		turnCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	unlock, err := st.LockContinuation(turnCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return turnResult{}, timeoutErr(fmt.Errorf("wait for chat continuation lock: %w", err))
		}
		if errors.Is(err, context.Canceled) {
			return turnResult{}, transportErr(fmt.Errorf("wait for chat continuation lock: %w", err))
		}
		return turnResult{}, configErr(fmt.Errorf("serialize chat continuation: %w", err))
	}
	defer unlock()

	history, err := st.Messages(conv.ID)
	if err != nil {
		return turnResult{}, configErr(err)
	}
	result, record, runErr := executeTurn(turnCtx, history, runner.Model(conv.Model), prompt, newRunner, 0)
	if runErr != nil {
		if err := recordFailedRun(st, conv.ID, record); err != nil {
			return turnResult{}, configErr(fmt.Errorf("record run diagnostics: %w", err))
		}
		return turnResult{}, runErr
	}
	if err := st.AppendTurn(conv.ID, prompt, result.Text, record); err != nil {
		return turnResult{}, configErr(fmt.Errorf("store chat turn: %w", err))
	}
	return result, nil
}

// runTurn preserves the small helper used by package tests and callers that
// only need the response text.
func runTurn(ctx context.Context, st *store.Store, conv store.Conversation, prompt string, newRunner newRunnerFunc, timeout time.Duration) (string, error) {
	result, err := runTurnResult(ctx, st, conv, prompt, newRunner, timeout)
	return result.Text, err
}

// runFirstTurn runs a new chat before creating its conversation row. Failed
// diagnostics are unattached; successful content and metadata commit in one
// store transaction.
func runFirstTurn(ctx context.Context, st *store.Store, model runner.Model, prompt string, newRunner newRunnerFunc, timeout time.Duration) (turnResult, store.Conversation, error) {
	result, record, runErr := executeTurn(ctx, nil, model, prompt, newRunner, timeout)
	if runErr != nil {
		if err := recordFailedRun(st, "", record); err != nil {
			return turnResult{}, store.Conversation{}, configErr(fmt.Errorf("record run diagnostics: %w", err))
		}
		return turnResult{}, store.Conversation{}, runErr
	}
	conv, err := st.CreateConversationWithTurn(string(model), truncateTitle(prompt), prompt, result.Text, record)
	if err != nil {
		return turnResult{}, store.Conversation{}, configErr(fmt.Errorf("store new chat: %w", err))
	}
	return result, conv, nil
}

func recordFailedRun(st *store.Store, convID string, record store.RunRecord) error {
	if record.StartedAt.IsZero() {
		// Validation failed before a transport run began, so there is no run
		// diagnostic to persist. In particular, oversized prompts must fail
		// before Apple and must not create an empty diagnostic row.
		return nil
	}
	return st.RecordRunMetadata(convID, record)
}

func fallbackReason(requested, used runner.Model, fallback runner.Fallback) string {
	if requested == runner.ModelAuto && used != "" && used != runner.ModelCloud {
		reason := string(fallback.Reason)
		if reason == "" {
			reason = "transport"
		}
		return reason
	}
	return ""
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
		Args: func(cmd *cobra.Command, args []string) error {
			if err := validateTimeout(cmd, timeout); err != nil {
				return err
			}
			_, promptArgs, hasPosModel := splitModelArgs(args)
			if continueID != "" && (hasPosModel || cmd.Flags().Changed("model")) {
				return usageErr(errors.New("--continue uses the conversation's stored model; do not pass --model or a positional model"))
			}
			if continueID == "" && cmd.Flags().Changed("model") && !runner.Model(modelFlag).Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto (default), cloud, cloud-pro, on-device, or chatgpt", modelFlag))
			}
			if len(promptArgs) > 0 {
				if err := chat.ValidatePrompt(strings.Join(promptArgs, " ")); err != nil {
					return usageErr(err)
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateTimeout(cmd, timeout); err != nil {
				return err
			}
			posModel, promptArgs, hasPosModel := splitModelArgs(args)
			if continueID != "" && (hasPosModel || cmd.Flags().Changed("model")) {
				return usageErr(errors.New("--continue uses the conversation's stored model; do not pass --model or a positional model"))
			}
			var m runner.Model
			if continueID == "" {
				var err error
				m, err = effectiveModel(cmd, modelFlag, posModel, hasPosModel)
				if err != nil {
					return configErr(err)
				}
				if !m.Valid() {
					return usageErr(fmt.Errorf("unknown model %q: choose auto (default), cloud, cloud-pro, on-device, or chatgpt", m))
				}
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
				b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), chat.MaxRenderedPromptBytes+1))
				if err != nil {
					return usageErr(fmt.Errorf("read prompt from stdin: %w", err))
				}
				prompt = string(b)
			}
			if !interactive && strings.TrimSpace(prompt) == "" {
				return usageErr(errors.New("empty prompt: give a prompt as an argument or pipe it via stdin"))
			}

			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()

			var conv store.Conversation
			selectedModel := m
			if continueID != "" {
				conv, err = st.GetConversation(continueID)
				if err != nil {
					return notFoundErr(err)
				}
				selectedModel = runner.Model(conv.Model)
				if !selectedModel.Valid() {
					return configErr(fmt.Errorf("conversation %s has unknown stored model %q", conv.ID, conv.Model))
				}
			}

			// Runtime bridge resolution:
			// explicit tiers refuse to run when their bridge did not resolve,
			// and every turn's transport is retargeted at the resolved refs.
			resolved, err := resolveForRunner(cmd.Context(), newRunner)
			if err != nil && !canAttemptAfterDiscoveryFailure(resolved, selectedModel) {
				return resolutionCLIError(err)
			}
			if err := checkModelAvailable(resolved, selectedModel); err != nil {
				return err
			}
			useRunner := resolvedNewRunnerFunc(newRunner, resolved)

			if interactive {
				return runInteractiveChat(cmd.Context(), st, string(selectedModel), continueID, useRunner, timeout, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			if continueID != "" {
				result, err := runTurnResult(cmd.Context(), st, conv, prompt, useRunner, timeout)
				if err != nil {
					return err
				}
				if flags.asJSON {
					return printChatJSON(cmd, result, conv, flags)
				}
				writeChatHuman(cmd, result, conv)
				return nil
			}
			result, conv, err := runFirstTurn(cmd.Context(), st, m, prompt, useRunner, timeout)
			if err != nil {
				return err
			}
			if flags.asJSON {
				return printChatJSON(cmd, result, conv, flags)
			}
			writeChatHuman(cmd, result, conv)
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
func runInteractiveChat(ctx context.Context, st *store.Store, model, continueID string, newRunner newRunnerFunc, timeout time.Duration, in io.Reader, out, errOut io.Writer) error {
	var conv store.Conversation
	var err error
	if continueID != "" {
		if conv, err = st.GetConversation(continueID); err != nil {
			return notFoundErr(err)
		}
		fmt.Fprintf(errOut, "Continuing · %s · %s\n", conv.Model, conv.ID)
	} else {
		fmt.Fprintf(errOut, "New chat · %s\n", model)
	}

	// bufio.Reader, not Scanner: Scanner caps a line at 64KB and reports the
	// overflow as EOF, so pasting a long prompt silently ended the session.
	rd := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "> ")
		raw, readErr := readBoundedPromptLine(rd, chat.MaxRenderedPromptBytes)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return usageErr(readErr)
		}
		if line := strings.TrimSpace(raw); line != "" {
			var result turnResult
			if conv.ID == "" {
				result, conv, err = runFirstTurn(ctx, st, runner.Model(model), line, newRunner, timeout)
				if err == nil {
					fmt.Fprintf(errOut, "conversation_id: %s\n", conv.ID)
				}
			} else {
				result, err = runTurnResult(ctx, st, conv, line, newRunner, timeout)
			}
			if err != nil {
				return err
			}
			if result.FallbackReason != "" {
				fmt.Fprintf(errOut, "hollis: fallback %s: answered with %s\n", result.FallbackReason, result.ModelUsed)
			}
			fmt.Fprintln(out, "<", result.Text)
		}
		if readErr != nil {
			// EOF (Ctrl-D). Any trailing line without a newline was just
			// handled above, so it is safe to stop here.
			return nil
		}
	}
}

func readBoundedPromptLine(rd *bufio.Reader, limit int) (string, error) {
	var line strings.Builder
	for {
		fragment, err := rd.ReadSlice('\n')
		if line.Len()+len(fragment) > limit {
			return "", fmt.Errorf("prompt line exceeds %d bytes", limit)
		}
		line.Write(fragment)
		switch {
		case err == nil:
			return strings.TrimSuffix(line.String(), "\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line.String(), io.EOF
		default:
			return "", fmt.Errorf("read prompt: %w", err)
		}
	}
}

func printChatJSON(cmd *cobra.Command, result turnResult, conv store.Conversation, flags *rootFlags) error {
	data := map[string]any{
		"conversation_id": conv.ID,
		"model_requested": conv.Model,
		"model_used":      result.ModelUsed,
		"response":        result.Text,
	}
	if result.FallbackReason != "" {
		data["fallback_reason"] = result.FallbackReason
	}
	return printJSONFilteredTo(cmd.OutOrStdout(), data, flags)
}

func writeChatHuman(cmd *cobra.Command, result turnResult, conv store.Conversation) {
	if result.FallbackReason != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "hollis: fallback %s: answered with %s\n", result.FallbackReason, result.ModelUsed)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "conversation_id: %s\n", conv.ID)
	fmt.Fprint(cmd.OutOrStdout(), result.Text)
	if !strings.HasSuffix(result.Text, "\n") {
		fmt.Fprintln(cmd.OutOrStdout())
	}
}

func newChatsCmd(flags *rootFlags, _ newRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Inspect persistent chats (list, search, show, rename, delete)",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return usageErr(fmt.Errorf("unknown chats command %q: run 'hollis chats --help'", args[0]))
		},
		// Unknown subcommands are a usage error for agents, not help + exit 0.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if flags.asJSON {
					return usageErr(errors.New("chats requires a subcommand in JSON or agent mode"))
				}
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
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) < 1 {
				return usageErr(errors.New("chats search requires a query"))
			}
			if strings.TrimSpace(strings.Join(args, " ")) == "" {
				return usageErr(errors.New("empty search query"))
			}
			if modelFilter != "" && !runner.Model(modelFilter).Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto, cloud, cloud-pro, on-device, or chatgpt", modelFilter))
			}
			if limit < 1 {
				return usageErr(fmt.Errorf("invalid --limit %d: must be at least 1", limit))
			}
			return nil
		},
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
				return printJSONArrayFilteredTo(cmd.OutOrStdout(), rows, flags)
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
		Args:  cobra.NoArgs,
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
				return printJSONArrayFilteredTo(cmd.OutOrStdout(), rows, flags)
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
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
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
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) < 2 {
				return usageErr(errors.New("usage: hollis chats rename <id> <title>"))
			}
			if strings.TrimSpace(strings.Join(args[1:], " ")) == "" {
				return usageErr(errors.New("conversation title must not be empty"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.TrimSpace(strings.Join(args[1:], " "))
			if title == "" {
				return usageErr(errors.New("conversation title must not be empty"))
			}
			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()
			if err := st.SetTitle(args[0], title); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return notFoundErr(err)
				}
				return configErr(err)
			}
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
					"ok": true, "conversation_id": args[0], "title": title,
				}, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "conversation %s renamed\n", args[0])
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
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageErr(errors.New("usage: hollis chats delete <id>"))
			}
			if !yes {
				if flags.noInput || flags.asJSON {
					return usageErr(errors.New("chats delete requires --yes in JSON, --no-input, or agent mode"))
				}
				fmt.Fprint(cmd.ErrOrStderr(), "Delete this conversation? [y/N] ")
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
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
			if err := st.DeleteConversation(args[0]); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return notFoundErr(err)
				}
				return configErr(err)
			}
			if flags.asJSON {
				return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{
					"ok": true, "conversation_id": args[0], "deleted": true,
				}, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "conversation %s deleted\n", args[0])
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

func truncateTitle(prompt string) string {
	r := []rune(strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0]))
	if len(r) > 60 {
		return strings.TrimSpace(string(r[:57])) + "..."
	}
	return string(r)
}
