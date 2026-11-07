// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func busyRetryError(captured bool) *runner.Error {
	return &runner.Error{Kind: runner.KindModelBusy, ExitCode: -1,
		NativeFailure: &runner.NativeFailureInfo{CallbackCaptured: &captured}}
}

type retryFixtureRunner struct {
	calls      int
	failures   []error
	result     runner.Completion
	delta      string
	lastPrompt string
}

func (r *retryFixtureRunner) next(prompt string, emit func(string) error) (runner.Completion, error) {
	r.lastPrompt = prompt
	r.calls++
	if emit != nil && r.delta != "" {
		if err := emit(r.delta); err != nil {
			return runner.Completion{}, err
		}
	}
	if r.calls <= len(r.failures) {
		return r.result, r.failures[r.calls-1]
	}
	return runner.Completion{Text: "OK", Model: runner.ModelLocal}, nil
}
func (r *retryFixtureRunner) Run(_ context.Context, _ runner.Model, prompt string) (string, runner.Model, error) {
	result, err := r.next(prompt, nil)
	return result.Text, result.Model, err
}
func (r *retryFixtureRunner) RunComplete(_ context.Context, _ runner.Model, prompt string) (runner.Completion, error) {
	return r.next(prompt, nil)
}
func (r *retryFixtureRunner) Stream(_ context.Context, _ runner.Model, prompt string, emit func(string) error) (runner.Completion, error) {
	return r.next(prompt, emit)
}
func (r *retryFixtureRunner) RunGeneration(_ context.Context, _ runner.Model, request runner.GenerationRequest, emit func(string) error) (runner.Completion, error) {
	return r.next(request.Prompt, emit)
}
func (r *retryFixtureRunner) RunSchema(_ context.Context, _ runner.Model, prompt string, _ *runner.Schema) (runner.Completion, error) {
	return r.next(prompt, nil)
}
func (r *retryFixtureRunner) RunCompleteWithImages(_ context.Context, _ runner.Model, prompt string, _ []string) (runner.Completion, error) {
	return r.next(prompt, nil)
}
func (r *retryFixtureRunner) StreamWithImages(_ context.Context, _ runner.Model, prompt string, _ []string, emit func(string) error) (runner.Completion, error) {
	return r.next(prompt, emit)
}
func (r *retryFixtureRunner) RunToolRound(_ context.Context, _ runner.Model, request runner.ToolRoundRequest) (runner.Completion, error) {
	return r.next(request.Prompt, nil)
}

func retryTestContext(extra int) (context.Context, *localRetry, *bytes.Buffer, *[]time.Duration) {
	var notes bytes.Buffer
	delays := []time.Duration{}
	state := &localRetry{extra: extra, attempts: 1, notes: &notes, wait: func(ctx context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return ctx.Err()
	}}
	return context.WithValue(context.Background(), localRetryKey{}, state), state, &notes, &delays
}

