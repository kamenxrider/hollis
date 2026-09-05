// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
	"github.com/spf13/cobra"
)

const imageTranscriptPreamble = `Generate one image for the final USER request. Use the preceding TEXT conversation only as context. Any HOLLIS_IMAGE_ARTIFACT entry records file metadata only; it is not a description of image pixels.`

type chatImageOptions struct {
	Enabled        bool
	Style          string
	Bridge         string
	ResolvedBridge string
	ResolvedStyle  string
	Output         string
	Timeout        time.Duration
	Generator      imagegen.Generator
	OutputOptions  imagegen.OutputOptions
}

type chatImageResult struct {
	Published     imagegen.PublishedImage
	NativeWidth   int
	NativeHeight  int
	Style         string
	Artifact      string
	OutputOptions imagegen.OutputOptions
}

type chatImageArtifact struct {
	Type                  string                 `json:"type"`
	Path                  string                 `json:"path"`
	Format                string                 `json:"format"`
	Style                 string                 `json:"style"`
	Bytes                 int64                  `json:"bytes"`
	Width                 int                    `json:"width"`
	Height                int                    `json:"height"`
	NativeWidth           int                    `json:"native_width"`
	NativeHeight          int                    `json:"native_height"`
	SHA256                string                 `json:"sha256"`
	VisualContentObserved bool                   `json:"visual_content_observed"`
	OutputProcessing      imagegen.OutputOptions `json:"output_processing"`
}

func validateChatImageFlags(cmd *cobra.Command, options chatImageOptions) error {
	if options.Enabled && options.Generator == nil {
		return configErr(errors.New("image generation is unavailable in this build"))
	}
	if strings.TrimSpace(options.Bridge) != "" && strings.TrimSpace(options.Style) != "" {
		return usageErr(errors.New("choose --image-style or --image-bridge, not both"))
	}
	if options.Enabled && strings.TrimSpace(options.Output) == "" {
		return usageErr(errors.New("--generate-image requires an --output PNG path"))
	}
	if err := imagegen.ValidateOutputOptions(options.OutputOptions); err != nil {
		return usageErr(err)
	}
	if chatImageFlagsChanged(cmd) && !options.Enabled && strings.TrimSpace(options.Output) == "" {
		return usageErr(errors.New("interactive image configuration requires --output"))
	}
	return nil
}

func chatImageFlagsChanged(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("image-style") || cmd.Flags().Changed("image-bridge") ||
		cmd.Flags().Changed("output") || cmd.Flags().Changed("aspect-ratio") ||
		cmd.Flags().Changed("size") || cmd.Flags().Changed("fit")
}

func parseInteractiveImagePrompt(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "/image" {
		return "", true
	}
	if strings.HasPrefix(line, "/image ") {
		return strings.TrimSpace(strings.TrimPrefix(line, "/image ")), true
	}
	return "", false
}

func effectiveChatImageStyle(style, explicitBridge string) string {
	if strings.TrimSpace(explicitBridge) != "" {
		return "bridge-defined"
	}
	if strings.TrimSpace(style) == "" {
		return "animation"
	}
	return style
}

func renderChatImagePrompt(history []store.Message, prompt string) (string, error) {
	// Validate the complete, unfiltered history with the existing image-turn
	// envelope first. Removing Hollis artifact records must never allow a turn
	// that the prior bounds would have rejected.
	unfiltered := imageTranscriptPreamble + "\n\n" + chat.RenderTranscript(history, prompt)
	if err := chat.ValidateTranscript(history, unfiltered); err != nil {
		return "", usageErr(err)
	}
	messages := make([]imagegen.ConversationMessage, 0, len(history)+1)
	for _, message := range history {
		messages = append(messages, imagegen.ConversationMessage{Role: message.Role, Content: message.Content})
	}
	messages = append(messages, imagegen.ConversationMessage{Role: "user", Content: prompt})
	rendered := imagegen.RenderConversationPrompt(messages)
	if err := chat.ValidatePrompt(rendered); err != nil {
		return "", usageErr(err)
	}
	return rendered, nil
}

