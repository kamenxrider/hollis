// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	// maxImageBytes bounds each direct image input before it is handed to
	// another process. Keep this at the documented Hollis per-file limit;
	// generation-specific API byte and pixel limits belong to their callers.
	maxImageBytes int64 = 64 << 20
	// The compressed byte limit alone cannot bound a full pixel decode.
	maxImagePixels int64 = 64_000_000
)

// ShortcutRunner invokes `/usr/bin/shortcuts run <bridge-reference>` with piped
// stdio and a hard deadline. It satisfies runner rules 1–7; rule 8 (concurrency
// policy) lives with callers. Run is safe for concurrent use — `shortcuts run`
// is stateless and 4 parallel invocations were proven clean.
type ShortcutRunner struct {
	// ShortcutsPath is the transport binary. Tests point this at a fake.
	ShortcutsPath string
	// BridgeRefs maps models to the identifier passed to `shortcuts run`.
	// New defaults to UUID candidates; callers should replace them with the
	// positively discovered names or explicit configured references.
	BridgeRefs map[Model]string
	// Timeout applies when the caller's context carries no deadline.
	// Measured p50 is ~1s; 30s default, 120s ceiling per plan §25.
	Timeout time.Duration
}

// New returns a ShortcutRunner with measured defaults: the system shortcuts
// CLI, compiled bridge candidates, and the 30s default timeout.
func New() *ShortcutRunner {
	return &ShortcutRunner{
		ShortcutsPath: DefaultShortcutsPath,
		BridgeRefs: map[Model]string{
			ModelCloud:    BridgeUUIDCloud,
			ModelCloudPro: BridgeUUIDCloudPro,
			ModelOnDevice: BridgeUUIDOnDevice,
			ModelChatGPT:  BridgeUUIDChatGPT,
		},
		Timeout: DefaultTimeout,
	}
}

// Run implements Runner. See the package doc for the rules this enforces.
// Callers that need to explain auto behavior should use RunWithFallback.
func (r *ShortcutRunner) Run(ctx context.Context, model Model, prompt string) (string, Model, error) {
	// Rule 4: reject empty prompts before spawning. Empty input hangs
	// `shortcuts run` forever; there is no exit code to map, only a kill.
	if strings.TrimSpace(prompt) == "" {
		return "", model, &Error{
			Kind:     KindEmptyPrompt,
			ExitCode: -1,
			Err:      fmt.Errorf("%w: shortcuts run hangs forever on empty input; refusing to spawn", ErrEmptyPrompt),
		}
	}
	if model == ModelAuto {
		text, used, _, err := r.runAuto(ctx, prompt)
		return text, used, err
	}
	text, _, _, err := r.RunWithFallback(ctx, model, prompt)
	return text, model, err
}

// RunWithFallback implements FallbackRunner. ModelAuto tries cloud once and,
// only for explicitly eligible transient failures, on-device once. Explicit
// tiers never fall back.
func (r *ShortcutRunner) RunWithFallback(ctx context.Context, model Model, prompt string) (string, Model, Fallback, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", model, Fallback{}, &Error{
			Kind: KindEmptyPrompt, ExitCode: -1,
			Err: fmt.Errorf("%w: shortcuts run hangs forever on empty input; refusing to spawn", ErrEmptyPrompt),
		}
	}
	if model != ModelAuto {
		text, err := r.runTier(ctx, model, prompt)
		return text, model, Fallback{}, err
	}
	return r.runAuto(ctx, prompt)
}

// RunWithImages invokes a concrete image-capable Shortcut tier. The measured
// Shortcuts transport requires both the prompt and images to be supplied as a
// repeated --input-path list; stdin is intentionally left empty.
func (r *ShortcutRunner) RunWithImages(ctx context.Context, model Model, prompt string, imagePaths []string) (string, Model, error) {
	if err := validateImageRequestShape(model, prompt, imagePaths); err != nil {
		return "", model, err
	}
	stagedPaths, cleanup, err := stageImageInputs(imagePaths)
	if err != nil {
		return "", model, err
	}
	defer cleanup()

	text, err := r.runTierWithImages(ctx, model, prompt, stagedPaths)
	return text, model, err
}