func TestLocalRetrySafetyRule(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		model                          runner.Model
		err                            error
		output, tool, structured, want bool
	}{
		{"busy", runner.ModelLocal, busyRetryError(false), false, false, false, true},
		{"stream", runner.ModelLocal, busyRetryError(false), true, false, false, false},
		{"tool", runner.ModelLocal, busyRetryError(false), false, true, false, false},
		{"structured", runner.ModelLocal, busyRetryError(false), false, false, true, false},
		{"callback", runner.ModelLocal, busyRetryError(true), false, false, false, false},
		{"cloud", runner.ModelCloud, busyRetryError(false), false, false, false, false},
		{"auto", runner.ModelAuto, busyRetryError(false), false, false, false, false},
		{"unknown", runner.ModelLocal, errors.New("busy"), false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayRetryLocal(tc.model, tc.err, tc.output, tc.tool, tc.structured); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestLocalRetryBusyBusySuccessAcrossGenerationPaths(t *testing.T) {
	for _, mode := range []string{"complete", "stream", "schema", "generation", "images", "image-stream", "tools"} {
		t.Run(mode, func(t *testing.T) {
			ctx, state, notes, delays := retryTestContext(3)
			fake := &retryFixtureRunner{failures: []error{busyRetryError(false), busyRetryError(false)}}
			r := retryRunnerForContext(ctx, fake).(*localRetryRunner)
			var result runner.Completion
			var err error
			switch mode {
			case "complete":
				result, err = r.RunComplete(ctx, runner.ModelLocal, "Hello")
			case "stream":
				result, err = r.Stream(ctx, runner.ModelLocal, "Hello", func(string) error { return nil })
			case "schema":
				result, err = r.RunSchema(ctx, runner.ModelLocal, "Hello", nil)
			case "generation":
				result, err = r.RunGeneration(ctx, runner.ModelLocal, runner.GenerationRequest{Prompt: "Hello"}, nil)
			case "images":
				result, err = r.RunCompleteWithImages(ctx, runner.ModelLocal, "Hello", []string{"fixture.png"})
			case "image-stream":
				result, err = r.StreamWithImages(ctx, runner.ModelLocal, "Hello", []string{"fixture.png"}, func(string) error { return nil })
			case "tools":
				result, err = r.RunToolRound(ctx, runner.ModelLocal, runner.ToolRoundRequest{Prompt: "Hello"})
			}
			if err != nil || result.Text != "OK" || fake.calls != 3 || state.attempts != 3 {
				t.Fatalf("result=%+v err=%v calls=%d attempts=%d", result, err, fake.calls, state.attempts)
			}
			if !reflect.DeepEqual(*delays, []time.Duration{time.Second, 2 * time.Second}) {
				t.Fatalf("delays=%v", *delays)
			}
			want := "hollis: Local model busy; retrying in 1 s (attempt 2 of 4)\n" +
				"hollis: Local model busy; retrying in 2 s (attempt 3 of 4)\n"
			if notes.String() != want {
				t.Fatalf("notes=%q", notes.String())
			}
		})
	}
}

func TestLocalRetryDoesNotRetryOtherFailures(t *testing.T) {
	for _, kind := range []runner.Kind{runner.KindRateLimited, runner.KindRequestDeclined, runner.KindContentRefused,
		runner.KindContextCapacity, runner.KindNativeProtocol, runner.KindToolCallInvalid, runner.KindUsage,
		runner.KindTimeout, runner.KindContextCanceled, runner.KindNativeFailed, runner.KindLocalUnavailable} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, state, notes, _ := retryTestContext(3)
			fake := &retryFixtureRunner{failures: []error{&runner.Error{Kind: kind}}}
			_, err := retryRunnerForContext(ctx, fake).(runner.CompleteRunner).RunComplete(ctx, runner.ModelLocal, "Hello")
			if err == nil || fake.calls != 1 || state.attempts != 1 || notes.Len() != 0 {
				t.Fatalf("err=%v calls=%d attempts=%d notes=%q", err, fake.calls, state.attempts, notes.String())
			}
		})
	}
}

func TestLocalRetryOutputAndToolBarriers(t *testing.T) {
	for _, mode := range []string{"stream", "generation-stream", "image-stream", "text", "structured", "callback", "tool-dispatched", "tool-unknown-capture"} {
		t.Run(mode, func(t *testing.T) {
			ctx, state, notes, _ := retryTestContext(3)
			fake := &retryFixtureRunner{failures: []error{busyRetryError(false)}}
			r := retryRunnerForContext(ctx, fake).(*localRetryRunner)
			var err error
			switch mode {
			case "stream", "generation-stream", "image-stream":
				fake.delta = "partial"
				var out bytes.Buffer
				emit := func(delta string) error { _, err := io.WriteString(&out, delta); return err }
				if mode == "stream" {
					_, err = r.Stream(ctx, runner.ModelLocal, "Hello", emit)
				} else if mode == "generation-stream" {
					_, err = r.RunGeneration(ctx, runner.ModelLocal, runner.GenerationRequest{}, emit)
				} else {
					_, err = r.StreamWithImages(ctx, runner.ModelLocal, "Hello", nil, emit)
				}
				if out.String() != "partial" {
					t.Fatalf("stream=%q", out.String())
				}
			case "text":
				fake.result.Text = "partial"
				_, err = r.RunComplete(ctx, runner.ModelLocal, "Hello")
			case "structured":
				fake.result.Structured = json.RawMessage(`{"partial":`)
				_, err = r.RunSchema(ctx, runner.ModelLocal, "Hello", nil)
			case "callback":
				fake.failures[0] = busyRetryError(true)
				_, err = r.RunToolRound(ctx, runner.ModelLocal, runner.ToolRoundRequest{})
			case "tool-unknown-capture":
				fake.failures[0] = &runner.Error{Kind: runner.KindModelBusy}
				_, err = r.RunToolRound(ctx, runner.ModelLocal, runner.ToolRoundRequest{})
			case "tool-dispatched":
				fake.result.NativeCalls = []runner.NativeToolCall{{ID: "call_1", Name: "example", Arguments: `{}`}}
				fake.failures = []error{nil, busyRetryError(false)}
				if _, firstErr := r.RunToolRound(ctx, runner.ModelLocal, runner.ToolRoundRequest{}); firstErr != nil {
					t.Fatal(firstErr)
				}
				// A dispatcher can execute the returned call here. A later
				// busy synthesis decision must not replay any model attempt.
				_, err = r.RunToolRound(ctx, runner.ModelLocal, runner.ToolRoundRequest{})
			}
			wantCalls := 1
			if mode == "tool-dispatched" {
				wantCalls = 2
			}
			if err == nil || fake.calls != wantCalls || state.attempts != 1 || notes.Len() != 0 {
				t.Fatalf("err=%v calls=%d attempts=%d notes=%q", err, fake.calls, state.attempts, notes.String())
			}
		})
	}
}