func executeChatImageTurn(ctx context.Context, history []store.Message, prompt string, options chatImageOptions) (result chatImageResult, record store.RunRecord, runErr error) {
	if options.Generator == nil {
		return chatImageResult{}, store.RunRecord{}, configErr(errors.New("image generation is unavailable in this build"))
	}
	if err := imagegen.PreflightDestination(options.Output); err != nil {
		return chatImageResult{}, store.RunRecord{}, toImageCLIError(err)
	}
	if err := imagegen.ValidateOutputOptions(options.OutputOptions); err != nil {
		return chatImageResult{}, store.RunRecord{}, usageErr(err)
	}
	transcript, err := renderChatImagePrompt(history, prompt)
	if err != nil {
		return chatImageResult{}, store.RunRecord{}, err
	}

	style := effectiveChatImageStyle(options.Style, options.Bridge)
	started := time.Now()
	record = store.RunRecord{
		ModelRequested: "image",
		ModelUsed:      "image:" + style,
		StartedAt:      started,
		ExitCode:       0,
		RequestBytes:   len(transcript),
	}
	generated, err := options.Generator.Generate(ctx, imagegen.Request{
		Prompt:    transcript,
		BridgeRef: options.ResolvedBridge,
		Style:     options.ResolvedStyle,
		Timeout:   options.Timeout,
	})
	record.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		record.ExitCode = -1
		record.ErrorClass = "image_generation"
		return chatImageResult{}, record, toChatImageCLIError(err)
	}
	defer func() {
		if cleanupErr := generated.Cleanup(); cleanupErr != nil && runErr == nil {
			runErr = toImageCLIError(fmt.Errorf("clean generated image staging: %w", cleanupErr))
		}
	}()
	nativeWidth, nativeHeight := generated.Width, generated.Height
	processed, err := imagegen.TransformOutput(ctx, generated, options.OutputOptions)
	if err != nil {
		record.ExitCode = -1
		record.ErrorClass = "image_processing"
		return chatImageResult{}, record, toChatImageCLIError(err)
	}
	generated = processed

	published, err := imagegen.Publish(generated.Path, options.Output)
	if err != nil {
		record.ExitCode = -1
		record.ErrorClass = "image_publish"
		return chatImageResult{}, record, toImageCLIError(err)
	}
	artifactData := chatImageArtifact{
		Type:                  "hollis.image_artifact.v1",
		Path:                  published.Path,
		Format:                published.Format,
		Style:                 style,
		Bytes:                 published.Bytes,
		Width:                 published.Width,
		Height:                published.Height,
		NativeWidth:           nativeWidth,
		NativeHeight:          nativeHeight,
		SHA256:                published.SHA256,
		VisualContentObserved: false,
		OutputProcessing:      options.OutputOptions,
	}
	encoded, err := json.Marshal(artifactData)
	if err != nil {
		return chatImageResult{}, record, configErr(fmt.Errorf("encode image artifact metadata: %w", err))
	}
	result = chatImageResult{
		Published:     published,
		NativeWidth:   nativeWidth,
		NativeHeight:  nativeHeight,
		Style:         style,
		Artifact:      "HOLLIS_IMAGE_ARTIFACT " + string(encoded),
		OutputOptions: options.OutputOptions,
	}
	record.ResponseBytes = len(result.Artifact)
	return result, record, nil
}

func toChatImageCLIError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutErr(fmt.Errorf("image turn timed out: %w", err))
	}
	if errors.Is(err, context.Canceled) {
		return transportErr(fmt.Errorf("image turn canceled: %w", err))
	}
	return toImageCLIError(err)
}

func runChatImageTurn(ctx context.Context, st *store.Store, conv store.Conversation, prompt string, options chatImageOptions) (chatImageResult, error) {
	turnCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		turnCtx, cancel = context.WithTimeout(turnCtx, options.Timeout)
		defer cancel()
	}
	unlock, err := st.LockContinuation(turnCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return chatImageResult{}, timeoutErr(fmt.Errorf("wait for chat continuation lock: %w", err))
		}
		if errors.Is(err, context.Canceled) {
			return chatImageResult{}, transportErr(fmt.Errorf("wait for chat continuation lock: %w", err))
		}
		return chatImageResult{}, configErr(fmt.Errorf("serialize chat continuation: %w", err))
	}
	defer unlock()

	history, err := st.Messages(conv.ID)
	if err != nil {
		return chatImageResult{}, configErr(err)
	}
	result, record, runErr := executeChatImageTurn(turnCtx, history, prompt, options)
	if runErr != nil {
		if err := recordFailedRun(st, conv.ID, record); err != nil {
			return chatImageResult{}, configErr(fmt.Errorf("record image run diagnostics: %w", err))
		}
		return chatImageResult{}, runErr
	}
	if err := st.AppendTurn(conv.ID, prompt, result.Artifact, record); err != nil {
		return chatImageResult{}, configErr(fmt.Errorf("store chat image turn: %w", err))
	}
	return result, nil
}

func runFirstChatImageTurn(ctx context.Context, st *store.Store, model runner.Model, prompt string, options chatImageOptions) (chatImageResult, store.Conversation, error) {
	turnCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		turnCtx, cancel = context.WithTimeout(turnCtx, options.Timeout)
		defer cancel()
	}
	result, record, runErr := executeChatImageTurn(turnCtx, nil, prompt, options)
	if runErr != nil {
		if err := recordFailedRun(st, "", record); err != nil {
			return chatImageResult{}, store.Conversation{}, configErr(fmt.Errorf("record image run diagnostics: %w", err))
		}
		return chatImageResult{}, store.Conversation{}, runErr
	}
	conv, err := st.CreateConversationWithTurn(string(model), truncateTitle(prompt), prompt, result.Artifact, record)
	if err != nil {
		return chatImageResult{}, store.Conversation{}, configErr(fmt.Errorf("store new chat image turn: %w", err))
	}
	return result, conv, nil
}

func writeChatImageResult(cmd *cobra.Command, result chatImageResult, conv store.Conversation, flags *rootFlags) error {
	data := map[string]any{
		"conversation_id":         conv.ID,
		"operation":               "image_generation",
		"model_requested":         conv.Model,
		"model_used":              "image_generation",
		"path":                    result.Published.Path,
		"format":                  result.Published.Format,
		"style":                   result.Style,
		"bytes":                   result.Published.Bytes,
		"width":                   result.Published.Width,
		"height":                  result.Published.Height,
		"native_width":            result.NativeWidth,
		"native_height":           result.NativeHeight,
		"checksum":                result.Published.SHA256,
		"visual_content_observed": false,
		"output_processing":       result.OutputOptions,
	}
	if flags.asJSON {
		return printJSONFilteredTo(cmd.OutOrStdout(), data, flags)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "conversation_id: %s\n", conv.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Saved PNG to %s\n", result.Published.Path)
	return nil
}
