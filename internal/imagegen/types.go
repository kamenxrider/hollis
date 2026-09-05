// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package imagegen contains the injectable file-output transport for a
// Shortcut that generates one PNG image. Tests use synthetic subprocesses.
//
// Generation returns a verified, private staging file. Separate helpers
// process and publish it. The package does not discover or install a Shortcut,
// or claim that an image-generation bridge is available.
package imagegen

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

const (
	// DefaultShortcutsPath is Apple's command-line transport. Tests must inject
	// a command factory and never invoke this path.
	DefaultShortcutsPath = "/usr/bin/shortcuts"

	// MaxTimeout is the hard per-request ceiling for image generation.
	MaxTimeout = 120 * time.Second

	// MaxPromptBytes bounds the UTF-8 prompt sent to a bridge.
	MaxPromptBytes = 128 << 10

	// MaxOutputBytes bounds a generated PNG before it is decoded or returned.
	MaxOutputBytes int64 = 16 << 20

	// MaxPixels bounds the decoded image dimensions.
	MaxPixels int64 = 16_000_000

	// processWaitDelay lets os/exec reap a process whose pipes or descendants
	// do not close immediately after the owned process group is killed.
	processWaitDelay = 2 * time.Second
)

// Request is one bounded image-generation operation.
//
// BridgeRef is passed as one argument to the Shortcuts CLI. It must be an
// explicit configured or positively discovered reference; this package does
// not resolve names, UUIDs, or availability. Timeout is optional: zero uses
// the transport default, while values above MaxTimeout are rejected.
type Request struct {
	Prompt    string
	BridgeRef string
	// Style selects the style in a parameterized JSON bridge. An empty Style
	// preserves the legacy plain-text protocol used by fixed-style bridges.
	Style   string
	Timeout time.Duration
}

// StyleLabel validates a Hollis style ID and returns the exact label accepted
// by the Image Playground Shortcut action.
func StyleLabel(style string) (string, bool) {
	switch style {
	case "any":
		return "Any Style", true
	case "animation":
		return "Animation", true
	case "genmoji":
		return "Genmoji", true
	case "illustration":
		return "Illustration", true
	case "sketch":
		return "Sketch", true
	case "chatgpt":
		return "ChatGPT", true
	default:
		return "", false
	}
}

// Result is the verified image owned by one successful Generate call.
//
// Path points into a private staging directory. The caller must call Cleanup
// after it has consumed or published the file. Cleanup is idempotent and
// removes only paths created by this operation.
type Result struct {
	Path   string
	Bytes  int64
	Width  int
	Height int
	SHA256 string

	cleanup *cleanupState
}

// Cleanup removes the private staging file and directory owned by the result.
// A cleanup failure is returned so callers can report an incomplete operation.
// Calling Cleanup more than once returns the first cleanup result.
func (r Result) Cleanup() error {
	if r.cleanup == nil {
		return nil
	}
	return r.cleanup.run()
}

// Generator is the narrow contract consumed by the CLI or other callers.
// Implementations return a complete verified PNG file, never image bytes as
// model text.
type Generator interface {
	Generate(context.Context, Request) (Result, error)
}

// CommandFactory constructs the child process used by a ShortcutTransport.
// The factory is an offline test seam: production uses exec.CommandContext,
// while tests can return an in-process Go test helper command. The transport
// installs process-group and WaitDelay handling on the returned command.
type CommandFactory func(context.Context, string, ...string) *exec.Cmd

// ShortcutTransport invokes one explicit image-generation Shortcut and stages
// its PNG output. It is safe to use concurrently when the CommandFactory is
// safe for concurrent use (the default factory is).
type ShortcutTransport struct {
	// ShortcutsPath is the executable passed to CommandFactory. The default is
	// Apple's /usr/bin/shortcuts path.
	ShortcutsPath string
	// TempDir is the parent for per-request 0700 staging directories. An empty
	// value uses the operating system temporary directory.
	TempDir string
	// Timeout is used when Request.Timeout is zero. Zero selects MaxTimeout.
	Timeout time.Duration
	// Command replaces exec.CommandContext for provider-free tests.
	Command CommandFactory
}

