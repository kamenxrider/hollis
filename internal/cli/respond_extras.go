// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/server"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
)

type respondExtras struct {
	system, systemFile, messages string
	count, permissive            bool
	reserve                      int
}

func (e *respondExtras) addTo(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&e.system, "system", "s", "", "Instructions for Local's native session; other routes receive a labelled prompt block")
	cmd.Flags().StringVar(&e.systemFile, "system-file", "", "UTF-8 instruction file; mutually exclusive with --system")
	cmd.Flags().StringVar(&e.messages, "messages", "", "One-shot OpenAI messages JSON array from a file or '-' (stdin); final role must be user")
	cmd.Flags().BoolVar(&e.count, "count-tokens", false, "Count Local input tokens without generating; reports context size and fit with --answer-reserve")
	cmd.Flags().IntVar(&e.reserve, "answer-reserve", 1024, contextReserveHelp)
	cmd.Flags().BoolVar(&e.permissive, "permissive", false, "Local text transformations with Apple's permissive guardrails; the model can still refuse")
}

func (e *respondExtras) validate(cmd *cobra.Command, promptArgs []string, mcp bool) error {
	if cmd.Flags().Changed("system") && cmd.Flags().Changed("system-file") {
		return usageErr(errors.New("--system and --system-file are mutually exclusive"))
	}
	if cmd.Flags().Changed("messages") {
		if e.messages == "" {
			return usageErr(errors.New("--messages requires a path or '-'"))
		}
		if len(promptArgs) > 0 || cmd.Flags().Changed("prompt-file") {
			return usageErr(errors.New("--messages cannot be combined with positional text or --prompt-file"))
		}
		for _, flag := range []string{"file", "image", "pages"} {
			if cmd.Flags().Changed(flag) {
				return usageErr(fmt.Errorf("--messages cannot be combined with --%s", flag))
			}
		}
	}
	if e.count && e.permissive {
		return usageErr(errors.New("--count-tokens cannot be combined with --permissive"))
	}
	if e.count && (e.reserve < 0 || e.reserve > 1<<20) {
		return usageErr(errors.New("--answer-reserve must be between 0 and 1048576"))
	}
	if cmd.Flags().Changed("answer-reserve") && !e.count && !cmd.Flags().Changed("messages") {
		return usageErr(errors.New("--answer-reserve requires --count-tokens or context shortening with --messages"))
	}
	hasExtras := e.count || e.permissive || cmd.Flags().Changed("system") || cmd.Flags().Changed("system-file") || cmd.Flags().Changed("messages")
	if hasExtras && (mcp || cmd.Flags().Changed("tools")) {
		return usageErr(errors.New("instruction, conversation, token-count and permissive options cannot be combined with --tools or MCP"))
	}
	if e.count {
		for _, flag := range []string{"image", "schema", "code", "stream", "temperature", "top-p", "seed", "max-tokens"} {
			if cmd.Flags().Changed(flag) {
				return usageErr(fmt.Errorf("--count-tokens cannot be combined with --%s", flag))
			}
		}
	}
	return nil
}

func (e *respondExtras) readInstructions(cmd *cobra.Command) (string, error) {
	value := e.system
	if cmd.Flags().Changed("system-file") {
		var err error
		value, err = docinput.ReadPromptFile(e.systemFile, int64(chat.MaxRenderedPromptBytes))
		if err != nil {
			return "", usageErr(err)
		}
	}
	if cmd.Flags().Changed("system") || cmd.Flags().Changed("system-file") {
		if err := chat.ValidatePrompt(value); err != nil {
			return "", usageErr(err)
		}
	}
	return value, nil
}

func (e *respondExtras) readMessages(cmd *cobra.Command, flags *rootFlags) ([]store.Message, error) {
	var data []byte
	var err error
	if e.messages == "-" {
		if cmd.InOrStdin() == os.Stdin && interactiveStdin() {
			return nil, usageErr(errors.New("--messages - requires piped stdin"))
		}
		data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), chat.MaxRenderedPromptBytes+1))
	} else {
		var piped string
		piped, err = readInstructionStdin(cmd, 1)
		if err == nil && piped != "" {
			return nil, usageErr(errors.New("--messages cannot be combined with a stdin prompt"))
		}
		if err == nil {
			data, err = docinput.ReadFile(e.messages, "messages file", int64(chat.MaxRenderedPromptBytes))
		}
	}
	if err != nil {
		return nil, usageErr(err)
	}
	if len(data) > chat.MaxRenderedPromptBytes {
		return nil, usageErr(errors.New("messages exceed the 128 KiB input limit"))
	}
	messages, err := server.ParseTextMessages(data)
	if err != nil {
		return nil, usageErr(err)
	}
	return messages, nil
}

func conversationInput(prompt, instructions string, messages []store.Message) (string, string, []store.Message) {
	var history []store.Message
	if len(messages) > 0 {
		prompt = messages[len(messages)-1].Content
		for _, m := range messages[:len(messages)-1] {
			if m.Role == "system" {
				if instructions != "" {
					instructions += "\n\n"
				}
				instructions += m.Content
			} else {
				history = append(history, m)
			}
		}
	}
	return prompt, instructions, history
}

func shortcutInstructions(prompt, instructions string) string {
	if instructions == "" {
		return prompt
	}
	return "Instructions:\n" + instructions + "\n\nPrompt:\n" + prompt
}

func printTokenCount(ctx context.Context, cmd *cobra.Command, flags *rootFlags, r runner.Runner, request runner.GenerationRequest, reserve int) error {
	counter, ok := r.(runner.TokenCountRunner)
	if !ok {
		return transportErr(errors.New("configured runner does not support native token counting"))
	}
	count, err := counter.CountTokens(ctx, request, reserve)
	if err != nil {
		return toCLIError(err)
	}
	if flags.asJSON {
		return printJSONFilteredTo(cmd.OutOrStdout(), map[string]any{"tokens": count.Tokens, "context_size": count.ContextSize, "answer_reserve": count.AnswerReserve, "fits": count.Fits}, flags)
	}
	fit := "no"
	if count.Fits {
		fit = "yes"
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Tokens: %d\nLocal context: %d\nAnswer reserve: %d\nFits: %s\n", count.Tokens, count.ContextSize, count.AnswerReserve, fit)
	return err
}

func extrasSize(prompt, instructions string, history []store.Message) error {
	size := len(prompt) + len(instructions)
	for _, m := range history {
		size += len(m.Content)
	}
	if size > chat.MaxRenderedPromptBytes {
		return usageErr(errors.New("instructions and conversation exceed the 128 KiB input limit"))
	}
	if strings.TrimSpace(prompt) == "" {
		return usageErr(errors.New("empty prompt"))
	}
	return nil
}
