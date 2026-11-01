// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

func newRespondCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var (
		model      string
		timeout    time.Duration
		promptFile string
		documents  []string
		images     []string
		stream     bool
		schemaFile string
	)
	cmd := &cobra.Command{
		Use:   "respond [prompt]",
		Short: "Send one prompt to Apple Intelligence and print the plain-text response",
		Long: `Send one prompt to Apple Intelligence and print the plain-text response.

The instruction comes from the positional argument, --prompt-file, or stdin
when neither is provided (multi-line safe). Add local UTF-8 .txt or .md source
material with repeatable --file flags. Documents cannot be mixed with images.
File and image requests require an argument or --prompt-file instruction and
reject nonempty piped stdin. Each call is stateless; use "hollis chat" for
persistent, SQLite-backed conversations.

The default model is auto: cloud first, with automatic fallback to the
on-device model if the cloud run fails. Explicit choices: cloud (AFM 3
Cloud), cloud-pro (AFM 3 Cloud Pro; macOS 27+ — unavailable if that
bridge is not installed), on-device (AFM 3 Core / Core Advanced by
hardware), or chatgpt (ChatGPT extension; enable it in System Settings >
Apple Intelligence & Siri). Image requests with no selected or configured
model default directly to cloud because auto cannot safely fall back. See
hollis models. Select local for Apple's native system model with genuine
streaming text on Apple Silicon/macOS 27. JSON and agent output stay complete.
Guide the native response with --schema <file>: the supported JSON Schema
subset is validated before anything runs, and the returned object is checked
again before it is reported as success.`,
		Example: `  hollis respond "Summarize this repo in one sentence"
  hollis respond model cloud-pro "Draft a reply"
  printf 'long prompt from a pipeline' | hollis respond
  hollis respond --prompt-file instructions.txt
  hollis respond "Summarize the differences" --file first.md --file second.txt
  hollis respond --prompt-file instructions.txt --image photo.png --model cloud
  hollis respond --image photo.jpg "What is this?"
  hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
  hollis respond --model cloud-pro "Flag form also works"
  hollis respond --model local --schema person.json "Invent one person"
  hollis respond --agent "Return strict JSON describing X"`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := validateTimeout(cmd, timeout); err != nil {
				return err
			}
			if cmd.Flags().Changed("model") && !runner.Model(model).Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto (default), cloud, cloud-pro, on-device, chatgpt, or local", model))
			}
			_, promptArgs, _ := splitModelArgs(args)
			hasPromptFile := cmd.Flags().Changed("prompt-file")
			if hasPromptFile && len(promptArgs) > 0 {
				return usageErr(errors.New("choose exactly one instruction source: positional text or --prompt-file"))
			}
			if len(documents) > 0 && len(images) > 0 {
				return usageErr(errors.New("--file cannot be combined with --image"))
			}
			if cmd.Flags().Changed("schema") && len(images) > 0 {
				return usageErr(errors.New("--schema cannot be combined with --image: guided output is text-only"))
			}
			if (len(documents) > 0 || len(images) > 0) && len(promptArgs) == 0 && !hasPromptFile {
				inputFlag := "--file"
				if len(images) > 0 {
					inputFlag = "--image"
				}
				return usageErr(fmt.Errorf("%s requires an explicit instruction: pass positional text or --prompt-file", inputFlag))
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
			hasPromptFile := cmd.Flags().Changed("prompt-file")
			if hasPromptFile || len(documents) > 0 || len(images) > 0 {
				if err := rejectFileInputStdin(cmd); err != nil {
					return err
				}
			}
			var prompt string
			switch {
			case len(promptArgs) > 0:
				prompt = strings.Join(promptArgs, " ")
			case hasPromptFile:
				var err error
				prompt, err = docinput.ReadPromptFile(promptFile, int64(chat.MaxRenderedPromptBytes))
				if err != nil {
					return usageErr(err)
				}
			default:
				// --no-input never waits on a terminal (measured: it would
				// otherwise block on stdin forever).
				if flags.noInput && interactiveStdin() {
					return usageErr(errors.New("no prompt provided: pass an argument or pipe stdin (refusing to wait on a terminal in --no-input mode)"))
				}
				// Bound stdin before allocating it. The extra byte lets the
				// shared validator distinguish the exact limit from overflow.
				b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), chat.MaxRenderedPromptBytes+1))
				if err != nil {
					return usageErr(fmt.Errorf("read prompt from stdin: %w", err))
				}
				prompt = string(b)
			}
			if strings.TrimSpace(prompt) == "" {
				return usageErr(errors.New("empty prompt: give a prompt as an argument or pipe it via stdin"))
			}
			if err := chat.ValidatePrompt(prompt); err != nil {
				return usageErr(err)
			}
			if len(documents) > 0 {
				prepared := make([]docinput.Document, 0, len(documents))
				rawBytes := len(prompt)
				for i, path := range documents {
					document, err := docinput.ReadDocument(path, int64(chat.MaxRenderedPromptBytes))
					if err != nil {
						return usageErr(fmt.Errorf("read document %d: %w", i+1, err))
					}
					if len(document.Text) > chat.MaxRenderedPromptBytes-rawBytes {
						return usageErr(fmt.Errorf("instruction and document text exceed the %d-byte prompt limit", chat.MaxRenderedPromptBytes))
					}
					rawBytes += len(document.Text)
					prepared = append(prepared, document)
				}
				var err error
				prompt, err = docinput.Prepare(prompt, prepared, chat.MaxRenderedPromptBytes)
				if err != nil {
					return usageErr(err)
				}
				if err := chat.ValidatePrompt(prompt); err != nil {
					return usageErr(err)
				}
			}

			// The schema is parsed and validated here, before any model
			// resolution or process spawn: an unsupported schema never reaches
			// the helper, and a rejection costs no discovery and no inference.
			var schema *runner.Schema
			if cmd.Flags().Changed("schema") {
				parsed, err := readSchemaFile(schemaFile)
				if err != nil {
					return err
				}
				schema = parsed
			}

			builtInModel := runner.ModelAuto
			if len(images) > 0 {
				// Auto is unsafe for vision because it can fall back to the
				// on-device tier. An image request with no selected/configured
				// tier therefore defaults directly to Cloud.
				builtInModel = runner.ModelCloud
			}
			m, err := effectiveModelWithDefault(cmd, model, posModel, hasPosModel, builtInModel)
			if err != nil {
				return configErr(err)
			}
			if !m.Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto (default), cloud, cloud-pro, on-device, chatgpt, or local", m))
			}
			if len(images) > 0 {
				if err := runner.ValidateImageRequest(m, prompt, images); err != nil {
					return toCLIError(err)
				}
			}
			if schema != nil {
				// Guided output is a route capability, read from the table
				// rather than assumed for a model name. It is also
				// complete-only: there is no partial structured value to
				// stream, so the combination is refused before spawn.
				spec, ok := runner.Spec(m)
				if !ok || !spec.Capabilities.Schema {
					return usageErr(errors.New("--schema requires --model local: guided output is a native local capability"))
				}
				if cmd.Flags().Changed("stream") && stream {
					return usageErr(errors.New("--schema is complete-only and cannot be combined with --stream"))
				}
				stream = false
			}

			streaming, err := selectStreaming(cmd, flags, m, stream)
			if err != nil {
				return err
			}
			r := newRunner()
			// Handle interruption only after reading the prompt, so idle stdin
			// retains normal signal behavior while in-flight staging can unwind.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// Runtime bridge resolution:
			// explicit tiers refuse to run when their bridge did not resolve,
			// and the real transport is retargeted at the resolved refs.
			resolved, err := resolveForModel(ctx, newRunner, m)
			if err != nil && !canAttemptAfterDiscoveryFailure(resolved, m) {
				return resolutionCLIError(err)
			}
			if err := checkModelAvailable(resolved, m); err != nil {
				return err
			}
			applyResolvedRefs(r, resolved)
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			var fallback runner.Fallback
			var text string
			var used runner.Model
			var usage *runner.Usage
			var structured json.RawMessage
			if len(images) > 0 {
				imageRunner, ok := r.(runner.ImageRunner)
				if !ok {
					return transportErr(errors.New("configured runner does not support image input"))
				}
				text, used, err = imageRunner.RunWithImages(ctx, m, prompt, images)
			} else if m == runner.ModelLocal {
				if schema != nil {
					completion, runErr := runLocalSchema(ctx, r, prompt, schema)
					text, used, usage, structured, err = completion.Text, completion.Model, completion.Usage, completion.Structured, runErr
				} else {
					completion, runErr := runLocalText(ctx, r, prompt, streaming, cmd.OutOrStdout())
					text, used, usage, err = completion.Text, completion.Model, completion.Usage, runErr
				}
			} else if rich, ok := r.(runner.FallbackRunner); ok {
				text, used, fallback, err = rich.RunWithFallback(ctx, m, prompt)
			} else {
				text, used, err = r.Run(ctx, m, prompt)
				if m == runner.ModelAuto && used != "" && used != runner.ModelCloud {
					fallback = runner.Fallback{Used: true, From: runner.ModelCloud, To: used, Reason: runner.KindTransport}
				}
			}
			if err != nil {
				return toCLIError(err)
			}

			if flags.asJSON {
				// model is what was asked for; model_used is what answered.
				// They differ when auto falls back, and the two tiers are not
				// interchangeable — a silent substitution would be the one
				// place hollis hides something from the caller.
				data := map[string]any{
					"model_requested": string(m),
					"model_used":      string(used),
					"response":        text,
				}
				// Guided output is a documented new-surface behavior: with
				// --schema, response is the decoded object rather than a text
				// field that happens to contain JSON.
				if len(structured) != 0 {
					data["response"] = structured
				}
				if usage != nil {
					data["usage"] = usage
				}
				if fallback.Used {
					data["fallback_reason"] = string(fallback.Reason)
				}
				return printJSONFilteredTo(cmd.OutOrStdout(), data, flags)
			}
			// Human output stays clean on stdout; the fallback notice goes to
			// stderr so pipelines are unaffected.
			if m == runner.ModelAuto && used != runner.ModelCloud {
				reason := string(fallback.Reason)
				if reason == "" {
					reason = "cloud unavailable"
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "hollis: fallback %s: answered with %s\n", reason, used)
			}
			if len(structured) != 0 {
				// Human guided output is the object itself, indented; the raw
				// text field is the same JSON, so printing both would duplicate
				// the answer.
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, structured, "", "  "); err != nil {
					return transportErr(errors.New("returned structured output is not valid JSON"))
				}
				fmt.Fprintln(cmd.OutOrStdout(), pretty.String())
				return nil
			}
			// Plain text out. Apple emits no trailing newline; add one
			// only for terminal ergonomics, never into stored values.
			if !streaming {
				fmt.Fprint(cmd.OutOrStdout(), humanTerminalText(cmd.OutOrStdout(), text))
			}
			if !strings.HasSuffix(text, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stream, "stream", false, "Stream native-local human text (default on terminals); --stream=false waits for completion")
	cmd.Flags().StringVar(&model, "model", string(runner.ModelAuto), "Model tier: auto (default: cloud first, on-device fallback), cloud (AFM 3 Cloud), cloud-pro (AFM 3 Cloud Pro; macOS 27+), on-device (AFM 3 Core / Core Advanced by hardware), chatgpt (ChatGPT extension), or local (native SDK; Apple Silicon/macOS 27); see hollis models")
	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "UTF-8 file containing the instruction (instead of positional text or stdin)")
	cmd.Flags().StringArrayVar(&documents, "file", nil, "UTF-8 .txt or .md document to include; repeat to preserve source order")
	cmd.Flags().StringArrayVar(&images, "image", nil, "PNG or JPEG image path; repeat for Cloud/Cloud Pro (ChatGPT accepts one; unavailable with auto/on-device)")
	cmd.Flags().StringVar(&schemaFile, "schema", "", "JSON Schema file for guided native-local output; requires --model local and completes without streaming")
	cmd.Flags().DurationVar(&timeout, "timeout", runner.DefaultTimeout, "Per-call timeout (default 30s, ceiling 120s)")
	return cmd
}

