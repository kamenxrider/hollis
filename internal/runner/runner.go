// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package runner invokes Apple Intelligence through the AFM bridge shortcuts.
//
// Every rule here is earned by a measured failure; see
// results/transport-and-persistence-2026-09-01.md and plan §36:
//
//  1. Always pass --output-type public.plain-text (the default output is RTF).
//  2. Capture stdout via pipes, never a TTY (a TTY silently suppresses output).
//  3. Always impose a context deadline and kill the child — empty input hangs
//     `shortcuts run` forever and macOS has no timeout(1).
//  4. Reject empty prompts before spawning.
//  5. Treat exit 0 + empty stdout as shortcut_no_output, never as a response.
//  6. Don't expect a trailing newline; don't store one that wasn't there.
//  7. Reference bridges by UUID, not name (names collide and get renamed;
//     verified 2026-09-01 that `shortcuts run <UUID>` is accepted).
//  8. Default concurrency 1, configurable — 4 parallel runs proven clean.
package runner

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Model identifies which Apple Intelligence tier to call.
type Model string

const (
	ModelCloud    Model = "cloud"
	ModelCloudPro Model = "cloud-pro"
	ModelOnDevice Model = "on-device"
	ModelChatGPT  Model = "chatgpt"
)

// Models is the exhaustive set of concrete model tiers. ModelAuto is a
// strategy, not a tier: it tries the default tier first and falls back to
// the on-device model on failure (Apple's documented PCC pattern), so it
// is valid for selection but has no bridge of its own.
var Models = []Model{ModelCloud, ModelCloudPro, ModelOnDevice, ModelChatGPT}

// ModelAuto selects the default tier (cloud) and falls back to the
// on-device model when the primary run fails with a transport-class
// error. Explicit tier selections never fall back.
const ModelAuto Model = "auto"

// Valid reports whether m is selectable: any concrete tier or auto.
func (m Model) Valid() bool {
	if m == ModelAuto {
		return true
	}
	for _, v := range Models {
		if m == v {
			return true
		}
	}
	return false
}

// Bridge UUIDs measured from the installed signed shortcuts
// (results/transport-and-persistence-2026-09-01.md). UUIDs are stable across
// renames; the imported display names carry a `.signed` suffix from the file
// name and can be renamed in Shortcuts.app at any time.
const (
	BridgeUUIDCloud    = "BD8CDC56-7CB8-418D-9B02-9D33AB911BF0"
	BridgeUUIDCloudPro = "DBB6E472-CBC6-4421-8D32-9D4543D5CDE6"
	BridgeUUIDOnDevice = "E530AE25-3C3C-4B11-88AF-A66F74039F88"
	BridgeUUIDChatGPT  = "24B4B536-571B-49D9-9519-B644281C8B08"
)

// DefaultShortcutsPath is the Apple-provided CLI used as transport.
const DefaultShortcutsPath = "/usr/bin/shortcuts"

// DefaultTimeout bounds a single shortcut run. Measured latency is ~1s; the
// plan sets a 30s default with a 120s ceiling.
const DefaultTimeout = 30 * time.Second

// Kind classifies the measured failure surface of `shortcuts run`.
type Kind string

const (
	KindEmptyPrompt     Kind = "empty_prompt"       // refused before spawn (hangs forever)
	KindNoOutput        Kind = "shortcut_no_output" // exit 0 + empty stdout (also what a TTY run looks like)
	KindShortcutMissing Kind = "shortcut_missing"   // exit 1
	KindUsage           Kind = "usage"              // exit 64
	KindSIGABRT         Kind = "sigabrt"            // exit 134 (the -o /dev/stdout crash; never used by us)
	KindTimeout         Kind = "timeout"            // deadline hit, child killed
	KindTransport       Kind = "transport"          // anything else
)

// Error is a classified runner failure. Kind maps to a stable CLI exit code.
type Error struct {
	Kind Kind
	// Ref is the bridge identifier (UUID) that was invoked, when known.
	Ref string
	// ExitCode is the child's exit status, or -1 when it never exited.
	ExitCode int
	// Stderr carries the child's stderr verbatim (Apple prints useful hints).
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := "shortcuts transport error"
	if e.Err != nil {
		msg = e.Err.Error()
	}
	if e.ExitCode >= 0 {
		return fmt.Sprintf("%s (shortcut exit %d)", msg, e.ExitCode)
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// ErrEmptyPrompt is returned when the prompt is empty or whitespace only.
// We refuse to spawn: empty input makes `shortcuts run` hang forever.
var ErrEmptyPrompt = errors.New("empty prompt")

// ErrUnknownModel is returned for a Model outside runner.Models.
var ErrUnknownModel = errors.New("unknown model")

// Runner sends one prompt to Apple Intelligence and returns the plain-text
// response. Implementations must be safe for concurrent use.
type Runner interface {
	// Run submits prompt and returns the model response. The returned text
	// is the child's stdout exactly as produced (no trailing newline added
	// or stripped beyond what Apple emits).
	Run(ctx context.Context, model Model, prompt string) (string, error)
}
