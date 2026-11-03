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
	"unicode/utf8"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

func newRespondCmd(flags *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var (
		model      string
		timeout    time.Duration
		promptFile string
		documents  []string
		pageSpecs  []string
		images     []string
		stream     bool
		schemaFile string
		code       bool
		toolsFile  string
		toolChoice string
	)
	cmd := &cobra.Command{
		Use:   "respond [prompt]",
		Short: "Send one prompt to Apple Intelligence and print the plain-text response",
		Long: `Send one prompt to Apple Intelligence and print the plain-text response.

The instruction comes from the positional argument, --prompt-file, or stdin
when neither is provided (multi-line safe); --prompt-file - reads the
instruction from stdin explicitly. Add local UTF-8 text source material of any extension
with repeatable --file flags; a PDF (detected from its content) is read
on-device from its text layer, with OCR for pages that have none, and --pages
selects pages of a single PDF. Documents cannot be mixed with images. With
--file or --image and no positional text or --prompt-file, piped stdin is the
instruction, so exactly one instruction source is used; passing positional text
and piped stdin together with file input is refused rather than silently
dropping one. A pipe held open with no data is not waited on. Each call is
stateless; use "hollis chat" for persistent, SQLite-backed conversations.

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
again before it is reported as success.
Use --code to print only the first complete fenced code block with content,
skipping empty or whitespace-only blocks, so the answer can be redirected into
a source file. A response without a complete fence is printed as it is.
An empty response or only empty code blocks fails with exit 3 and a message
explaining what was empty. --code is complete-only: it is refused with JSON
or agent output, --stream, --schema and --tools.

Offer tools to the native model with --tools <file>, a JSON array in the
OpenAI tools shape. Hollis never runs a tool: it prints one JSON document,
either {"finish_reason":"tool_calls","tool_calls":[...]} with validated
arguments, or {"finish_reason":"stop","text":"..."}. --tool-choice none keeps
the tools out of the prompt and disables the model's tool mode. --tools
requires --model local, completes without streaming, and cannot be combined
with --image, --schema, --code, --json or --agent.`,
		Example: `  hollis respond "Summarize this repo in one sentence"
  hollis respond model cloud-pro "Draft a reply"
  printf 'long prompt from a pipeline' | hollis respond
  printf 'Summarize the notes' | hollis respond --file notes.md
  hollis respond --prompt-file instructions.txt
  printf 'Summarize this' | hollis respond --prompt-file -
  hollis respond "Summarize the differences" --file first.md --file second.txt
  hollis respond --file report.pdf --pages 1,4-6 "Summarize these pages"
  hollis respond --prompt-file instructions.txt --image photo.png --model cloud
  hollis respond --image photo.jpg "What is this?"
  hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
  hollis respond --model cloud-pro "Flag form also works"
  hollis respond --model local --schema person.json "Invent one person"
  hollis respond --code "Write a Go function that reverses a slice" > reverse.go
  hollis respond --model local --tools tools.json "What is the weather in Tokyo?"
  hollis respond --agent "Return strict JSON describing X"`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := validateTimeout(cmd, timeout); err != nil {
				return err
			}
			if cmd.Flags().Changed("model") && !runner.Model(model).Valid() {
				return usageErr(fmt.Errorf("unknown model %q: choose auto (default), cloud, cloud-pro, on-device, chatgpt, or local", model))
			}
			_, promptArgs, _ := splitRespondModelArgs(cmd, args)
			hasPromptFile := cmd.Flags().Changed("prompt-file")
			if hasPromptFile && len(promptArgs) > 0 {
				return usageErr(errors.New("choose exactly one instruction source: positional text or --prompt-file"))
			}
			if len(documents) > 0 && len(images) > 0 {
				return usageErr(errors.New("--file cannot be combined with --image"))
			}
			if cmd.Flags().Changed("pages") {
				if len(images) > 0 {
					return pagesErr(errors.New("--pages selects PDF pages and cannot be combined with --image"))
				}
				if len(documents) != 1 {
					return pagesErr(fmt.Errorf("--pages requires exactly one --file, and it must be a PDF (got %d --file)", len(documents)))
				}
				if _, err := parsePages(pageSpecs); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("schema") && len(images) > 0 {
				return usageErr(errors.New("--schema cannot be combined with --image: guided output is text-only"))
			}
			if code {
				if flags.asJSON || flags.agent {
					return usageErr(errors.New("--code cannot be combined with JSON or agent output"))
				}
				if cmd.Flags().Changed("stream") && stream {
					return usageErr(errors.New("--code is complete-only and cannot be combined with --stream"))
				}
				if cmd.Flags().Changed("schema") {
					return usageErr(errors.New("--code cannot be combined with --schema: guided output is an object, not code"))
				}
			}
			if err := validateToolFlags(cmd, flags, len(images) > 0); err != nil {
				return err
			}
			// File input accepts piped stdin as its instruction, so the
			// presence of that source is decided in RunE, where stdin can be
			// probed without blocking. Here only explicit sources are checked.
			if len(promptArgs) > 0 {
				if err := chat.ValidatePrompt(strings.Join(promptArgs, " ")); err != nil {
					return usageErr(err)
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			timeout, err := effectiveTimeout(cmd, timeout, runner.DefaultTimeout)
			if err != nil {
				return err
			}
			// Checked again here because --agent turns on JSON output in the
			// persistent pre-run, after Args has already run.
			if err := validateToolFlags(cmd, flags, len(images) > 0); err != nil {
				return err
			}
			posModel, promptArgs, hasPosModel := splitRespondModelArgs(cmd, args)
			hasPromptFile := cmd.Flags().Changed("prompt-file")
			hasFileInput := len(documents) > 0 || len(images) > 0
			var prompt string
			switch {
			case len(promptArgs) > 0:
				prompt = strings.Join(promptArgs, " ")
				if hasFileInput {
					// A second instruction may be sitting in the pipe. Refuse
					// it instead of silently dropping one of the two sources.
					piped, err := readInstructionStdin(cmd, 1)
					if err != nil {
						return usageErr(err)
					}
					if piped != "" {
						return usageErr(errors.New("file input cannot be combined with positional text and piped stdin; choose exactly one instruction source"))
					}
				}
			case hasPromptFile && promptFile == "-":
				if flags.noInput && interactiveStdin() {
					return usageErr(errors.New("--prompt-file - requires piped stdin (refusing to wait on a terminal in --no-input mode)"))
				}
				piped, err := readPromptFileStdin(cmd)
				if err != nil {
					return usageErr(err)
				}
				prompt = piped
			case hasPromptFile:
				// --prompt-file is explicit, so a second instruction in the
				// pipe is refused rather than silently ignored. The probe is
				// bounded, so a pipe with nothing in it costs one short check.
				piped, err := readInstructionStdin(cmd, 1)
				if err != nil {
					return usageErr(err)
				}
				if piped != "" {
					return usageErr(errors.New("--prompt-file cannot be combined with piped stdin; choose exactly one instruction source"))
				}
				prompt, err = docinput.ReadPromptFile(promptFile, int64(chat.MaxRenderedPromptBytes))
				if err != nil {
					return usageErr(err)
				}
			case hasFileInput:
				// With file or image input, piped stdin is the instruction.
				// The probe is bounded, so an open pipe with nothing in it
				// reports the missing instruction instead of hanging.
				piped, err := readInstructionStdin(cmd, chat.MaxRenderedPromptBytes+1)
				if err != nil {
					return usageErr(err)
				}
				if piped == "" {
					inputFlag := "--file"
					if len(images) > 0 {
						inputFlag = "--image"
					}
					return usageErr(fmt.Errorf("%s requires an instruction: pass positional text, --prompt-file, or pipe it via stdin", inputFlag))
				}
				prompt = piped
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
				if hasPromptFile && promptFile == "-" {
					return usageErr(errors.New("--prompt-file - requires the instruction on stdin"))
				}
				return usageErr(errors.New("empty prompt: give a prompt as an argument or pipe it via stdin"))
			}
			if err := chat.ValidatePrompt(prompt); err != nil {
				return usageErr(err)
			}
			instruction := prompt
			var inputs []documentInput
			hasPDF := false
			var pages []int
			if len(documents) > 0 {
				var err error
				inputs, err = readDocumentInputs(documents, len(prompt))
				if err != nil {
					return err
				}
				for _, input := range inputs {
					hasPDF = hasPDF || input.isPDF()
				}
				if cmd.Flags().Changed("pages") {
					if !inputs[0].isPDF() {
						return pagesErr(fmt.Errorf("--pages requires a PDF, but %q is a text document", inputs[0].name))
					}
					pages, _ = parsePages(pageSpecs)
				}
				if !hasPDF {
					if err := prepareDocuments(&prompt, inputs); err != nil {
						return err
					}
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

			var toolSet []runner.Tool
			if cmd.Flags().Changed("tools") {
				parsed, err := readToolsFile(toolsFile)
				if err != nil {
					return err
				}
				toolSet = parsed
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
				return err
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
			if toolSet != nil {
				// Tools are a native-local capability and, like guided output,
				// complete-only: a call must be validated before any of it is
				// shown, so the combination with --stream is refused, not
				// silently downgraded.
				spec, ok := runner.Spec(m)
				if !ok || spec.Transport != runner.TransportNative {
					return usageErr(errors.New("--tools requires --model local: tool calling is a native local capability"))
				}
				stream = false
			}

			streaming, err := selectStreaming(cmd, flags, m, stream)
			if err != nil {
				return err
			}
			if code {
				// --code needs the complete text to find the block; a terminal
				// local run would otherwise stream prose into the redirection.
				streaming = false
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
			if hasPDF {
				// Extraction runs on-device before the model is asked
				// anything; the framed result faces the same 128 KiB check
				// as any other prompt, with both numbers and no truncation.
				extracted, err := extractDocuments(ctx, r, inputs, pages, strings.Join(pageSpecs, ","), timeout)
				if err != nil {
					return err
				}
				prompt, err = docinput.Prepare(instruction, extracted, chat.MaxRenderedPromptBytes)
				if err != nil {
					return usageErr(err)
				}
			}
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			if toolSet != nil {
				return withPDFRefusalContext(runRespondTools(ctx, cmd, r, prompt, toolSet, runner.ToolChoice(toolChoice)), hasPDF)
			}
			var fallback runner.Fallback
			var text string
			var used runner.Model
			var usage *runner.Usage
			var structured json.RawMessage
			if len(images) > 0 && m == runner.ModelLocal {
				completion, runErr := runLocalImages(ctx, r, prompt, images, streaming, cmd.OutOrStdout())
				text, used, usage, err = completion.Text, completion.Model, completion.Usage, runErr
			} else if len(images) > 0 {
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
				return withPDFRefusalContext(toCLIError(err), hasPDF)
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
			if code {
				// Print the first complete block with content, or the answer
				// itself when it contains no complete block.
				codeText := text
				if block, ok := firstFencedBlock(text); ok {
					if block == "" {
						return notFoundErr(errors.New("no code to print: the code blocks were empty"))
					}
					codeText = block
				}
				codeText = strings.TrimRight(codeText, "\n")
				if strings.TrimSpace(codeText) == "" {
					return notFoundErr(errors.New("no code to print: the response contained no code"))
				}
				fmt.Fprintln(cmd.OutOrStdout(), humanTerminalText(cmd.OutOrStdout(), codeText))
				return nil
			}
			if len(structured) != 0 {
				// Human guided output is the object itself, indented; the raw
				// text field is the same JSON, so printing both would duplicate
				// the answer.
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, structured, "", "  "); err != nil {
					return transportErr(errors.New("returned structured output is not valid JSON"))
				}
				// JSON escapes C0 controls but not C1, and 0x9b is an 8-bit CSI
				// introducer on some terminals, so the model's string values can
				// still carry a live sequence. Sanitizing the rendered text is
				// lossless for JSON: an escaped \u0080 inside a string literal
				// decodes to the same character. Non-terminal output is
				// untouched, so pipes and files keep the exact bytes.
				fmt.Fprintln(cmd.OutOrStdout(), humanTerminalText(cmd.OutOrStdout(), pretty.String()))
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
	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "UTF-8 file containing the instruction (instead of positional text or stdin); '-' reads the instruction from stdin")
	cmd.Flags().StringArrayVar(&documents, "file", nil, "UTF-8 text document (any extension), or a PDF read on-device, to include; repeat to preserve source order")
	cmd.Flags().StringArrayVar(&pageSpecs, "pages", nil, "PDF pages to read from a single --file PDF: 3, 3-7, or 1,4-6; repeatable, 1-based, at most 2000 pages")
	cmd.Flags().StringArrayVar(&images, "image", nil, "PNG or JPEG image path; repeat for Cloud/Cloud Pro (ChatGPT accepts one; unavailable with auto/on-device)")
	cmd.Flags().StringVar(&schemaFile, "schema", "", "JSON Schema file for guided native-local output; requires --model local and completes without streaming")
	cmd.Flags().BoolVar(&code, "code", false, "Print the first complete non-empty fenced code block (or the whole answer when there is no complete fence); exit 3 when there is nothing to print")
	cmd.Flags().StringVar(&toolsFile, "tools", "", "JSON file with an OpenAI-shaped tools array for native-local tool calling; prints the tool calls or the answer as one JSON document")
	cmd.Flags().StringVar(&toolChoice, "tool-choice", string(runner.ToolChoiceAuto), "With --tools: auto (default) lets the model call a tool; none keeps tools out of the prompt")
	cmd.Flags().DurationVar(&timeout, "timeout", runner.DefaultTimeout, "Per-call timeout (default 30s, ceiling 120s)")
	return cmd
}

// prepareDocuments frames text-only document input, which needs no helper.
func prepareDocuments(prompt *string, inputs []documentInput) error {
	documents := make([]docinput.Document, 0, len(inputs))
	for _, input := range inputs {
		documents = append(documents, input.document)
	}
	prepared, err := docinput.Prepare(*prompt, documents, chat.MaxRenderedPromptBytes)
	if err != nil {
		return usageErr(err)
	}
	if err := chat.ValidatePrompt(prepared); err != nil {
		return usageErr(err)
	}
	*prompt = prepared
	return nil
}

func splitRespondModelArgs(cmd *cobra.Command, args []string) (string, []string, bool) {
	// Both selector tokens must precede "--"; do not consume literal text
	// even when the separator divides "model" from its tier.
	if at := cmd.ArgsLenAtDash(); at >= 0 && at < 2 {
		return "", args, false
	}
	return splitModelArgs(args)
}

// schemaFileReadLimit bounds the raw file before parsing. The subset's own
// 64 KiB bound applies to the normalized encoding, and Foundation's encoder
// escapes each "/" as "\/", so a pretty-printed schema legitimately exceeds the
// normalized bound in source bytes.
const schemaFileReadLimit = 1 << 20

// stdinInstructionWindow is how long a file or image request waits for the
// first byte of a piped instruction. A pipe that stays open with no data is not
// an instruction source, and waiting for it would stall the command: the
// previous check blocked until the writer produced a byte or closed.
const stdinInstructionWindow = 200 * time.Millisecond

// readInstructionStdin reads up to limit bytes of piped stdin, or returns ""
// when stdin carries nothing. Conflict checks need only one byte, not EOF.
// A terminal is never read here; --prompt-file - is the explicit way to read
// the instruction from stdin interactively.
func readInstructionStdin(cmd *cobra.Command, limit int64) (string, error) {
	in := cmd.InOrStdin()
	if file, ok := in.(*os.File); ok {
		if in == os.Stdin && interactiveStdin() {
			return "", nil
		}
		ready, err := waitReadable(file, stdinInstructionWindow)
		if err != nil {
			return "", fmt.Errorf("check stdin for an instruction: %w", err)
		}
		if !ready {
			return "", nil
		}
	}
	b, err := io.ReadAll(io.LimitReader(in, limit))
	if err != nil {
		return "", fmt.Errorf("read instruction from stdin: %w", err)
	}
	return string(b), nil
}

// readPromptFileStdin reads --prompt-file - as an ordinary stdin read. The
// caller rejects terminals in non-interactive modes; otherwise the read waits
// for end of input like any other file read.
func readPromptFileStdin(cmd *cobra.Command) (string, error) {
	b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), chat.MaxRenderedPromptBytes+1))
	if err != nil {
		return "", fmt.Errorf("read --prompt-file - from stdin: %w", err)
	}
	if !utf8.Valid(b) {
		return "", errors.New("--prompt-file - must contain valid UTF-8")
	}
	return string(b), nil
}

// waitReadable reports whether the file has data, or its writer has closed,
// within the window. Regular files are always ready; pipes and terminals are
// polled so an open pipe with no data cannot block the command.
func waitReadable(file *os.File, window time.Duration) (bool, error) {
	deadline := time.Now().Add(window)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		poll := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, int(remaining/time.Millisecond)+1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		return n > 0, nil
	}
}

// readSchemaFile parses and validates a guided-output schema. Every rejection
// is a usage error naming the offending JSON pointer, and it happens before the
// helper is spawned.
//
// The read follows the same safe-input rules as --prompt-file and --file:
// basename-only diagnostics, classified errors that never carry the caller's
// local path, a regular-file check before reading, and a non-blocking open so a
// raced FIFO cannot stall the command.
func readSchemaFile(path string) (*runner.Schema, error) {
	if path == "" {
		return nil, usageErr(errors.New("--schema requires a file path"))
	}
	data, err := docinput.ReadFile(path, "schema file", schemaFileReadLimit)
	if err != nil {
		return nil, usageErr(err)
	}
	schema, err := runner.ParseSchema(data)
	if err != nil {
		return nil, usageErr(err)
	}
	return schema, nil
}

// toolsFileReadLimit bounds the raw tools file. Thirty-two tools at the 64 KiB
// per-schema bound fit; the rendered prompt budget is the real aggregate bound.
const toolsFileReadLimit = 4 << 20

// validateToolFlags refuses every --tools combination §4.2 rules out, before
// anything is read, staged or spawned.
func validateToolFlags(cmd *cobra.Command, flags *rootFlags, hasImages bool) error {
	hasTools := cmd.Flags().Changed("tools")
	if cmd.Flags().Changed("tool-choice") && !hasTools {
		return usageErr(errors.New("--tool-choice requires --tools"))
	}
	if !hasTools {
		return nil
	}
	choice, _ := cmd.Flags().GetString("tool-choice")
	switch runner.ToolChoice(choice) {
	case runner.ToolChoiceAuto, runner.ToolChoiceNone:
	default:
		return usageErr(errors.New("--tool-choice accepts only auto or none"))
	}
	if hasImages {
		return usageErr(errors.New("--tools cannot be combined with --image: a later tool round could not see the image"))
	}
	if code, _ := cmd.Flags().GetBool("code"); code {
		return usageErr(errors.New("--tools cannot be combined with --code: tool output is a JSON document, not code"))
	}
	if cmd.Flags().Changed("schema") {
		return usageErr(errors.New("--tools cannot be combined with --schema: a reply cannot be both a schema object and a tool call"))
	}
	if streamFlag, _ := cmd.Flags().GetBool("stream"); cmd.Flags().Changed("stream") && streamFlag {
		return usageErr(errors.New("--tools is complete-only and cannot be combined with --stream"))
	}
	if flags.asJSON {
		return usageErr(errors.New("--tools already prints one JSON document; remove --json/--agent"))
	}
	return nil
}

// readToolsFile reads and validates a tools array with the same safe-input
// rules as --schema. Every rejection is a usage error.
func readToolsFile(path string) ([]runner.Tool, error) {
	if path == "" {
		return nil, usageErr(errors.New("--tools requires a file path"))
	}
	data, err := docinput.ReadFile(path, "tools file", toolsFileReadLimit)
	if err != nil {
		return nil, usageErr(err)
	}
	tools, err := runner.ParseTools(data, "--tools")
	if err != nil {
		return nil, usageErr(err)
	}
	return tools, nil
}

func withPDFRefusalContext(err error, hasPDF bool) error {
	var ce *cliError
	if hasPDF && errors.As(err, &ce) && ce.machineCode == "request_declined" {
		return &cliError{code: ce.code, machineCode: ce.machineCode, err: fmt.Errorf("%w%s", ce.err, extractedDeclinedNote)}
	}
	return err
}

// runRespondTools runs the tool loop and prints its one JSON document. Calls
// are returned to the caller; nothing is executed.
func runRespondTools(ctx context.Context, cmd *cobra.Command, r runner.Runner, prompt string, tools []runner.Tool, choice runner.ToolChoice) error {
	roundRunner, ok := r.(runner.ToolRoundRunner)
	if !ok {
		return transportErr(errors.New("configured runner does not support native-local tool calling"))
	}
	outcome, err := runner.RunToolLoop(ctx, roundRunner, runner.ModelLocal, runner.ToolConversation{
		Tools: tools, Choice: choice, Messages: []store.Message{{Role: "user", Content: prompt}},
	}, runner.ReturnToCaller{})
	if err != nil {
		return toCLIError(err)
	}
	document := toolDocument(outcome)
	encoded, err := json.Marshal(document)
	if err != nil {
		return transportErr(errors.New("could not encode the tool result"))
	}
	out := cmd.OutOrStdout()
	if terminalOutput(out) {
		// The human rendering is the same document, indented. Model text in
		// string values is sanitized for the terminal, which is lossless for
		// JSON (see the guided-output path above).
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, encoded, "", "  "); err != nil {
			return transportErr(errors.New("could not encode the tool result"))
		}
		fmt.Fprintln(out, humanTerminalText(out, pretty.String()))
		return nil
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

// toolDocument is the fixed --tools output envelope.
func toolDocument(outcome runner.ToolOutcome) map[string]any {
	document := map[string]any{"model": string(outcome.Model), "rounds": outcome.Rounds}
	if outcome.Usage != nil {
		document["usage"] = outcome.Usage
	}
	if len(outcome.Calls) == 0 {
		document["finish_reason"] = "stop"
		document["text"] = outcome.Text
		return document
	}
	calls := make([]map[string]any, len(outcome.Calls))
	for index, call := range outcome.Calls {
		calls[index] = map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}}
	}
	document["finish_reason"] = "tool_calls"
	document["tool_calls"] = calls
	return document
}