func TestLocalRetryCeilingAndBackoff(t *testing.T) {
	ctx, state, _, delays := retryTestContext(5)
	fake := &retryFixtureRunner{failures: []error{busyRetryError(false), busyRetryError(false), busyRetryError(false), busyRetryError(false), busyRetryError(false), busyRetryError(false)}}
	_, err := retryRunnerForContext(ctx, fake).(runner.CompleteRunner).RunComplete(ctx, runner.ModelLocal, "Hello")
	if err == nil || fake.calls != 6 || state.attempts != 6 ||
		!reflect.DeepEqual(*delays, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}) {
		t.Fatalf("err=%v calls=%d attempts=%d delays=%v", err, fake.calls, state.attempts, *delays)
	}
}

func TestLocalRetryCancellationDuringBackoff(t *testing.T) {
	base, state, _, _ := retryTestContext(3)
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	waiting := make(chan struct{})
	state.wait = func(ctx context.Context, delay time.Duration) error {
		close(waiting)
		return waitLocalRetry(ctx, delay)
	}
	fake := &retryFixtureRunner{failures: []error{busyRetryError(false)}}
	done := make(chan error, 1)
	go func() {
		_, err := retryRunnerForContext(ctx, fake).(runner.CompleteRunner).RunComplete(ctx, runner.ModelLocal, "Hello")
		done <- err
	}()
	<-waiting
	cancel() // signal.NotifyContext turns Ctrl-C into this same cancellation.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || fake.calls != 1 {
			t.Fatalf("err=%v calls=%d", err, fake.calls)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancellation waited for the backoff")
	}
}

func TestLocalRetrySharesDeadline(t *testing.T) {
	base, state, _, _ := retryTestContext(3)
	ctx, cancel := context.WithTimeout(base, 20*time.Millisecond)
	defer cancel()
	state.wait = waitLocalRetry
	fake := &retryFixtureRunner{failures: []error{busyRetryError(false)}}
	_, err := retryRunnerForContext(ctx, fake).(runner.CompleteRunner).RunComplete(ctx, runner.ModelLocal, "Hello")
	var failure *runner.Error
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &failure) || failure.Kind != runner.KindTimeout || fake.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, fake.calls)
	}
}

func runRetryCLI(t *testing.T, r runner.Runner, args ...string) (string, string, error) {
	t.Helper()
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
	var out, notes bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&notes)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		err = flags.helpError
	}
	return out.String(), notes.String(), err
}

