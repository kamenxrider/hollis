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

	"github.com/kamenxrider/hollis/internal/shortcutdiagnostic"
	"github.com/kamenxrider/hollis/internal/subproc"
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
// only for confirmed missing bridges or recognized rate limits, on-device once. Explicit
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
	// Route facts come from the table; auto, a routing policy rather than a
	// route, keeps its own arm. This is an if chain rather than a switch so
	// that no production code dispatches on a model value — routespec_guard
	// enforces that, and the table is the only place such a decision belongs.
	spec, ok := routeSpecs[model]
	if model == ModelAuto {
		return usage(errors.New("--image is unavailable with auto because auto can fall back to on-device"))
	}
	if !ok || spec.Transport != TransportShortcuts {
		return usage(fmt.Errorf("%w: %q", ErrUnknownModel, model))
	}
	if !spec.Capabilities.Images {
		return usage(errors.New(spec.ImageRestriction))
	}
	if spec.Capabilities.MaxImages == 1 && len(imagePaths) != 1 {
		return usage(fmt.Errorf("%s accepts exactly one --image; multiple image paths were unreliable in testing", model))
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
	// Native stdin can classify code-shaped text as a different input object.
	// Always stage the exact rendered text, including for text-only requests.
	// Keep the prompt first when co-delivering images. Never retry via stdin.
	promptPath, cleanup, err := writePromptFile(prompt)
	if err != nil {
		return "", &Error{
			Kind: KindTransport, Ref: ref, ExitCode: -1,
			Err: fmt.Errorf("prepare private prompt: %w", err),
		}
	}
	defer cleanup()
	args = append(args, "--input-path", promptPath)
	for _, imagePath := range imagePaths {
		args = append(args, "--input-path", imagePath)
	}
	// Rule 2: capture through pipes this package owns. The capped writers
	// keep draining (the child's writes never block on the capture) while
	// bounding parent memory: a flooding child grows the capture only to
	// the cap. Truncation is recorded and surfaced — a truncated stdout is
	// never returned as a complete response.
	//
	// Rule 3: the deadline owns the pipes. The child runs in its own process
	// group, the group is SIGKILLed when the deadline expires, and the
	// capture read ends are closed at that same moment, so a detached
	// descendant holding the inherited pipe cannot extend the budget past
	// the deadline. A leader that exits successfully is not a completed
	// response while the deadline has expired; the shared lifecycle checks
	// the context after Wait. SIGALRM was measured to terminate the hung
	// child cleanly; SIGKILL on the group is at least as decisive.
	stdout, stderr := &cappedBuffer{}, &cappedStderr{}
	res, runErr := subproc.Run(ctx, subproc.Spec{
		Name:       r.ShortcutsPath,
		Args:       args,
		StdoutSink: stdout,
		StderrSink: stderr,
	})

	if !res.Started {
		startErr := res.StartErr
		if startErr == nil {
			startErr = runErr
		}
		switch parentCtx.Err() {
		case context.Canceled:
			return "", &Error{
				Kind:     KindContextCanceled,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("context canceled before %s could start: %w", r.ShortcutsPath, startErr),
			}
		case context.DeadlineExceeded:
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline already exceeded before %s could start: %w", r.ShortcutsPath, startErr),
			}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline exceeded before %s could start: %w", r.ShortcutsPath, startErr),
			}
		}
		return "", &Error{
			Kind:     KindTransport,
			Ref:      ref,
			ExitCode: -1,
			Err:      fmt.Errorf("spawn %s: %w", r.ShortcutsPath, startErr),
		}
	}

	out := stdout.String()
	if runErr != nil {
		// The governing context ended or the capture failed. Output from
		// such a run is never presented as a response.
		//
		// Expired means the deadline (caller's or the runner's own) ended the
		// run, so the killed child's status adds nothing actionable: these
		// keep the wording they had when the kill was observed directly.
		if res.Expired {
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
		// The child finished on its own while the caller's context ended.
		waitErr := res.WaitErr
		if waitErr == nil {
			waitErr = runErr
		}
		switch parentCtx.Err() {
		case context.Canceled:
			return "", &Error{
				Kind:     KindContextCanceled,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("context canceled while running %s: %w", r.ShortcutsPath, waitErr),
			}
		case context.DeadlineExceeded:
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline exceeded while running %s: %w", r.ShortcutsPath, waitErr),
			}
		}
		// The runner adds its own default/ceiling deadline when the caller
		// has none, so the derived deadline can expire before the parent's.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("shortcut run exceeded %s and was killed", effective),
			}
		}
		if ctx.Err() != nil {
			return "", &Error{
				Kind: KindContextCanceled, Ref: ref, ExitCode: -1,
				Err: errors.New("shortcut run canceled by caller"),
			}
		}
		return "", &Error{
			Kind:     KindTransport,
			Ref:      ref,
			ExitCode: -1,
			Err:      fmt.Errorf("read %s output: %w", r.ShortcutsPath, runErr),
		}
	}

	exitCode := exitOf(res.WaitErr)
	stderrText := stderr.String()

	// Rule 5: exit 0 + empty stdout is shortcut_no_output, never a response.
	// On a TTY this is exactly what suppressed-but-successful output looks
	// like, so it must always be treated as failure here.
	if res.WaitErr == nil && strings.TrimSpace(out) == "" {
		if isRateLimited(stderrText) {
			message := "rate limit reported"
			if stderr.truncated {
				message += " [stderr truncated]"
			}
			return "", &Error{
				Kind:     KindRateLimited,
				Ref:      ref,
				ExitCode: 0,
				Stderr:   stderrText,
				Err:      errors.New(message),
			}
		}
		message := "exit 0 with empty stdout; treated as failure, not an empty response"
		if stderr.truncated {
			message += " [stderr truncated]"
		}
		return "", &Error{
			Kind:     KindNoOutput,
			Ref:      ref,
			ExitCode: 0,
			Stderr:   stderrText,
			Err:      errors.New(message),
		}
	}

	if res.WaitErr != nil {
		switch parentCtx.Err() {
		case context.Canceled:
			return "", &Error{
				Kind:     KindContextCanceled,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("context canceled while running %s: %w", r.ShortcutsPath, res.WaitErr),
			}
		case context.DeadlineExceeded:
			return "", &Error{
				Kind:     KindTimeout,
				Ref:      ref,
				ExitCode: -1,
				Err:      fmt.Errorf("deadline exceeded while running %s: %w", r.ShortcutsPath, res.WaitErr),
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
		signal, signaled := processSignal(res.WaitErr)
		kind := KindShortcutFailed
		switch {
		case signaled && signal == syscall.SIGABRT:
			kind = KindSIGABRT
		case signaled:
			kind = KindSignal
		case exitCode < 0:
			kind = KindTransport
		case exitCode != 64 && shortcutdiagnostic.RequestDeclined(stderrText):
			kind = KindRequestDeclined
		case isRateLimited(stderrText):
			kind = KindRateLimited
		case isMissingShortcut(stderrText):
			kind = KindShortcutMissing
		case exitCode == 64:
			kind = KindUsage
		}
		err := res.WaitErr
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
	// newline; we neither expect nor add one here. "As produced" means
	// complete: a capture that hit the cap never passes as a response —
	// partial output presented as whole would be the same fabrication class
	// as the list path's post-deadline success.
	if stdout.truncated {
		return "", &Error{
			Kind:     KindTransport,
			Ref:      ref,
			ExitCode: 0,
			Stderr:   stderrText,
			Err: fmt.Errorf("shortcut output exceeded the %d MiB capture cap; the response cannot be delivered faithfully",
				runCaptureCap>>20),
		}
	}
	return out, nil
}

// runCaptureCap bounds each run-path stream at 1 MiB. Oversized stdout is
// rejected, not delivered as a partial response. Stderr retains its head and
// tail so late diagnostics can still classify the failure.
const runCaptureCap = 1 << 20

// cappedBuffer keeps a bounded stdout prefix and keeps draining afterwards.
// Truncation is recorded so callers never pass partial output off as complete.
type cappedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := runCaptureCap - int64(b.buf.Len()); room <= 0 {
		b.truncated = true
		return len(p), nil
	} else if int64(len(p)) > room {
		b.buf.Write(p[:room])
		b.truncated = true
	} else {
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }

// cappedStderr keeps equal-sized head and tail excerpts. The tail is a ring
// so a noisy child does not force a full-buffer copy on every write.
type cappedStderr struct {
	head      []byte
	tail      []byte
	tailPos   int
	truncated bool
}

func (b *cappedStderr) Write(p []byte) (int, error) {
	n := len(p)
	const half = runCaptureCap / 2
	if room := half - len(b.head); room > 0 {
		keep := min(room, len(p))
		b.head = append(b.head, p[:keep]...)
		p = p[keep:]
	}
	if room := half - len(b.tail); room > 0 {
		keep := min(room, len(p))
		b.tail = append(b.tail, p[:keep]...)
		p = p[keep:]
	}
	if len(p) > 0 {
		b.truncated = true
		if len(p) >= half {
			copy(b.tail, p[len(p)-half:])
			b.tailPos = 0
		} else {
			written := copy(b.tail[b.tailPos:], p)
			copy(b.tail, p[written:])
			b.tailPos = (b.tailPos + len(p)) % half
		}
	}
	return n, nil
}

func (b *cappedStderr) String() string {
	var out strings.Builder
	out.Grow(len(b.head) + len(b.tail) + len("\n[stderr truncated]\n"))
	out.Write(b.head)
	if b.truncated {
		out.WriteString("\n[stderr truncated]\n")
	}
	out.Write(b.tail[b.tailPos:])
	out.Write(b.tail[:b.tailPos])
	return out.String()
}

// writePromptFile stages bytes without normalization. Deferred removal covers
// normal returns, errors and handled cancellation; SIGKILL or a host crash can
// leave a mode-0600 temporary file. Removal does not promise secure erasure.
func writePromptFile(prompt string) (string, func(), error) {
	file, err := os.CreateTemp("", "hollis-prompt-*.txt")
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
	n, err := io.WriteString(file, prompt)
	if err != nil {
		return fail(err)
	}
	if n != len(prompt) {
		return fail(io.ErrShortWrite)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// listDeadline bounds a single `shortcuts list`. The approved user-facing
// deadline message derives its number from this constant — never hardcode
// the duration twice, so the number in the message cannot drift from the
// number enforced. It matches the bounded shell path's LIST_DEADLINE=10.
const listDeadline = 10 * time.Second

// listCaptureCap bounds the listing capture. Ten seconds of a flooding child
// would otherwise grow the parent heap without limit; a truncated listing is
// rejected rather than turned into names.
const listCaptureCap = 1 << 20

// listDeadlineError classifies an expired list deadline. The message is the
// approved fixed sentence, byte-identical to the bounded shell path's: it
// claims only what is known — the listing was stopped, and no bridge is
// proven missing. ListTimeout keeps the CLI from appending the model-centric
// timeout hint.
func listDeadlineError(budget time.Duration) *Error {
	deadlineText := "before the command started"
	if budget > 0 {
		// Round only the display value, not the enforced deadline. The
		// normal 10s bound remains exactly "10 seconds"; shorter caller
		// deadlines are reported using their actual (rounded) budget.
		budget = budget.Round(time.Millisecond)
		if budget <= 0 {
			budget = time.Millisecond
		}
		if budget%time.Second == 0 {
			deadlineText = fmt.Sprintf("%d seconds", int64(budget/time.Second))
		} else {
			deadlineText = budget.String()
		}
		return &Error{
			Kind:        KindTimeout,
			ExitCode:    -1,
			ListTimeout: true,
			Err: fmt.Errorf("Listing Shortcuts did not complete within %s and was stopped; no bridge is proven missing. The underlying cause is unconfirmed.",
				deadlineText),
		}
	}
	return &Error{
		Kind:        KindTimeout,
		ExitCode:    -1,
		ListTimeout: true,
		Err: fmt.Errorf("Listing Shortcuts deadline expired %s; no bridge is proven missing. The underlying cause is unconfirmed.",
			deadlineText),
	}
}

func (r *ShortcutRunner) ListShortcuts(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, listDeadlineError(0)
		}
		return nil, &Error{Kind: KindContextCanceled, ExitCode: -1, Err: err}
	}
	budget := listDeadline
	if callerDeadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(callerDeadline)
		if remaining <= 0 {
			return nil, listDeadlineError(0)
		}
		if remaining < budget {
			budget = remaining
		}
	}
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// The shared lifecycle owns both pipes so their lifetime ends at the
	// context deadline, not a fresh WaitDelay interval after leader exit.
	// Closing the readers also bounds a detached descendant that cannot be
	// killed with this group. Stderr keeps a bounded prefix (32 KiB) while
	// continuing to drain: ExitError normally carries that excerpt, and the
	// explicit lifecycle retains it without letting stderr grow the heap.
	res, runErr := subproc.Run(cctx, subproc.Spec{
		Name:      r.ShortcutsPath,
		Args:      []string{"list"},
		StdoutCap: listCaptureCap,
		StderrCap: 32 << 10,
	})
	if !res.Started {
		startErr := res.StartErr
		if startErr == nil {
			startErr = runErr
		}
		switch {
		case errors.Is(cctx.Err(), context.DeadlineExceeded):
			return nil, listDeadlineError(budget)
		case errors.Is(cctx.Err(), context.Canceled):
			return nil, &Error{Kind: KindContextCanceled, ExitCode: -1, Err: startErr}
		default:
			return nil, &Error{Kind: KindListFailure, ExitCode: exitOf(startErr), Err: startErr}
		}
	}
	if exitErr, ok := res.WaitErr.(*exec.ExitError); ok {
		exitErr.Stderr = append([]byte(nil), res.Stderr...)
	}
	readErr := res.ReadErr
	if readErr == nil && runErr != nil && cctx.Err() == nil {
		readErr = runErr
	}
	// A listing that outgrew the capture is not a listing. Truncated bytes
	// would otherwise turn into invented names; an expired deadline still owns
	// the classification, and the message never claims a bridge is missing.
	if res.StdoutTruncated && cctx.Err() == nil {
		return nil, &Error{
			Kind:     KindListFailure,
			ExitCode: 0,
			Err: fmt.Errorf("Listing Shortcuts produced more than the %d MiB capture cap; no bridge is proven missing.",
				listCaptureCap>>20),
		}
	}
	return listShortcutsResult(cctx, budget, res.WaitErr, readErr, string(res.Stdout))
}

func listShortcutsResult(ctx context.Context, budget time.Duration, waitErr, readErr error, out string) ([]string, error) {
	ctxErr := ctx.Err()
	if waitErr != nil {
		signal, _ := processSignal(waitErr)
		switch {
		case errors.Is(ctxErr, context.DeadlineExceeded):
			return nil, listDeadlineError(budget)
		case errors.Is(ctxErr, context.Canceled):
			return nil, &Error{Kind: KindContextCanceled, ExitCode: exitOf(waitErr), Signal: signal, Err: waitErr}
		}
		return nil, &Error{Kind: KindListFailure, ExitCode: exitOf(waitErr), Signal: signal, Err: waitErr}
	}
	// A successful leader exit is not a completed listing while an inherited
	// pipe stays open. Never turn captured bytes into names after expiry.
	if ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, listDeadlineError(budget)
		}
		return nil, &Error{Kind: KindContextCanceled, ExitCode: -1, Err: ctxErr}
	}
	if readErr != nil {
		return nil, &Error{Kind: KindListFailure, ExitCode: -1, Err: readErr}
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
