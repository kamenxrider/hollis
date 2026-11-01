// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kamenxrider/hollis/internal/subproc"
)

const NativeProtocol = 1
const NativeVersion = "0.4.0"
const NativeMaxPromptBytes = 128 << 10
const nativeMaxOutputBytes = 8 << 20

// LocalStatus describes a non-generative availability check, not inference proof.
type LocalStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Protocol  int    `json:"protocol"`
	Version   string `json:"version"`
}

type NativeStatusRunner interface {
	NativeStatus(context.Context) (LocalStatus, error)
}

// NativeRunner invokes only the helper beside the resolved Hollis executable.
// Fields used to substitute fake processes remain private to provider-free tests.
type NativeRunner struct {
	Timeout       time.Duration
	helperPath    string
	platformCheck func(context.Context) error
}

func NewNative() *NativeRunner {
	return &NativeRunner{Timeout: DefaultTimeout}
}

func (r *NativeRunner) helper(ctx context.Context) (string, error) {
	check := nativePlatform
	if r.platformCheck != nil {
		check = r.platformCheck
	}
	if err := check(ctx); err != nil {
		return "", err
	}
	path := r.helperPath
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return "", err
		}
		path = filepath.Join(filepath.Dir(executable), "hollis-native")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("native local helper is missing or not executable; install the matching Hollis Apple Silicon bundle")
	}
	return path, nil
}

func nativePlatform(ctx context.Context) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return errors.New("native local requires Apple Silicon and macOS 27 or later")
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return errors.New("could not determine macOS version for native local")
	}
	major, err := strconv.Atoi(strings.Split(strings.TrimSpace(string(output)), ".")[0])
	if err != nil || major < 27 {
		return errors.New("native local requires macOS 27 or later")
	}
	return nil
}

func (r *NativeRunner) Run(ctx context.Context, model Model, prompt string) (string, Model, error) {
	result, err := r.RunComplete(ctx, model, prompt)
	return result.Text, model, err
}
func (r *NativeRunner) RunComplete(ctx context.Context, model Model, prompt string) (Completion, error) {
	result, _, err := r.invoke(ctx, model, "complete", prompt, nil)
	return result, err
}
func (r *NativeRunner) Stream(ctx context.Context, model Model, prompt string, emit func(string) error) (Completion, error) {
	if emit == nil {
		return Completion{}, nativeError(KindUsage, "stream callback is required")
	}
	result, _, err := r.invoke(ctx, model, "stream", prompt, emit)
	return result, err
}
func (r *NativeRunner) NativeStatus(ctx context.Context) (LocalStatus, error) {
	_, status, err := r.invoke(ctx, ModelLocal, "status", "", nil)
	return status, err
}

func nativeError(kind Kind, message string) *Error {
	return &Error{Kind: kind, ExitCode: -1, Err: errors.New(message)}
}

func nativeContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nativeError(KindTimeout, "native local request timed out")
	}
	if ctx.Err() != nil {
		return nativeError(KindContextCanceled, "native local request canceled")
	}
	return nil
}

type nativeRequest struct {
	Protocol  int    `json:"protocol"`
	Operation string `json:"operation"`
	Prompt    string `json:"prompt,omitempty"`
}
type nativeUsage struct {
	InputTokens     *int `json:"input_tokens"`
	OutputTokens    *int `json:"output_tokens"`
	ReasoningTokens *int `json:"reasoning_tokens"`
}

func (u *nativeUsage) valid() bool {
	return u != nil && u.InputTokens != nil && u.OutputTokens != nil && u.ReasoningTokens != nil && *u.InputTokens >= 0 && *u.OutputTokens >= 0 && *u.ReasoningTokens >= 0 && *u.ReasoningTokens <= *u.OutputTokens
}

type nativeEvent struct {
	Protocol  int          `json:"protocol"`
	Version   string       `json:"version"`
	Model     Model        `json:"model"`
	Event     string       `json:"event"`
	Available *bool        `json:"available,omitempty"`
	Reason    string       `json:"reason,omitempty"`
	Text      *string      `json:"text,omitempty"`
	Usage     *nativeUsage `json:"usage,omitempty"`
	Kind      Kind         `json:"kind,omitempty"`
}

