// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	helperEnabledEnv = "HOLLIS_IMAGEGEN_HELPER"
	helperModeEnv    = "HOLLIS_IMAGEGEN_MODE"
	helperChildEnv   = "HOLLIS_IMAGEGEN_CHILD_PID"
)

func TestImageGenerationHelperProcess(t *testing.T) {
	if os.Getenv(helperEnabledEnv) != "1" {
		return
	}
	mode := os.Getenv(helperModeEnv)
	outputPath := argumentValue("--output-path")
	if outputPath == "" && mode != "hang" {
		os.Exit(2)
	}

	switch mode {
	case "success":
		if err := writeHelperPNG(outputPath); err != nil {
			os.Exit(3)
		}
		// Prove that binary stdout is ignored by the transport rather than
		// being returned as model text.
		_, _ = os.Stdout.Write([]byte{0x89, 'P', 'N', 'G', 0x00, 0xff})
	case "invalid":
		if err := os.WriteFile(outputPath, []byte("not a PNG"), 0o600); err != nil {
			os.Exit(4)
		}
	case "oversize":
		file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			os.Exit(5)
		}
		_, err = io.Copy(file, io.LimitReader(bytes.NewReader(bytes.Repeat([]byte{'x'}, int(MaxOutputBytes+1))), MaxOutputBytes+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			os.Exit(6)
		}
	case "nonzero":
		_, _ = os.Stderr.WriteString("synthetic bridge failure\n")
		os.Exit(17)
	case "hang":
		child := exec.Command("/bin/sh", "-c", "sleep 300")
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		if err := child.Start(); err != nil {
			os.Exit(7)
		}
		pidPath := os.Getenv(helperChildEnv)
		if pidPath != "" {
			_ = os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		}
		select {}
	default:
		os.Exit(8)
	}
	os.Exit(0)
}

func newTestTransport(t *testing.T, mode string) (*ShortcutTransport, *testCommandTrace) {
	t.Helper()
	trace := &testCommandTrace{}
	transport := New()
	transport.ShortcutsPath = "synthetic-shortcuts"
	transport.TempDir = t.TempDir()
	transport.Timeout = 2 * time.Second
	transport.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		trace.name = name
		trace.args = append([]string(nil), args...)
		helperArgs := append([]string{"-test.run=TestImageGenerationHelperProcess", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), helperEnabledEnv+"=1", helperModeEnv+"="+mode)
		if trace.childPIDPath != "" {
			cmd.Env = append(cmd.Env, helperChildEnv+"="+trace.childPIDPath)
		}
		return cmd
	}
	return transport, trace
}

type testCommandTrace struct {
	name         string
	args         []string
	childPIDPath string
}

