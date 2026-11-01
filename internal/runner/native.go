// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kamenxrider/hollis/internal/subproc"
)

// NativeProtocol and NativeVersion are the matched helper contract this CLI
// speaks. A protocol bump is deliberate: guided output needs an operation and
// event fields an older helper would silently ignore.
const NativeProtocol = 2
const NativeVersion = "0.5.0"
const NativeMaxPromptBytes = 128 << 10
const nativeMaxOutputBytes = 8 << 20

// ValidProtocol reports whether n is a usable helper protocol pin: a positive
// integer no greater than 99. Plugins, the packager and the runtime-record
// producer enforce the same bound, so a pin outside it is an invalid receipt
// rather than a protocol era to guess at.
func ValidProtocol(n int) bool { return n >= 1 && n <= 99 }

// platformProbeTimeout bounds the sw_vers platform probe independently of the
// invoking call's deadline. Variable so offline fixtures can shorten it.
var platformProbeTimeout = 5 * time.Second

// helperProbeTimeout bounds the non-generative --protocol-version probe that
// guards a guided-output request. Variable so offline fixtures can shorten it.
var helperProbeTimeout = 5 * time.Second

// swVersPath is the platform probe binary. Variable so offline fixtures can
// substitute a stand-in without invoking the real probe.
var swVersPath = "/usr/bin/sw_vers"

// LocalStatus describes a non-generative availability check, not inference proof.
// ContextSize is the measured context window, present only where the helper
// reports it; absent signals stay absent rather than zero-filled.
type LocalStatus struct {
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
	Protocol    int    `json:"protocol"`
	Version     string `json:"version"`
	ContextSize int    `json:"context_size,omitempty"`
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
	// The probe is bounded by its own deadline as well as the caller's: a wedged
	// sw_vers must not hold a status or completion call open.
	result, err := subproc.Run(ctx, subproc.Spec{
		Name:      swVersPath,
		Args:      []string{"-productVersion"},
		Timeout:   platformProbeTimeout,
		StdoutCap: 4096,
	})
	if err != nil || result.WaitErr != nil {
		return errors.New("could not determine macOS version for native local")
	}
	major, err := strconv.Atoi(strings.Split(strings.TrimSpace(string(result.Stdout)), ".")[0])
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
	result, _, err := r.invoke(ctx, model, "complete", prompt, nil, nil)
	return result, err
}
func (r *NativeRunner) Stream(ctx context.Context, model Model, prompt string, emit func(string) error) (Completion, error) {
	if emit == nil {
		return Completion{}, nativeError(KindUsage, "stream callback is required")
	}
	result, _, err := r.invoke(ctx, model, "stream", prompt, emit, nil)
	return result, err
}

// RunSchema requests guided output. The schema must already be parsed: there is
// no path here that forwards an unvalidated schema to the helper.
func (r *NativeRunner) RunSchema(ctx context.Context, model Model, prompt string, schema *Schema) (Completion, error) {
	if schema == nil {
		return Completion{Model: model}, nativeError(KindUsage, "guided output requires a schema")
	}
	result, _, err := r.invoke(ctx, model, "schema", prompt, nil, schema)
	return result, err
}

func (r *NativeRunner) NativeStatus(ctx context.Context) (LocalStatus, error) {
	_, status, err := r.invoke(ctx, ModelLocal, "status", "", nil, nil)
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

// requireGuidedSupport asks the resolved helper which protocol it speaks
// before a schema request spawns. Guided output is a protocol-2 operation: an
// older helper answers a protocol-2 request with an error event at best, so the
// refusal happens here with an actionable message instead. The probe is the
// non-generative --protocol-version command, and its output is never
// propagated beyond the parsed integer.
func (r *NativeRunner) requireGuidedSupport(ctx context.Context, path string) error {
	timeout := helperProbeTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, runErr := subproc.Run(probeCtx, subproc.Spec{
		Name:      path,
		Args:      []string{"--protocol-version"},
		StdoutCap: 4096,
		StderrCap: -1,
	})
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		// No model request happened, so the generic "native local request
		// timed out" wording (and the CLI's raise-your-timeout hint) would
		// describe something that never occurred. The probe has its own
		// deadline and its own sentence.
		re := nativeError(KindTimeout, "native helper did not answer --protocol-version within the probe deadline")
		re.ProbeTimeout = true
		return re
	}
	if probeCtx.Err() != nil {
		return nativeError(KindContextCanceled, "native local request canceled")
	}
	if runErr != nil || !res.Started || res.WaitErr != nil {
		return nativeError(KindNativeFailed, "native helper could not report its supported protocol")
	}
	reported, convErr := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if convErr != nil || !ValidProtocol(reported) {
		return nativeError(KindNativeFailed, "native helper reported an unsupported protocol version")
	}
	switch {
	case reported < NativeProtocol:
		return nativeError(KindUsage, "native helper does not support guided output; install the matching bundle")
	case reported > NativeProtocol:
		return nativeError(KindNativeProtocol, "native helper version, protocol or model mismatch; install the matching bundle")
	default:
		return nil
	}
}