// invoke never retries, truncates, or repairs model output. Complete events must
// be followed by clean EOF and a successful process exit before success is returned.
func (r *NativeRunner) invoke(parent context.Context, model Model, operation, prompt string, emit func(string) error) (Completion, LocalStatus, error) {
	result := Completion{Model: model}
	status := LocalStatus{}
	fail := func(err error) (Completion, LocalStatus, error) { return Completion{Model: model}, status, err }
	if model != ModelLocal {
		return fail(nativeError(KindUsage, "native backend only supports model local"))
	}
	if operation != "status" {
		if strings.TrimSpace(prompt) == "" {
			return fail(nativeError(KindEmptyPrompt, "empty prompt"))
		}
		if !utf8.ValidString(prompt) {
			return fail(nativeError(KindUsage, "native prompt must be valid UTF-8"))
		}
		if len(prompt) > NativeMaxPromptBytes {
			return fail(nativeError(KindContextCapacity, "rendered conversation exceeds Hollis's 128 KiB input limit; no messages were removed"))
		}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if deadline, ok := parent.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := nativeContextError(ctx); err != nil {
		return fail(err)
	}
	path, err := r.helper(ctx)
	if err != nil {
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return fail(ctxErr)
		}
		return fail(nativeError(KindLocalUnavailable, err.Error()))
	}
	request, err := json.Marshal(nativeRequest{NativeProtocol, operation, prompt})
	if err != nil {
		return fail(nativeError(KindTransport, "could not encode native request"))
	}
	// The helper's stderr can contain framework diagnostics. Do not propagate or
	// accumulate it: protocol errors provide deliberately prompt-free diagnostics.
	ready, terminal := false, false
	previous := ""
	total := 0
	handle := func(line []byte) error {
		total += len(line)
		if total > 64*nativeMaxOutputBytes {
			return nativeError(KindNativeProtocol, "native output exceeded protocol limit")
		}
		if terminal {
			return nativeError(KindNativeProtocol, "native helper emitted data after completion")
		}
		if !utf8.Valid(line) {
			return nativeError(KindNativeProtocol, "native event is not valid UTF-8")
		}
		var event nativeEvent
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return nativeError(KindNativeProtocol, "invalid native event")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return nativeError(KindNativeProtocol, "extra native event data")
		}
		if event.Protocol != NativeProtocol || event.Version != NativeVersion || event.Model != ModelLocal {
			return nativeError(KindNativeProtocol, "native helper version, protocol or model mismatch; install the matching bundle")
		}
		switch event.Event {
		case "status":
			if ready || event.Available == nil {
				return nativeError(KindNativeProtocol, "invalid native status sequence")
			}
			ready = true
			status = LocalStatus{*event.Available, event.Reason, event.Protocol, event.Version}
			if operation == "status" {
				terminal = true
			} else if !status.Available {
				return nativeError(KindLocalUnavailable, "native local model is unavailable: "+event.Reason)
			}
		case "snapshot":
			if !ready || operation != "stream" || event.Text == nil {
				return nativeError(KindNativeProtocol, "unexpected native snapshot")
			}
			if !strings.HasPrefix(*event.Text, previous) {
				return nativeError(KindNativeProtocol, "native model revised earlier streamed text; response incomplete")
			}
			delta := (*event.Text)[len(previous):]
			if delta != "" {
				if err := emit(delta); err != nil {
					return err
				}
			}
			previous = *event.Text
		case "complete":
			if !ready || operation == "status" || event.Text == nil || *event.Text == "" {
				return nativeError(KindNativeProtocol, "invalid native completion")
			}
			if operation == "stream" {
				if *event.Text != previous || event.Usage != nil {
					return nativeError(KindNativeProtocol, "stream completion does not match snapshots or includes unqualified usage")
				}
			} else if !event.Usage.valid() {
				return nativeError(KindNativeProtocol, "native complete response lacks valid measured usage")
			}
			result.Text = *event.Text
			if event.Usage != nil {
				result.Usage = &Usage{*event.Usage.InputTokens, *event.Usage.OutputTokens, *event.Usage.ReasoningTokens}
			}
			terminal = true
		case "error":
			switch event.Kind {
			case KindContextCapacity:
				return nativeError(event.Kind, "local model context capacity exceeded; shorten the conversation explicitly or choose another model; no messages were removed")
			case KindRequestDeclined, KindRateLimited, KindLocalUnavailable, KindNativeFailed:
				return nativeError(event.Kind, "native local request failed: "+string(event.Kind))
			default:
				return nativeError(KindNativeProtocol, "unrecognized native failure")
			}
		default:
			return nativeError(KindNativeProtocol, "unknown native event")
		}
		return nil
	}
	// The shared lifecycle owns the output pipe, kills the group when the
	// protocol is rejected or the deadline expires, and closes the read end so
	// a descendant retaining an inherited pipe cannot hold the scan.
	res, runErr := subproc.Scan(ctx, subproc.Spec{
		Name:      path,
		Stdin:     append(request, '\n'),
		StderrCap: -1,
		LineCap:   nativeMaxOutputBytes,
	}, handle)
	if !res.Started {
		if res.StartErr == nil {
			return fail(nativeError(KindTransport, "could not open native output pipe"))
		}
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return fail(ctxErr)
		}
		return fail(nativeError(KindTransport, "could not launch native helper"))
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail(nativeError(KindTimeout, "native local request timed out"))
		}
		return fail(nativeError(KindContextCanceled, "native local request canceled"))
	}
	if res.EmitErr != nil {
		return fail(res.EmitErr)
	}
	if res.LineOverflow || res.ReadErr != nil || runErr != nil {
		return fail(nativeError(KindNativeProtocol, "could not read native output"))
	}
	if res.WaitErr != nil {
		return fail(nativeError(KindNativeFailed, "native helper exited unsuccessfully"))
	}
	if !terminal {
		return fail(nativeError(KindNativeProtocol, "native helper ended without completion"))
	}
	return result, status, nil
}