// schemaFileReadLimit bounds the raw file before parsing. The subset's own
// 64 KiB bound applies to the normalized encoding, so a pretty-printed schema
// legitimately exceeds it in source bytes.
const schemaFileReadLimit = 1 << 20

// readSchemaFile parses and validates a guided-output schema. Every rejection
// is a usage error naming the offending JSON pointer, and it happens before the
// helper is spawned.
func readSchemaFile(path string) (*runner.Schema, error) {
	if path == "" {
		return nil, usageErr(errors.New("--schema requires a file path"))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, usageErr(fmt.Errorf("read schema file: %w", err))
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, schemaFileReadLimit+1))
	if err != nil {
		return nil, usageErr(fmt.Errorf("read schema file: %w", err))
	}
	if len(data) > schemaFileReadLimit {
		return nil, usageErr(fmt.Errorf("schema file exceeds the %d-byte read limit", schemaFileReadLimit))
	}
	schema, err := runner.ParseSchema(data)
	if err != nil {
		return nil, usageErr(err)
	}
	return schema, nil
}

// rejectFileInputStdin prevents a second instruction source from being hidden
// in a pipe. A closed or empty non-terminal stdin is harmless, which keeps
// file and image calls usable from agents and CI processes that do not own a
// TTY. Positional-only requests do not call this helper, preserving their
// established behavior.
func rejectFileInputStdin(cmd *cobra.Command) error {
	if cmd.InOrStdin() == os.Stdin && interactiveStdin() {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1))
	if err != nil {
		return usageErr(fmt.Errorf("check stdin for file input: %w", err))
	}
	if len(b) > 0 {
		return usageErr(errors.New("file input cannot be combined with nonempty piped stdin; pass exactly one instruction using positional text or --prompt-file"))
	}
	return nil
}