func TestRetryRefusesNonLocalBeforeRunnerConstruction(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	const wantMessage = "--retry works only with --model local; Cloud, Cloud Pro, on-device and ChatGPT requests are never retried"
	for _, command := range []string{"respond", "chat"} {
		for _, model := range []string{"cloud", "cloud-pro", "on-device", "chatgpt", "auto"} {
			for _, source := range []string{"explicit", "environment", "saved"} {
				t.Run(command+"/"+model+"/"+source, func(t *testing.T) {
					if err := saveConfig(config{}); err != nil {
						t.Fatal(err)
					}
					args := []string{command, "--retry", "Hello"}
					switch source {
					case "explicit":
						args = append(args, "-m", model)
					case "environment":
						t.Setenv("HOLLIS_MODEL", model)
					case "saved":
						if err := saveConfig(config{DefaultModel: model}); err != nil {
							t.Fatal(err)
						}
					}
					constructed := 0
					cmd := NewRootCmd(func() runner.Runner { constructed++; return &retryFixtureRunner{} })
					cmd.SetOut(io.Discard)
					cmd.SetErr(io.Discard)
					cmd.SetIn(strings.NewReader(""))
					cmd.SetArgs(args)
					err := cmd.Execute()
					if ExitCode(err) != 2 || err.Error() != wantMessage || constructed != 0 {
						t.Fatalf("err=%v constructed=%d", err, constructed)
					}
				})
			}
		}
	}
}

func TestRetryFlagDefaultsAreOptIn(t *testing.T) {
	for _, command := range []string{"respond", "chat"} {
		for _, value := range []string{"absent", "bare", "0", "1", "2", "3", "4", "5"} {
			t.Run(command+"/"+value, func(t *testing.T) {
				root := NewRootCmd(func() runner.Runner { t.Fatal("parsing constructed a runner"); return nil })
				cmd, _, err := root.Find([]string{command})
				if err != nil {
					t.Fatal(err)
				}
				flag := cmd.Flags().Lookup("retry")
				if flag.DefValue != "0" || flag.NoOptDefVal != "3" || flag.Changed {
					t.Fatalf("retry registration=%+v", flag)
				}
				var args []string
				want := "0"
				switch value {
				case "absent":
				case "bare":
					args, want = []string{"--retry"}, "3"
				default:
					args, want = []string{"--retry=" + value}, value
				}
				if err := cmd.ParseFlags(args); err != nil {
					t.Fatal(err)
				}
				if flag.Value.String() != want || flag.Changed != (value != "absent") {
					t.Fatalf("retry value=%s changed=%v", flag.Value.String(), flag.Changed)
				}
			})
		}
	}
}

func TestRetryCLICountsNumericPromptDefaultsAndHelp(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	for _, source := range []string{"explicit", "environment", "saved"} {
		t.Run(source, func(t *testing.T) {
			if err := saveConfig(config{}); err != nil {
				t.Fatal(err)
			}
			args := []string{"respond", "--retry", "--json", "7"}
			switch source {
			case "explicit":
				args = append(args, "-m", "local")
			case "environment":
				t.Setenv("HOLLIS_MODEL", "local")
			case "saved":
				if err := saveConfig(config{DefaultModel: "local"}); err != nil {
					t.Fatal(err)
				}
			}
			fake := &retryFixtureRunner{}
			out, notes, err := runRetryCLI(t, fake, args...)
			if err != nil || fake.calls != 1 || fake.lastPrompt != "7" || !strings.Contains(out, `"attempts":1`) || notes != "" {
				t.Fatalf("out=%q notes=%q err=%v calls=%d prompt=%q", out, notes, err, fake.calls, fake.lastPrompt)
			}
		})
	}
	for _, command := range []string{"respond", "chat"} {
		out, _, err := runRetryCLI(t, &retryFixtureRunner{}, command, "--help")
		if err != nil || !strings.Contains(out, retryHelp) || !strings.Contains(out, "--retry int[=3]") ||
			strings.Contains(out, "(default 3)") {
			t.Fatalf("help=%q err=%v", out, err)
		}
		for _, count := range []string{"-1", "6", "99"} {
			fake := &retryFixtureRunner{}
			_, _, err := runRetryCLI(t, fake, command, "-m", "local", "--retry="+count, "Hello")
			if ExitCode(err) != 2 || fake.calls != 0 {
				t.Fatalf("count=%s err=%v calls=%d", count, err, fake.calls)
			}
		}
	}
	fake := &retryFixtureRunner{}
	out, _, err := runRetryCLI(t, fake, "respond", "-m", "local", "--retry=2", "--agent", "--select", "attempts", "Hello")
	if err != nil || !strings.Contains(out, `"attempts":1`) {
		t.Fatalf("selection=%q err=%v", out, err)
	}
	out, _, err = runRetryCLI(t, fake, "respond", "-m", "local", "--json", "Hello")
	if err != nil || strings.Contains(out, `"attempts"`) {
		t.Fatalf("default output=%q err=%v", out, err)
	}
}