// RoutedRunner adds native local without changing ShortcutRunner or auto policy.
type RoutedRunner struct {
	*ShortcutRunner
	Native *NativeRunner
}

func NewRouted() *RoutedRunner { return &RoutedRunner{New(), NewNative()} }
func (r *RoutedRunner) Run(ctx context.Context, model Model, prompt string) (string, Model, error) {
	if model == ModelLocal {
		return r.Native.Run(ctx, model, prompt)
	}
	return r.ShortcutRunner.Run(ctx, model, prompt)
}
func (r *RoutedRunner) RunComplete(ctx context.Context, model Model, prompt string) (Completion, error) {
	if model == ModelLocal {
		return r.Native.RunComplete(ctx, model, prompt)
	}
	text, used, err := r.ShortcutRunner.Run(ctx, model, prompt)
	return Completion{Text: text, Model: used}, err
}
func (r *RoutedRunner) RunWithFallback(ctx context.Context, model Model, prompt string) (string, Model, Fallback, error) {
	if model == ModelLocal {
		text, used, err := r.Native.Run(ctx, model, prompt)
		return text, used, Fallback{}, err
	}
	return r.ShortcutRunner.RunWithFallback(ctx, model, prompt)
}
func (r *RoutedRunner) RunWithImages(ctx context.Context, model Model, prompt string, images []string) (string, Model, error) {
	if model == ModelLocal {
		return "", model, nativeError(KindUsage, "native local does not support image input")
	}
	return r.ShortcutRunner.RunWithImages(ctx, model, prompt, images)
}
func (r *RoutedRunner) Stream(ctx context.Context, model Model, prompt string, emit func(string) error) (Completion, error) {
	if model != ModelLocal {
		return Completion{}, nativeError(KindUsage, "streaming is supported only for model local")
	}
	return r.Native.Stream(ctx, model, prompt, emit)
}
func (r *RoutedRunner) NativeStatus(ctx context.Context) (LocalStatus, error) {
	return r.Native.NativeStatus(ctx)
}

// ShortcutTransport exposes configuration and discovery of existing bridges.
func (r *RoutedRunner) ShortcutTransport() *ShortcutRunner { return r.ShortcutRunner }