type nativeRequest struct {
	Protocol  int             `json:"protocol"`
	Operation string          `json:"operation"`
	Prompt    string          `json:"prompt,omitempty"`
	Schema    json.RawMessage `json:"schema,omitempty"`
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
	// ContextSize is the measured context window a protocol-2 status reports.
	ContextSize *int `json:"context_size,omitempty"`
	// Structured is the guided-output object. It is accepted only on a
	// terminal complete event for a schema request, and only after this
	// package re-validates it against the requested schema.
	Structured json.RawMessage `json:"structured,omitempty"`
}

// invoke never retries, truncates, or repairs model output. Complete events must
// be followed by clean EOF and a successful process exit before success is returned.
func (r *NativeRunner) invoke(parent context.Context, model Model, operation, prompt string, emit func(string) error, schema *Schema) (Completion, LocalStatus, error) {
	result := Completion{Model: model}
	status := LocalStatus{}
	fail := func(err error) (Completion, LocalStatus, error) { return Completion{Model: model}, status, err }
	if model != ModelLocal {
		return fail(nativeError(KindUsage, "native backend only supports model local"))
	}
	if operation == "schema" && schema == nil {
		return fail(nativeError(KindUsage, "guided output requires a schema"))
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
	if operation == "schema" {
		// Refuse a guided request before the request spawns when the paired
		// helper predates the operation. The probe is the helper's metadata
		// command: it loads no model and never reads the request, so the
		// refusal costs no inference.
		if err := r.requireGuidedSupport(ctx, path); err != nil {
			return fail(err)
		}
	}
	var requestSchema json.RawMessage
	if schema != nil {
		requestSchema = schema.Bytes()
	}
	request, err := json.Marshal(nativeRequest{Protocol: NativeProtocol, Operation: operation, Prompt: prompt, Schema: requestSchema})
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
		// Signals the design pins to one place in the sequence: the context
		// window belongs to status, and a structured object belongs to a
		// terminal guided completion. Anything else is a protocol error, never
		// a value to be picked up wherever it appears.
		if event.ContextSize != nil && event.Event != "status" {
			return nativeError(KindNativeProtocol, "unexpected native context size")
		}
		if len(event.Structured) != 0 && event.Event != "complete" {
			return nativeError(KindNativeProtocol, "unexpected structured output")
		}
		switch event.Event {
		case "status":
			if ready || event.Available == nil {
				return nativeError(KindNativeProtocol, "invalid native status sequence")
			}
			// Build and fully validate the candidate before it becomes the
			// reported status. A rejected event must not leave a half-filled
			// status behind: the failure result carries it, and a diagnostic
			// caller must never see available:true from an event we refused.
			candidate := LocalStatus{Available: *event.Available, Reason: event.Reason, Protocol: event.Protocol, Version: event.Version}
			if event.ContextSize != nil {
				if *event.ContextSize <= 0 {
					return nativeError(KindNativeProtocol, "invalid native context size")
				}
				candidate.ContextSize = *event.ContextSize
			}
			ready = true
			status = candidate
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
			if operation == "schema" {
				// The helper's SDK decode is necessary but not trusted: the
				// returned object is re-validated here against the same
				// requested subset. No retry, repair or fallback.
				if len(event.Structured) == 0 {
					return nativeError(KindNativeProtocol, "native guided response lacks structured output")
				}
				if err := schema.Validate(event.Structured); err != nil {
					return nativeError(KindNativeProtocol, "returned payload does not match the requested schema")
				}
				result.Structured = event.Structured
			} else if len(event.Structured) != 0 {
				return nativeError(KindNativeProtocol, "unexpected structured output")
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
		// A child that never started because the governing deadline expired is
		// a timeout, not a transport failure. Check the context before the
		// missing StartErr: an expired run reports no start error, and the
		// stale pipe message would blame the helper for the caller's deadline.
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return fail(ctxErr)
		}
		if res.StartErr == nil {
			return fail(nativeError(KindTransport, "could not open native output pipe"))
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
func (r *RoutedRunner) RunSchema(ctx context.Context, model Model, prompt string, schema *Schema) (Completion, error) {
	// Guided output is a native-only capability. Shortcut routes have no schema
	// operation, so the refusal is a usage error before anything spawns.
	if model != ModelLocal {
		return Completion{Model: model}, nativeError(KindUsage, "guided output is supported only for model local")
	}
	return r.Native.RunSchema(ctx, model, prompt, schema)
}
func (r *RoutedRunner) NativeStatus(ctx context.Context) (LocalStatus, error) {
	return r.Native.NativeStatus(ctx)
}

// ShortcutTransport exposes configuration and discovery of existing bridges.
func (r *RoutedRunner) ShortcutTransport() *ShortcutRunner { return r.ShortcutRunner }