func TestRetryChatContinuedModelAndJSON(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	path := filepath.Join(t.TempDir(), "chat.db")
	old := openStore
	openStore = func() (*store.Store, error) { return store.Open(path) }
	t.Cleanup(func() { openStore = old })
	fake := &retryFixtureRunner{}
	out, notes, err := runRetryCLI(t, fake, "chat", "-m", "local", "--retry=2", "--json", "Hello")
	if err != nil || notes != "" || !strings.Contains(out, `"attempts":1`) {
		t.Fatalf("out=%q notes=%q err=%v", out, notes, err)
	}
	var data struct {
		ID string `json:"conversation_id"`
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOLLIS_MODEL", "cloud")
	out, _, err = runRetryCLI(t, fake, "chat", "--continue", data.ID, "--retry", "--json", "Hello again")
	if err != nil || !strings.Contains(out, `"attempts":1`) {
		t.Fatalf("continued=%q err=%v", out, err)
	}
	st, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversationWithToolTurn("cloud", "Nonlocal", []store.Message{{Role: "user", Content: "Hello"}, {Role: "assistant", Content: "Hi"}}, store.RunRecord{})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	before := fake.calls
	_, _, err = runRetryCLI(t, fake, "chat", "--continue", conv.ID, "--retry", "Hello")
	if ExitCode(err) != 2 || fake.calls != before {
		t.Fatalf("err=%v calls=%d", err, fake.calls)
	}
}

func TestRetryCLIQuietAndZero(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	for _, quiet := range []bool{false, true} {
		fake := &retryFixtureRunner{failures: []error{busyRetryError(false)}}
		args := []string{"respond", "-m", "local", "--retry=1", "--json", "Hello"}
		if quiet {
			args = append(args, "-q")
		}
		out, notes, err := runRetryCLI(t, fake, args...)
		if err != nil || fake.calls != 2 || !strings.Contains(out, `"attempts":2`) ||
			(quiet && notes != "") || (!quiet && !strings.Contains(notes, "attempt 2 of 2")) {
			t.Fatalf("quiet=%v out=%q notes=%q err=%v calls=%d", quiet, out, notes, err, fake.calls)
		}
	}
	fake := &retryFixtureRunner{failures: []error{busyRetryError(false)}}
	_, notes, err := runRetryCLI(t, fake, "respond", "-m", "local", "--retry=0", "Hello")
	if err == nil || fake.calls != 1 || notes != "" {
		t.Fatalf("err=%v calls=%d notes=%q", err, fake.calls, notes)
	}
}

func TestRetryRefusesNonGenerationAndParsesRootNumericPrompt(t *testing.T) {
	stubConfigPath(t)
	clearDefaultEnvironment(t)
	for _, args := range [][]string{
		{"respond", "-m", "local", "--retry", "--count-tokens", "Hello"},
		{"chat", "-m", "cloud", "--retry", "--generate-image", "--image-style", "illustration", "--output", "unused.png", "Hello"},
	} {
		fake := &retryFixtureRunner{}
		_, _, err := runRetryCLI(t, fake, args...)
		if ExitCode(err) != 2 || fake.calls != 0 {
			t.Fatalf("args=%v err=%v calls=%d", args, err, fake.calls)
		}
	}
	fake := &retryFixtureRunner{}
	cmd, flags := newRootCmdWithFlags(func() runner.Runner { return fake })
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader(""))
	if err := executeResolvedCommand(cmd, flags, []string{"-m", "local", "--retry", "7"}); err != nil || fake.lastPrompt != "7" || fake.calls != 1 {
		t.Fatalf("err=%v prompt=%q calls=%d", err, fake.lastPrompt, fake.calls)
	}
	// Rendering helpers are also used before Cobra installs a context.
	data := map[string]any{"response": "OK"}
	addRetryAttempts(nil, data)
	if _, ok := data["attempts"]; ok {
		t.Fatal("nil context added retry metadata")
	}
}
