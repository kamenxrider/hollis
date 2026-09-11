// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"bufio"
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
	"syscall"
	"time"
	"unicode/utf8"
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
	cmd := exec.CommandContext(ctx, path)
	cmd.Stdin = bytes.NewReader(append(request, '\n'))
	// The helper's stderr can contain framework diagnostics. Do not propagate or
	// accumulate it: protocol errors provide deliberately prompt-free diagnostics.
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(nativeError(KindTransport, "could not open native output pipe"))
	}
	if err := cmd.Start(); err != nil {
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return fail(ctxErr)
		}
		return fail(nativeError(KindTransport, "could not launch native helper"))
	}
	// Close stdout as well as killing the group when cancellation occurs, so a
	// descendant retaining an inherited pipe cannot hold the scanner indefinitely.
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stdout.Close()
		case <-stopped:
		}
	}()
	defer close(stopped)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), nativeMaxOutputBytes)
	ready, terminal := false, false
	previous := ""
	total := 0
	var protocolErr error
	for scanner.Scan() {
		total += len(scanner.Bytes())
		if total > 64*nativeMaxOutputBytes {
			protocolErr = nativeError(KindNativeProtocol, "native output exceeded protocol limit")
			break
		}
		if terminal {
			protocolErr = nativeError(KindNativeProtocol, "native helper emitted data after completion")
			break
		}
		if !utf8.Valid(scanner.Bytes()) {
			protocolErr = nativeError(KindNativeProtocol, "native event is not valid UTF-8")
			break
		}
		var event nativeEvent
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			protocolErr = nativeError(KindNativeProtocol, "invalid native event")
			break
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			protocolErr = nativeError(KindNativeProtocol, "extra native event data")
			break
		}
		if event.Protocol != NativeProtocol || event.Version != NativeVersion || event.Model != ModelLocal {
			protocolErr = nativeError(KindNativeProtocol, "native helper version, protocol or model mismatch; install the matching bundle")
			break
		}
		switch event.Event {
		case "status":
			if ready || event.Available == nil {
				protocolErr = nativeError(KindNativeProtocol, "invalid native status sequence")
				break
			}
			ready = true
			status = LocalStatus{*event.Available, event.Reason, event.Protocol, event.Version}
			if operation == "status" {
				terminal = true
			} else if !status.Available {
				protocolErr = nativeError(KindLocalUnavailable, "native local model is unavailable: "+event.Reason)
			}
		case "snapshot":
			if !ready || operation != "stream" || event.Text == nil {
				protocolErr = nativeError(KindNativeProtocol, "unexpected native snapshot")
				break
			}
			if !strings.HasPrefix(*event.Text, previous) {
				protocolErr = nativeError(KindNativeProtocol, "native model revised earlier streamed text; response incomplete")
				break
			}
			delta := (*event.Text)[len(previous):]
			if delta != "" {
				if err := emit(delta); err != nil {
					protocolErr = err
					break
				}
			}
			previous = *event.Text
		case "complete":
			if !ready || operation == "status" || event.Text == nil || *event.Text == "" {
				protocolErr = nativeError(KindNativeProtocol, "invalid native completion")
				break
			}
			if operation == "stream" {
				if *event.Text != previous || event.Usage != nil {
					protocolErr = nativeError(KindNativeProtocol, "stream completion does not match snapshots or includes unqualified usage")
					break
				}
			} else if !event.Usage.valid() {
				protocolErr = nativeError(KindNativeProtocol, "native complete response lacks valid measured usage")
				break
			}
			result.Text = *event.Text
			if event.Usage != nil {
				result.Usage = &Usage{*event.Usage.InputTokens, *event.Usage.OutputTokens, *event.Usage.ReasoningTokens}
			}
			terminal = true
		case "error":
			switch event.Kind {
			case KindContextCapacity:
				protocolErr = nativeError(event.Kind, "local model context capacity exceeded; shorten the conversation explicitly or choose another model; no messages were removed")
			case KindRequestDeclined, KindRateLimited, KindLocalUnavailable, KindNativeFailed:
				protocolErr = nativeError(event.Kind, "native local request failed: "+string(event.Kind))
			default:
				protocolErr = nativeError(KindNativeProtocol, "unrecognized native failure")
			}
		default:
			protocolErr = nativeError(KindNativeProtocol, "unknown native event")
		}
		if protocolErr != nil {
			break
		}
	}
	if protocolErr != nil || scanner.Err() != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail(nativeError(KindTimeout, "native local request timed out"))
		}
		return fail(nativeError(KindContextCanceled, "native local request canceled"))
	}
	if protocolErr != nil {
		return fail(protocolErr)
	}
	if scanErr != nil {
		return fail(nativeError(KindNativeProtocol, "could not read native output"))
	}
	if waitErr != nil {
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
