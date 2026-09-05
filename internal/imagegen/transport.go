// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

var _ Generator = (*ShortcutTransport)(nil)

// Generate invokes the explicitly supplied bridge and stages one complete PNG
// in a private directory. The final destination is deliberately outside this
// package: callers such as the CLI own no-clobber publishing.
func (g *ShortcutTransport) Generate(ctx context.Context, req Request) (Result, error) {
	if err := validateRequest(ctx, req); err != nil {
		return Result{}, err
	}

	timeout, err := g.requestTimeout(req.Timeout)
	if err != nil {
		return Result{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stageDir, err := os.MkdirTemp(g.TempDir, "hollis-imagegen-")
	if err != nil {
		return Result{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("create private image staging directory: %w", err),
		}
	}
	// MkdirTemp currently creates 0700 directories, but enforce the contract
	// explicitly so a changed umask or platform implementation cannot widen it.
	if err := os.Chmod(stageDir, 0o700); err != nil {
		cleanupErr := (&cleanupState{dir: stageDir}).run()
		return Result{}, joinCleanup(fmt.Errorf("secure image staging directory: %w", err), cleanupErr)
	}

	cleanup := &cleanupState{dir: stageDir}
	fail := func(err error) (Result, error) {
		return Result{}, joinCleanup(err, cleanup.run())
	}

	input, err := bridgeInput(req)
	if err != nil {
		return fail(err)
	}
	promptPath, err := writePrompt(stageDir, input)
	if err != nil {
		return fail(&Error{
			Kind:      KindOutputInspection,
			BridgeRef: req.BridgeRef,
			ExitCode:  -1,
			Err:       fmt.Errorf("stage image prompt: %w", err),
		})
	}
	cleanup.paths = append(cleanup.paths, promptPath)

	outputPath := filepath.Join(stageDir, "image.png")
	if err := reserveOutput(outputPath); err != nil {
		return fail(&Error{
			Kind:      KindOutputInspection,
			BridgeRef: req.BridgeRef,
			ExitCode:  -1,
			Err:       fmt.Errorf("reserve image output: %w", err),
		})
	}
	cleanup.paths = append(cleanup.paths, outputPath)

	factory := g.Command
	if factory == nil {
		factory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, args...)
		}
	}
	shortcutsPath := g.ShortcutsPath
	if shortcutsPath == "" {
		shortcutsPath = DefaultShortcutsPath
	}
	args := []string{
		"run", req.BridgeRef,
		"--input-path", promptPath,
		"--output-path", outputPath,
		"--output-type", "public.png",
	}
	cmd := factory(runCtx, shortcutsPath, args...)
	if cmd == nil {
		return fail(&Error{
			Kind:      KindSpawn,
			BridgeRef: req.BridgeRef,
			ExitCode:  -1,
			Err:       errors.New("image command factory returned nil"),
		})
	}
	stderrBuffer := configureCommand(cmd)

	if err := cmd.Start(); err != nil {
		if runCtx.Err() != nil {
			return fail(contextError(ctx, runCtx, req.BridgeRef, timeout))
		}
		return fail(&Error{
			Kind:      KindSpawn,
			BridgeRef: req.BridgeRef,
			ExitCode:  -1,
			Err:       fmt.Errorf("start image bridge: %w", err),
		})
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	deadlineHit := false
	select {
	case waitErr = <-waitDone:
	case <-runCtx.Done():
		deadlineHit = true
		// cmd.Cancel is also installed for os/exec's context watcher. Calling
		// it here makes cancellation deterministic even when a test factory
		// returned exec.Command instead of exec.CommandContext.
		_ = cmd.Cancel()
		waitErr = <-waitDone
	}
	if deadlineHit {
		return fail(contextError(ctx, runCtx, req.BridgeRef, timeout))
	}
	if waitErr != nil {
		stderrText := strings.TrimSpace(stderrBuffer.String())
		exitCode := exitCode(waitErr)
		message := fmt.Errorf("image bridge exited with status %d: %w", exitCode, ErrNonZeroExit)
		if stderrText != "" {
			message = fmt.Errorf("%w: %s", message, stderrText)
		}
		return fail(&Error{
			Kind:      KindNonZeroExit,
			BridgeRef: req.BridgeRef,
			ExitCode:  exitCode,
			Stderr:    stderrText,
			Err:       message,
		})
	}

	result, err := verifyPNG(outputPath)
	if err != nil {
		return fail(&Error{
			Kind:      kindForOutputError(err),
			BridgeRef: req.BridgeRef,
			ExitCode:  0,
			Err:       err,
		})
	}
	result.cleanup = cleanup
	return result, nil
}

func validateRequest(ctx context.Context, req Request) error {
	if ctx == nil {
		return &Error{Kind: KindUsage, ExitCode: -1, Err: errors.New("image generation requires a non-nil context")}
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return &Error{Kind: KindEmptyPrompt, ExitCode: -1, Err: ErrEmptyPrompt}
	}
	if len(req.Prompt) > MaxPromptBytes {
		return &Error{Kind: KindUsage, ExitCode: -1, Err: errors.New("image prompt exceeds 128 KiB")}
	}
	if !utf8.ValidString(req.Prompt) {
		return &Error{Kind: KindInvalidPrompt, ExitCode: -1, Err: ErrInvalidPrompt}
	}
	if req.Style != "" {
		if _, ok := StyleLabel(req.Style); !ok {
			return &Error{Kind: KindUsage, ExitCode: -1, Err: fmt.Errorf("unknown image style %q", req.Style)}
		}
	}
	bridge := strings.TrimSpace(req.BridgeRef)
	if bridge == "" {
		return &Error{Kind: KindMissingBridge, ExitCode: -1, Err: ErrMissingBridge}
	}
	if strings.HasPrefix(bridge, "-") {
		return &Error{
			Kind: KindUsage, ExitCode: -1,
			Err: fmt.Errorf("bridge reference must not begin with '-': %w", ErrMissingBridge),
		}
	}
	if err := ctx.Err(); err != nil {
		return contextError(ctx, ctx, req.BridgeRef, 0)
	}
	return nil
}

func bridgeInput(req Request) (string, error) {
	if req.Style == "" {
		return req.Prompt, nil
	}
	label, ok := StyleLabel(req.Style)
	if !ok {
		return "", &Error{Kind: KindUsage, ExitCode: -1, Err: fmt.Errorf("unknown image style %q", req.Style)}
	}
	payload, err := json.Marshal(struct {
		Prompt string `json:"prompt"`
		Style  string `json:"style"`
	}{Prompt: req.Prompt, Style: label})
	if err != nil {
		return "", &Error{Kind: KindUsage, ExitCode: -1, Err: fmt.Errorf("encode image bridge input: %w", err)}
	}
	if len(payload) > MaxPromptBytes+256 {
		return "", &Error{Kind: KindUsage, ExitCode: -1, Err: errors.New("encoded image bridge input exceeds its limit")}
	}
	return string(payload), nil
}

func (g *ShortcutTransport) requestTimeout(requestTimeout time.Duration) (time.Duration, error) {
	timeout := requestTimeout
	if timeout == 0 {
		timeout = g.Timeout
		if timeout == 0 {
			timeout = MaxTimeout
		}
	}
	if timeout < 0 {
		return 0, &Error{Kind: KindUsage, ExitCode: -1, Err: ErrInvalidTimeout}
	}
	if timeout > MaxTimeout {
		return 0, &Error{Kind: KindUsage, ExitCode: -1, Err: ErrTimeoutTooLarge}
	}
	return timeout, nil
}

func writePrompt(dir, prompt string) (string, error) {
	file, err := os.CreateTemp(dir, "prompt-*.txt")
	if err != nil {
		return "", err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", err
	}
	if _, err := io.WriteString(file, prompt); err != nil {
		_ = file.Close()
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", err
	}
	return path, nil
}

func reserveOutput(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func configureCommand(cmd *exec.Cmd) *limitedBuffer {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Stdout = io.Discard
	limited := &limitedBuffer{limit: 4096}
	cmd.Stderr = limited
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = processWaitDelay
	return limited
}

type limitedBuffer struct {
	data  []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.data) < b.limit {
		n := b.limit - len(b.data)
		if n > len(p) {
			n = len(p)
		}
		b.data = append(b.data, p[:n]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.data)
}

func contextError(parent, run context.Context, bridge string, timeout time.Duration) *Error {
	if parent.Err() == context.Canceled || run.Err() == context.Canceled {
		return &Error{
			Kind:      KindCanceled,
			BridgeRef: bridge,
			ExitCode:  -1,
			Err:       ErrCanceled,
		}
	}
	if parent.Err() == context.DeadlineExceeded {
		return &Error{
			Kind:      KindTimeout,
			BridgeRef: bridge,
			ExitCode:  -1,
			Err:       fmt.Errorf("%w: caller deadline exceeded", ErrTimeout),
		}
	}
	if timeout > 0 {
		return &Error{
			Kind:      KindTimeout,
			BridgeRef: bridge,
			ExitCode:  -1,
			Err:       fmt.Errorf("%w after %s", ErrTimeout, timeout),
		}
	}
	return &Error{Kind: KindTimeout, BridgeRef: bridge, ExitCode: -1, Err: ErrTimeout}
}

func verifyPNG(path string) (Result, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, ErrNoOutput
		}
		return Result{}, fmt.Errorf("%w: inspect staged output: %v", ErrNoOutput, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("%w: staged output is not a regular file", ErrInvalidPNG)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return Result{}, fmt.Errorf("%w: secure staged output: %v", ErrInvalidPNG, err)
	}
	// Re-stat after the mode change so the byte bound applies to the same
	// regular file that will be decoded and hashed.
	info, err = os.Lstat(path)
	if err != nil {
		return Result{}, fmt.Errorf("%w: restat staged output: %v", ErrInvalidPNG, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("%w: staged output changed type", ErrInvalidPNG)
	}
	if info.Size() <= 0 {
		return Result{}, ErrNoOutput
	}
	if info.Size() > MaxOutputBytes {
		return Result{}, fmt.Errorf("%w: %d bytes exceeds %d", ErrImageTooLarge, info.Size(), MaxOutputBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("%w: open staged output: %v", ErrInvalidPNG, err)
	}
	defer file.Close()
	config, err := png.DecodeConfig(file)
	if err != nil {
		return Result{}, fmt.Errorf("%w: decode PNG header: %v", ErrInvalidPNG, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Result{}, fmt.Errorf("%w: PNG dimensions are empty", ErrInvalidPNG)
	}
	pixels := int64(config.Width) * int64(config.Height)
	if pixels > MaxPixels {
		return Result{}, fmt.Errorf("%w: %d pixels exceeds %d", ErrImageTooLarge, pixels, MaxPixels)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Result{}, fmt.Errorf("%w: rewind staged output: %v", ErrInvalidPNG, err)
	}
	if _, err := png.Decode(file); err != nil {
		return Result{}, fmt.Errorf("%w: decode PNG pixels: %v", ErrInvalidPNG, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Result{}, fmt.Errorf("%w: rewind staged output for checksum: %v", ErrInvalidPNG, err)
	}
	hash := sha256.New()
	bytesRead, err := io.Copy(hash, io.LimitReader(file, MaxOutputBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("%w: hash staged output: %v", ErrInvalidPNG, err)
	}
	if bytesRead != info.Size() {
		return Result{}, fmt.Errorf("%w: staged output changed during validation", ErrInvalidPNG)
	}
	if bytesRead > MaxOutputBytes {
		return Result{}, fmt.Errorf("%w: staged output grew beyond %d bytes", ErrImageTooLarge, MaxOutputBytes)
	}
	return Result{
		Path:   path,
		Bytes:  bytesRead,
		Width:  config.Width,
		Height: config.Height,
		SHA256: fmt.Sprintf("%x", hash.Sum(nil)),
	}, nil
}

func kindForOutputError(err error) ErrorKind {
	switch {
	case errors.Is(err, ErrNoOutput):
		return KindNoOutput
	case errors.Is(err, ErrImageTooLarge):
		return KindImageTooLarge
	default:
		return KindInvalidPNG
	}
}

func joinCleanup(err, cleanupErr error) error {
	if cleanupErr == nil {
		return err
	}
	if err == nil {
		return cleanupErr
	}
	return errors.Join(err, cleanupErr)
}

func removeOwnedPath(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