// ValidateImageRequest checks the measured v0.2 image contract before any
// Shortcut process is spawned. PNG and JPEG are the only supported formats;
// the concrete ShortcutRunner binds and decodes the bytes from a descriptor
// that cannot resolve a symlink when it stages the request.
func ValidateImageRequest(model Model, prompt string, imagePaths []string) error {
	return validateImageRequestShape(model, prompt, imagePaths)
}

func validateImageRequestShape(model Model, prompt string, imagePaths []string) error {
	usage := func(err error) error {
		return &Error{Kind: KindUsage, ExitCode: -1, Err: err}
	}
	if strings.TrimSpace(prompt) == "" {
		return &Error{
			Kind: KindEmptyPrompt, ExitCode: -1,
			Err: fmt.Errorf("%w: image requests require a non-empty prompt argument", ErrEmptyPrompt),
		}
	}
	if !utf8.ValidString(prompt) {
		return usage(errors.New("image prompt must be valid UTF-8"))
	}
	if len(imagePaths) == 0 {
		return usage(errors.New("image request requires at least one --image path"))
	}
	switch model {
	case ModelCloud, ModelCloudPro:
		// Multiple images were measured successfully on both Apple cloud tiers.
	case ModelChatGPT:
		if len(imagePaths) != 1 {
			return usage(errors.New("chatgpt accepts exactly one --image; multiple image paths were unreliable in testing"))
		}
	case ModelOnDevice:
		return usage(errors.New("--image is unavailable with on-device: the tested Shortcut ignored the pixels"))
	case ModelAuto:
		return usage(errors.New("--image is unavailable with auto because auto can fall back to on-device"))
	default:
		return usage(fmt.Errorf("%w: %q", ErrUnknownModel, model))
	}

	for _, path := range imagePaths {
		if strings.TrimSpace(path) == "" {
			return usage(errors.New("--image path must not be empty"))
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
			return usage(fmt.Errorf("unsupported image %q: use a PNG or JPEG file", path))
		}
		info, err := os.Lstat(path)
		if err != nil {
			return usage(fmt.Errorf("open image %q: %w", path, err))
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return usage(fmt.Errorf("image %q must be a direct regular file", path))
		}
	}
	return nil
}

func imageUsageError(err error) error {
	return &Error{Kind: KindUsage, ExitCode: -1, Err: err}
}

// stageImageInputs binds each image to the bytes read from its validated
// descriptor. The child receives only paths in a private directory, so it
// cannot reopen the caller's pathname after validation.
func stageImageInputs(imagePaths []string) ([]string, func(), error) {
	stageDir, err := os.MkdirTemp("", "hollis-image-inputs-")
	if err != nil {
		return nil, nil, &Error{
			Kind: KindTransport, ExitCode: -1,
			Err: fmt.Errorf("create private image staging directory: %w", err),
		}
	}
	cleanup := func() { _ = os.RemoveAll(stageDir) }
	if err := os.Chmod(stageDir, 0o700); err != nil {
		cleanup()
		return nil, nil, &Error{
			Kind: KindTransport, ExitCode: -1,
			Err: fmt.Errorf("secure image staging directory: %w", err),
		}
	}

	staged := make([]string, 0, len(imagePaths))
	for i, imagePath := range imagePaths {
		contents, err := readValidatedImage(imagePath)
		if err != nil {
			cleanup()
			return nil, nil, imageUsageError(fmt.Errorf("open image %q: %w", imagePath, err))
		}
		ext := filepath.Ext(imagePath)
		stagePath := filepath.Join(stageDir, fmt.Sprintf("image-%d%s", i, ext))
		if err := writeStagedImage(stagePath, contents); err != nil {
			cleanup()
			return nil, nil, &Error{
				Kind: KindTransport, ExitCode: -1,
				Err: fmt.Errorf("stage image %q: %w", imagePath, err),
			}
		}
		staged = append(staged, stagePath)
	}
	return staged, cleanup, nil
}

