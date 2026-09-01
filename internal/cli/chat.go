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
func runTurn(st *store.Store, conv store.Conversation, prompt string, newRunner newRunnerFunc) (string, error) {
	history, err := st.Messages(conv.ID)
	if err != nil {
		return "", configErr(err)
	}
	transcript := chat.RenderTranscript(history, prompt)

	r := newRunner()
	start := time.Now()
	text, err := r.Run(context.Background(), runner.Model(conv.Model), transcript)
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
	)
	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Persistent chat: one-shot via argument/stdin, or interactive in a terminal",
		Long: `Persistent chat backed by local SQLite.

Apple's runs are stateless; hollis replays the stored transcript each turn to
create continuity (plan §11/§13, proven in results Test B/C/E).

With a prompt argument or piped stdin, sends exactly one turn. With no
argument and a terminal stdin, starts an interactive session (blank line or
Ctrl-D ends it).

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

			st, err := openStore()
			if err != nil {
				return configErr(err)
			}
			defer st.Close()

			// Resolve or create the conversation.
			var conv store.Conversation
			if continueID != "" {
				conv, err = st.GetConversation(continueID)
				if err != nil {
					return notFoundErr(err)
				}
			} else if len(promptArgs) == 0 && interactiveStdin() && !flags.noInput {
				return runInteractiveChat(st, model, newRunner)
			} else {
				conv, err = st.CreateConversation(model, "")
				if err != nil {
					return configErr(err)
				}
			}

			var prompt string
			if len(promptArgs) > 0 {
				prompt = strings.Join(promptArgs, " ")
			} else {
				// --no-input never waits on a terminal (TEST-REPORT §6.4):
				// fail fast instead of blocking on stdin forever.
				if flags.noInput && interactiveStdin() {
					return usageErr(errors.New("no prompt provided: pass an argument or pipe stdin (refusing to wait on a terminal in --no-input mode)"))
				}
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return usageErr(fmt.Errorf("read prompt from stdin: %w", err))
				}
				prompt = string(b)
			}
			if strings.TrimSpace(prompt) == "" {
				return usageErr(errors.New("empty prompt: give a prompt as an argument or pipe it via stdin"))
			}

			text, err := runTurn(st, conv, prompt, newRunner)
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
	return cmd
}

// runInteractiveChat runs the plan §18 REPL: "> " prompts, "< " responses,
// Ctrl-D ends the session.
func runInteractiveChat(st *store.Store, model string, newRunner newRunnerFunc) error {
	conv, err := st.CreateConversation(model, "")
	if err != nil {
		return configErr(err)
	}
	fmt.Printf("New chat · %s · %s\n", model, conv.ID)
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		text, err := runTurn(st, conv, line, newRunner)
		if err != nil {
			return err
		}
		fmt.Println("<", text)
	}
	return nil
}

func newChatsCmd(flags *rootFlags, _ newRunnerFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Inspect persistent chats (list, show, rename, delete)",
	}
	cmd.AddCommand(newChatsListCmd(flags))
	cmd.AddCommand(newChatsShowCmd(flags))
	cmd.AddCommand(newChatsRenameCmd(flags))
	cmd.AddCommand(newChatsDeleteCmd(flags))
	return cmd
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
				n, _ := st.MessageCount(c.ID)
				rows = append(rows, map[string]any{
					"id":         c.ID,
					"title":      c.Title,
					"model":      c.Model,
					"messages":   n,
					"created_at": c.CreatedAt,
					"updated_at": c.UpdatedAt,
					"archived":   c.Archived,
				})
			}
			if flags.asJSON {
				return printJSONArrayFiltered(rows, flags)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%-38s  %-9s  %-8s  %s\n", "ID", "MESSAGES", "MODEL", "TITLE")
			for _, c := range convs {
				n, _ := st.MessageCount(c.ID)
				fmt.Fprintf(w, "%s  %-9d  %s\n", c.ID, n, c.Title)
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