func TestGenerateStagesAndVerifiesPNG(t *testing.T) {
	transport, trace := newTestTransport(t, "success")
	result, err := transport.Generate(context.Background(), Request{
		Prompt:    "A red bicycle beside a blue wall",
		BridgeRef: "Synthetic Image Bridge",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if trace.name == DefaultShortcutsPath {
		t.Fatalf("test attempted to invoke the real Shortcuts path")
	}
	if result.Path == "" || result.Bytes <= 0 || result.Width != 2 || result.Height != 3 || len(result.SHA256) != sha256.Size*2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := result.SHA256; got != helperPNGChecksum(t) {
		t.Fatalf("SHA256=%q, want %q", got, helperPNGChecksum(t))
	}

	stageDir := filepath.Dir(result.Path)
	dirInfo, err := os.Stat(stageDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("stage mode=%#o, want 0700", got)
	}
	outputInfo, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := outputInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("output mode=%#o, want 0600", got)
	}

	promptPath := argumentAfter(trace.args, "--input-path")
	if promptPath == "" {
		t.Fatalf("missing prompt input path in args: %#v", trace.args)
	}
	promptInfo, err := os.Stat(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := promptInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("prompt mode=%#o, want 0600", got)
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(prompt) != "A red bicycle beside a blue wall" {
		t.Fatalf("prompt file=%q", prompt)
	}
	if argumentAfter(trace.args, "--output-path") != result.Path {
		t.Fatalf("output arg=%q, result path=%q", argumentAfter(trace.args, "--output-path"), result.Path)
	}
	if got := argumentAfter(trace.args, "--output-type"); got != "public.png" {
		t.Fatalf("output type=%q, want public.png", got)
	}
	if trace.args[0] != "run" || trace.args[1] != "Synthetic Image Bridge" {
		t.Fatalf("unexpected command args: %#v", trace.args)
	}

	if err := result.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if err := result.Cleanup(); err != nil {
		t.Fatalf("second Cleanup: %v", err)
	}
	if _, err := os.Stat(stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage directory after cleanup: %v", err)
	}
}

func TestGenerateRejectsEmptyPromptAndMissingBridgeBeforeSpawn(t *testing.T) {
	transport, trace := newTestTransport(t, "success")
	for _, test := range []struct {
		name string
		req  Request
		want error
	}{
		{name: "empty", req: Request{Prompt: " \n", BridgeRef: "bridge"}, want: ErrEmptyPrompt},
		{name: "missing bridge", req: Request{Prompt: "draw", BridgeRef: "\t"}, want: ErrMissingBridge},
		{name: "flag-like bridge", req: Request{Prompt: "draw", BridgeRef: "--bad"}, want: ErrMissingBridge},
	} {
		t.Run(test.name, func(t *testing.T) {
			trace.name = ""
			trace.args = nil
			_, err := transport.Generate(context.Background(), test.req)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want errors.Is(..., %v)", err, test.want)
			}
			if trace.name != "" || len(trace.args) != 0 {
				t.Fatalf("command factory called for invalid request: name=%q args=%#v", trace.name, trace.args)
			}
		})
	}
}

func TestGenerateRejectsTimeoutAboveHardCeiling(t *testing.T) {
	transport, trace := newTestTransport(t, "success")
	_, err := transport.Generate(context.Background(), Request{
		Prompt:    "draw",
		BridgeRef: "bridge",
		Timeout:   MaxTimeout + time.Nanosecond,
	})
	if !errors.Is(err, ErrTimeoutTooLarge) {
		t.Fatalf("err=%v, want ErrTimeoutTooLarge", err)
	}
	if trace.name != "" || len(trace.args) != 0 {
		t.Fatalf("command factory called for over-ceiling timeout")
	}
}

func TestGenerateCleansFailedOutputPaths(t *testing.T) {
	for _, mode := range []string{"invalid", "oversize", "nonzero"} {
		t.Run(mode, func(t *testing.T) {
			transport, _ := newTestTransport(t, mode)
			_, err := transport.Generate(context.Background(), Request{Prompt: "draw", BridgeRef: "bridge"})
			if err == nil {
				t.Fatal("Generate succeeded for synthetic failure")
			}
			if mode == "invalid" && !errors.Is(err, ErrInvalidPNG) {
				t.Fatalf("err=%v, want invalid PNG", err)
			}
			if mode == "oversize" && !errors.Is(err, ErrImageTooLarge) {
				t.Fatalf("err=%v, want image-too-large", err)
			}
			if mode == "nonzero" && (!errors.Is(err, ErrNonZeroExit) || !strings.Contains(err.Error(), "synthetic bridge failure")) {
				t.Fatalf("err=%v, want bounded stderr and nonzero exit", err)
			}
			entries, readErr := os.ReadDir(transport.TempDir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("failed generation left staging entries: %v", entries)
			}
		})
	}
}

func TestGenerateCancellationKillsOwnedProcessGroupAndCleans(t *testing.T) {
	transport, trace := newTestTransport(t, "hang")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	trace.childPIDPath = childPIDPath
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := transport.Generate(ctx, Request{Prompt: "draw", BridgeRef: "bridge", Timeout: time.Second})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err=%v, want timeout", err)
	}
	childPID := waitForPID(t, childPIDPath)
	waitForProcessExit(t, childPID)
	entries, readErr := os.ReadDir(transport.TempDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("canceled generation left staging entries: %v", entries)
	}
}

func TestGenerateCallerCancellationIsDistinct(t *testing.T) {
	transport, trace := newTestTransport(t, "hang")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	trace.childPIDPath = childPIDPath
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	_, err := transport.Generate(ctx, Request{Prompt: "draw", BridgeRef: "bridge", Timeout: time.Second})
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err=%v, want caller cancellation", err)
	}
	waitForProcessExit(t, waitForPID(t, childPIDPath))
}

func TestResultCleanupReportsFailure(t *testing.T) {
	transport, _ := newTestTransport(t, "success")
	result, err := transport.Generate(context.Background(), Request{Prompt: "draw", BridgeRef: "bridge"})
	if err != nil {
		t.Fatal(err)
	}
	// Replace the owned file with a non-empty directory. The cleanup code must
	// report the failure and leave unrelated contents intact for diagnosis.
	if err := os.Remove(result.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(result.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(result.Path, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupErr := result.Cleanup()
	if !errors.Is(cleanupErr, ErrCleanup) {
		t.Fatalf("Cleanup err=%v, want ErrCleanup", cleanupErr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup removed content it did not own: %v", err)
	}
	_ = os.Remove(marker)
	_ = os.Remove(result.Path)
	_ = os.Remove(filepath.Dir(result.Path))
}

func writeHelperPNG(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 100), G: uint8(y * 60), B: 180, A: 255})
		}
	}
	return png.Encode(file, img)
}

func helperPNGChecksum(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 100), G: uint8(y * 60), B: 180, A: 255})
		}
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

func argumentValue(name string) string {
	return argumentAfter(os.Args, name)
}

func argumentAfter(args []string, name string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name {
			return args[index+1]
		}
	}
	return ""
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("helper never recorded child PID at %s", path)
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("synthetic child process %d survived owned process-group cancellation", pid)
}