func writeStagedImage(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closeAndRemove := func(err error) error {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return closeAndRemove(err)
	}
	n, err := file.Write(contents)
	if err != nil {
		return closeAndRemove(err)
	}
	if n != len(contents) {
		return closeAndRemove(io.ErrShortWrite)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func readValidatedImage(path string) ([]byte, error) {
	// Lstat rejects a final symlink before opening. O_NOFOLLOW protects the
	// open itself if the final component is swapped between these operations;
	// O_NONBLOCK ensures a raced FIFO cannot make validation wait for a writer.
	lstat, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect path: %w", err)
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return nil, errors.New("image path must be a direct regular file")
	}
	file, err := openDirectImage(path)
	if err != nil {
		return nil, fmt.Errorf("open direct file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("image descriptor is not a regular file")
	}
	if !os.SameFile(lstat, info) {
		_ = file.Close()
		return nil, errors.New("image path changed while opening")
	}
	if info.Size() <= 0 {
		_ = file.Close()
		return nil, errors.New("image file is empty")
	}
	if info.Size() > maxImageBytes {
		_ = file.Close()
		return nil, fmt.Errorf("image file exceeds %d byte limit", maxImageBytes)
	}

	contents, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close image: %w", closeErr)
	}
	if int64(len(contents)) > maxImageBytes {
		return nil, fmt.Errorf("image file exceeds %d byte limit", maxImageBytes)
	}
	if int64(len(contents)) != info.Size() {
		return nil, errors.New("image file changed while reading")
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(contents))
	if err != nil {
		return nil, fmt.Errorf("decode image header: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return nil, errors.New("image content must be PNG or JPEG")
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, errors.New("image dimensions are empty")
	}
	if int64(config.Width) > maxImagePixels/int64(config.Height) {
		return nil, fmt.Errorf("image exceeds %d pixel limit", maxImagePixels)
	}
	if _, _, err := image.Decode(bytes.NewReader(contents)); err != nil {
		return nil, fmt.Errorf("decode image pixels: %w", err)
	}
	return contents, nil
}

// openDirectImage asks the macOS kernel to reject symlinks in every component
// during the open itself. No separate parent check can race with that open.
func openDirectImage(path string) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// macOS provides these root-owned compatibility links. Resolve only their
	// known targets; ordinary user-created links remain prohibited.
	for _, alias := range []string{"/var", "/tmp", "/etc"} {
		if !strings.HasPrefix(abs, alias+"/") {
			continue
		}
		info, err := os.Lstat(alias)
		if err != nil {
			return nil, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		target, linkErr := os.Readlink(alias)
		if ok && stat.Uid == 0 && linkErr == nil && filepath.Clean(filepath.Join("/", target)) == "/private"+alias {
			abs = "/private" + abs
		}
		break
	}
	return os.OpenFile(abs, os.O_RDONLY|unix.O_NOFOLLOW_ANY|syscall.O_NONBLOCK, 0)
}

// runAuto tries the default tier (cloud) once, then the on-device model
// once. Empty prompts, usage errors, timeout/cancel, and process crashes are
// never retried: retrying them either produces the same local failure or
// risks another charge/usage event.
func (r *ShortcutRunner) runAuto(ctx context.Context, prompt string) (string, Model, Fallback, error) {
	text, err := r.runTier(ctx, ModelCloud, prompt)
	if err == nil {
		return text, ModelCloud, Fallback{}, nil
	}
	var re *Error
	if !errors.As(err, &re) || !FallbackEligible(re.Kind) {
		return "", ModelCloud, Fallback{}, err
	}
	primaryKind := re.Kind
	text, err = r.runTier(ctx, ModelOnDevice, prompt)
	return text, ModelOnDevice, Fallback{
		Used:   true,
		From:   ModelCloud,
		To:     ModelOnDevice,
		Reason: primaryKind,
	}, err
}

func (r *ShortcutRunner) runTier(ctx context.Context, model Model, prompt string) (string, error) {
	return r.runTierWithImages(ctx, model, prompt, nil)
}

func (r *ShortcutRunner) runTierWithImages(ctx context.Context, model Model, prompt string, imagePaths []string) (string, error) {
	if !model.Valid() || model == ModelAuto {
		return "", &Error{Kind: KindUsage, ExitCode: -1, Err: fmt.Errorf("%w: %q", ErrUnknownModel, model)}
	}
	ref, ok := r.BridgeRefs[model]
	if !ok || strings.TrimSpace(ref) == "" {
		return "", &Error{
			Kind: KindShortcutMissing, ExitCode: -1,
			Err: fmt.Errorf("bridge for %q was not positively discovered or explicitly configured", model),
		}
	}
	if strings.HasPrefix(strings.TrimSpace(ref), "-") {
		return "", &Error{Kind: KindUsage, Ref: ref, ExitCode: -1, Err: errors.New("bridge reference must not begin with '-'")}
	}

	// Rule 3: always a deadline. The caller's deadline is authoritative in
	// BOTH directions when present; r.Timeout is only the default for
	// callers that set none. Anything above the ceiling is clamped (plan
	// §25).
	//
	// This used to take the caller's deadline only when it was SHORTER than
	// r.Timeout, which silently capped every run at the 30s default: a
	// `--timeout 120s` died at 30s, MaxTimeout was unreachable dead code,
	// and the timeout error's own "hint: raise --timeout" was advice that
	// could not work.
	effective := r.Timeout
	if deadline, has := ctx.Deadline(); has {
		effective = time.Until(deadline)
	}
	if effective > MaxTimeout {
		effective = MaxTimeout
	}
	if effective <= 0 {
		// The caller's deadline already passed. Spawning anyway would hand
		// exec an expired context, Start would fail with ctx.Err(), and that
		// lands in KindTransport rather than KindTimeout — with a message
		// quoting a negative duration.
		return "", &Error{
			Kind:     KindTimeout,
			Ref:      ref,
			ExitCode: -1,
			Err:      errors.New("deadline already passed before the shortcut could be spawned"),
		}
	}
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, effective)
	defer cancel()

	// Rule 1: plain text, never the RTF default. Rule 7: reference by UUID.
	args := []string{"run", ref, "--output-type", "public.plain-text"}
	var removePrompt func()
	if len(imagePaths) > 0 {
		promptPath, cleanup, err := writeImagePromptFile(prompt)
		if err != nil {
			return "", &Error{
				Kind: KindTransport, Ref: ref, ExitCode: -1,
				Err: fmt.Errorf("prepare private image prompt: %w", err),
			}
		}
		removePrompt = cleanup
		defer removePrompt()
		args = append(args, "--input-path", promptPath)
		for _, imagePath := range imagePaths {
			args = append(args, "--input-path", imagePath)
		}
	}
	cmd := exec.CommandContext(ctx, r.ShortcutsPath, args...)
	if len(imagePaths) == 0 {
		cmd.Stdin = strings.NewReader(prompt)
	}

	// Rule 2: capture via pipes. bytes.Buffer forces exec to create an
	// os.Pipe for stdout — the child never sees a TTY, which is the one
	// form that reliably produces output.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Rule 3: deadline semantics. exec.CommandContext kills only the direct
	// child on cancel; put the child in its own process group and SIGKILL
	// the whole group so no orphaned `shortcuts run` survives the deadline.
	// SIGALRM was measured to terminate the hung child cleanly; SIGKILL on
	// the group is at least as decisive.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// WaitDelay guarantees Wait returns even if the killed child's pipe
	// goroutines would otherwise linger.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		switch parentCtx.Err() {
		case context.Canceled:
			return "", &Error{
				Kind:     KindContextCanceled,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("context canceled before %s could start: %w", r.ShortcutsPath, err),
			}
		case context.DeadlineExceeded:
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline already exceeded before %s could start: %w", r.ShortcutsPath, err),
			}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline exceeded before %s could start: %w", r.ShortcutsPath, err),
			}
		}
		return "", &Error{
			Kind:     KindTransport,
			Ref:      ref,
			ExitCode: -1,
			Err:      fmt.Errorf("spawn %s: %w", r.ShortcutsPath, err),
		}
	}

	type result struct {
		err      error
		deadline bool
	}
	done := make(chan result, 1)
	go func() {
		err := cmd.Wait()
		// Buffers are filled before Wait returns (exec drains them).
		done <- result{err: err}
	}()

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		// Rule 3: kill the process group, then reap.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		res = <-done
		res.deadline = true
	}

	out := stdout.String()
	if res.deadline {
		if errors.Is(parentCtx.Err(), context.Canceled) {
			return "", &Error{
				Kind: KindContextCanceled, Ref: ref, ExitCode: -1,
				Err: errors.New("shortcut run canceled by caller"),
			}
		}
		return "", &Error{
			Kind:     KindTimeout,
			Ref:      ref,
			ExitCode: -1,
			Err:      fmt.Errorf("shortcut run exceeded %s and was killed", effective),
		}
	}

	exitCode := exitOf(res.err)
	stderrText := stderr.String()

	// Rule 5: exit 0 + empty stdout is shortcut_no_output, never a response.
	// On a TTY this is exactly what suppressed-but-successful output looks
	// like, so it must always be treated as failure here.
	if res.err == nil && strings.TrimSpace(out) == "" {
		if isRateLimited(stderr.String()) {
			return "", &Error{
				Kind:     KindRateLimited,
				Ref:      ref,
				ExitCode: 0,
				Stderr:   stderr.String(),
				Err:      errors.New("rate limit reported"),
			}
		}
		return "", &Error{
			Kind:     KindNoOutput,
			Ref:      ref,
			ExitCode: 0,
			Err:      errors.New("exit 0 with empty stdout; treated as failure, not an empty response"),
		}
	}

	if res.err != nil {
		switch parentCtx.Err() {
		case context.Canceled:
			return "", &Error{
				Kind:     KindContextCanceled,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("context canceled while running %s: %w", r.ShortcutsPath, res.err),
			}
		case context.DeadlineExceeded:
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline exceeded while running %s: %w", r.ShortcutsPath, res.err),
			}
		}
		// The runner adds its own default/ceiling deadline when the caller has
		// none. cmd.Wait can win the select race against ctx.Done after the
		// process is killed; consult the derived context before treating that
		// SIGKILL as an ordinary process crash.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("shortcut run exceeded %s and was killed", effective),
			}
		}
		signal, signaled := processSignal(res.err)
		kind := KindTransport
		switch {
		case signaled && signal == syscall.SIGABRT:
			kind = KindSIGABRT
		case signaled:
			kind = KindSignal
		case isRateLimited(stderrText):
			kind = KindRateLimited
		case isMissingShortcut(stderrText):
			kind = KindShortcutMissing
		case exitCode == 64:
			kind = KindUsage
		}
		err := res.err
		if exitCode >= 0 {
			if se := strings.TrimSpace(stderrText); se != "" {
				err = fmt.Errorf("%s: %w", se, err)
			}
		}
		return "", &Error{
			Kind:     kind,
			Ref:      ref,
			ExitCode: exitCode,
			Signal:   signal,
			Stderr:   stderrText,
			Err:      err,
		}
	}

	// Rule 6: return stdout exactly as produced. Apple omits the trailing
	// newline; we neither expect nor add one here.
	return out, nil
}