// New returns a transport with the hard, provider-independent defaults.
func New() *ShortcutTransport {
	return &ShortcutTransport{
		ShortcutsPath: DefaultShortcutsPath,
		Timeout:       MaxTimeout,
	}
}

// ErrorKind classifies a transport failure without exposing child stdout.
type ErrorKind string

const (
	KindUsage            ErrorKind = "usage"
	KindEmptyPrompt      ErrorKind = "empty_prompt"
	KindInvalidPrompt    ErrorKind = "invalid_prompt"
	KindMissingBridge    ErrorKind = "missing_bridge"
	KindTimeout          ErrorKind = "timeout"
	KindCanceled         ErrorKind = "canceled"
	KindSpawn            ErrorKind = "spawn"
	KindNonZeroExit      ErrorKind = "nonzero_exit"
	KindSessionLocked    ErrorKind = "session_locked"
	KindNoOutput         ErrorKind = "no_output"
	KindInvalidPNG       ErrorKind = "invalid_png"
	KindImageTooLarge    ErrorKind = "image_too_large"
	KindCleanup          ErrorKind = "cleanup"
	KindOutputInspection ErrorKind = "output_inspection"
)

var (
	// ErrEmptyPrompt is returned before a child process is spawned.
	ErrEmptyPrompt = errors.New("image prompt is empty")
	// ErrInvalidPrompt is returned for a prompt that is not valid UTF-8.
	ErrInvalidPrompt = errors.New("image prompt is not valid UTF-8")
	// ErrMissingBridge is returned when no explicit bridge reference is given.
	ErrMissingBridge = errors.New("image bridge reference is required")
	// ErrTimeout marks a bounded deadline failure.
	ErrTimeout = errors.New("image generation timed out")
	// ErrCanceled marks caller cancellation.
	ErrCanceled = errors.New("image generation canceled")
	// ErrNonZeroExit marks a bridge process that did not complete successfully.
	ErrNonZeroExit = errors.New("image bridge exited unsuccessfully")
	// ErrSessionLocked marks the observed Shortcuts refusal while macOS is locked.
	ErrSessionLocked = errors.New("Shortcuts requires an unlocked Mac session")
	// ErrInvalidTimeout marks a non-positive configured timeout.
	ErrInvalidTimeout = errors.New("image generation timeout must be positive")
	// ErrTimeoutTooLarge marks a timeout above the hard product ceiling.
	ErrTimeoutTooLarge = errors.New("image generation timeout exceeds the 120-second limit")
	// ErrNoOutput marks an exit without a non-empty staged file.
	ErrNoOutput = errors.New("image generation produced no output file")
	// ErrInvalidPNG marks output that is not a complete decodable PNG.
	ErrInvalidPNG = errors.New("image generation produced an invalid PNG")
	// ErrImageTooLarge marks a file or dimensions above the product ceiling.
	ErrImageTooLarge = errors.New("generated PNG exceeds the image limit")
	// ErrCleanup marks an owned staging cleanup failure.
	ErrCleanup = errors.New("image staging cleanup failed")
)

// Error is a classified, wrapped transport error. Child stdout is never
// copied into Error; it may contain binary image data. Stderr is retained only
// as a bounded human-facing hint by the transport.
type Error struct {
	Kind      ErrorKind
	BridgeRef string
	ExitCode  int
	Stderr    string
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return string(e.Kind)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type cleanupState struct {
	once  sync.Once
	paths []string
	dir   string
	err   error
}

func (c *cleanupState) run() error {
	c.once.Do(func() {
		for _, path := range c.paths {
			if err := removeOwnedPath(path); err != nil {
				c.err = errors.Join(c.err, err)
			}
		}
		if err := removeOwnedPath(c.dir); err != nil {
			c.err = errors.Join(c.err, err)
		}
		if c.err != nil {
			c.err = errors.Join(ErrCleanup, c.err)
		}
	})
	return c.err
}