func writeImagePromptFile(prompt string) (string, func(), error) {
	file, err := os.CreateTemp("", "hollis-image-prompt-*.txt")
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	fail := func(err error) (string, func(), error) {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := io.WriteString(file, prompt); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func (r *ShortcutRunner) ListShortcuts(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		kind := KindTransport
		switch err {
		case context.Canceled:
			kind = KindContextCanceled
		case context.DeadlineExceeded:
			kind = KindTimeout
		}
		return nil, &Error{Kind: kind, ExitCode: -1, Err: err}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, r.ShortcutsPath, "list").Output()
	if err != nil {
		kind := KindListFailure
		signal, _ := processSignal(err)
		switch cctx.Err() {
		case context.Canceled:
			kind = KindContextCanceled
		case context.DeadlineExceeded:
			kind = KindTimeout
		}
		return nil, &Error{Kind: kind, ExitCode: exitOf(err), Signal: signal, Err: err}
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

func exitOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func processSignal(err error) (syscall.Signal, bool) {
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ProcessState == nil {
		return 0, false
	}
	ws, ok := ee.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0, false
	}
	return ws.Signal(), true
}

func isRateLimited(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "too many incoming requests") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate-limit")
}

func isMissingShortcut(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "could not be found") ||
		strings.Contains(lower, "shortcut not found")
}
