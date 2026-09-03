// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

// echoRunner returns the transcript it received so tests can assert exactly
// what was sent to the transport, and stores the last prompt.
type echoRunner struct {
	lastPrompt string
}

func (r *echoRunner) Run(_ context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	r.lastPrompt = prompt
	return prompt, m, nil
}

type recordingRunner struct {
	response  string
	used      runner.Model
	err       error
	calls     int
	requested []runner.Model
	prompts   []string
}

func (r *recordingRunner) Run(_ context.Context, m runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	r.requested = append(r.requested, m)
	r.prompts = append(r.prompts, prompt)
	used := r.used
	if used == "" {
		used = m
	}
	if r.err != nil {
		return "", used, r.err
	}
	return r.response, used, nil
}

func openTempStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hollis.db"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunTurnStoresReplayHistory(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "t1")
	if err != nil {
		t.Fatal(err)
	}

	r := &echoRunner{}
	newRunner := func() runner.Runner { return r }

	// Turn 1: no history.
	if _, err := runTurn(context.Background(), st, conv, "Remember the codeword ORBIT-9", func() runner.Runner { return r }, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.lastPrompt, "ASSISTANT:") {
		t.Fatalf("first turn should have no ASSISTANT history: %q", r.lastPrompt)
	}

	// Turn 2: the replay must contain the prior turn's user message.
	if _, err := runTurn(context.Background(), st, conv, "What was the codeword?", newRunner, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.lastPrompt, "ORBIT-9") {
		t.Fatalf("second turn transcript missing prior turn: %q", r.lastPrompt)
	}

	msgs, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 { // 2 user + 2 assistant
		t.Fatalf("stored messages = %d, want 4", len(msgs))
	}
	if msgs[3].Role != "assistant" {
		t.Fatalf("last role = %q, want assistant", msgs[3].Role)
	}
}

func TestTruncateTitle(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := truncateTitle("first line\nsecond line")
	if strings.Contains(got, "\n") {
		t.Fatalf("title must be single line: %q", got)
	}
	if got := truncateTitle(long); len([]rune(got)) > 61 {
		t.Fatalf("title too long: %d runes", len([]rune(got)))
	}
}

func TestRunFirstTurnFailureLeavesNoConversation(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	r := &recordingRunner{err: &runner.Error{
		Kind: runner.KindTransport, ExitCode: 1, Stderr: "private transport details", Err: errors.New("transport failed"),
	}}
	_, _, err := runFirstTurn(context.Background(), st, runner.ModelCloud, "private prompt", func() runner.Runner { return r }, 0)
	if err == nil {
		t.Fatal("runFirstTurn unexpectedly succeeded")
	}
	if r.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", r.calls)
	}
	convs, err := st.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 0 {
		t.Fatalf("failed first turn left %d conversations, want 0", len(convs))
	}
}

func TestRunTurnRejectsOversizedInputBeforeRunner(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "limits")
	if err != nil {
		t.Fatal(err)
	}
	r := &recordingRunner{response: "should not run"}
	if _, err := runTurn(context.Background(), st, conv, strings.Repeat("x", chat.MaxRenderedPromptBytes), func() runner.Runner { return r }, 0); err == nil {
		t.Fatal("oversized rendered prompt was accepted")
	}
	if r.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", r.calls)
	}
	for i := 0; i < chat.MaxHistoryMessages-2; i++ {
		if _, err := st.AppendMessage(conv.ID, "user", "history"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runTurn(context.Background(), st, conv, "small", func() runner.Runner { return r }, 0); err != nil {
		t.Fatalf("turn reaching exact stored-history limit failed: %v", err)
	}
	if _, err := runTurn(context.Background(), st, conv, "one too many", func() runner.Runner { return r }, 0); err == nil {
		t.Fatal("turn exceeding stored-history limit was accepted")
	}
	if r.calls != 1 {
		t.Fatalf("runner calls after history check = %d, want 1", r.calls)
	}
}

func TestInteractiveChatRejectsOversizedLineBeforeRunner(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	r := &recordingRunner{response: "must not run"}
	err := runInteractiveChat(
		context.Background(), st, "cloud", "", func() runner.Runner { return r }, 0,
		strings.NewReader(strings.Repeat("x", chat.MaxRenderedPromptBytes+1)), io.Discard, io.Discard,
	)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want usage 2", err, ExitCode(err))
	}
	if r.calls != 0 {
		t.Fatalf("runner calls=%d, want 0", r.calls)
	}
	conversations, err := st.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 0 {
		t.Fatalf("oversized line created %d conversations", len(conversations))
	}
}

type atomicCountingRunner struct {
	calls atomic.Int32
}

func (r *atomicCountingRunner) Run(_ context.Context, m runner.Model, _ string) (string, runner.Model, error) {
	r.calls.Add(1)
	return "answer", m, nil
}

func TestConcurrentContinuationCannotExceedStoredHistoryLimit(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "history boundary")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < chat.MaxHistoryMessages-2; i++ {
		if _, err := st.AppendMessage(conv.ID, "user", "history"); err != nil {
			t.Fatal(err)
		}
	}

	r := &atomicCountingRunner{}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runTurnResult(context.Background(), st, conv, "final", func() runner.Runner { return r }, 0)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var successes, failures int
	for err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 || r.calls.Load() != 1 {
		t.Fatalf("successes=%d failures=%d runner calls=%d, want 1/1/1", successes, failures, r.calls.Load())
	}
	if count, err := st.MessageCount(conv.ID); err != nil || count != chat.MaxHistoryMessages {
		t.Fatalf("stored messages=%d err=%v, want %d", count, err, chat.MaxHistoryMessages)
	}
}

func TestContinuationLockWaitHonorsTurnTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hollis.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	conv, err := first.CreateConversation("cloud", "locked")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := first.LockContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	r := &recordingRunner{response: "must not run"}
	_, err = runTurnResult(context.Background(), second, conv, "waiting", func() runner.Runner { return r }, 25*time.Millisecond)
	if err == nil || ExitCode(err) != 7 {
		t.Fatalf("err=%v exit=%d, want timeout 7", err, ExitCode(err))
	}
	if r.calls != 0 {
		t.Fatalf("runner calls=%d, want 0", r.calls)
	}
}

func TestConcurrentContinuationCommitsCompleteAdjacentTurns(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "concurrent continuation")
	if err != nil {
		t.Fatal(err)
	}

	const turns = 8
	errs := make(chan error, turns)
	var wg sync.WaitGroup
	for i := 0; i < turns; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &recordingRunner{response: fmt.Sprintf("answer-%d", i)}
			_, err := runTurnResult(context.Background(), st, conv, fmt.Sprintf("question-%d", i), func() runner.Runner { return r }, 0)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != turns*2 {
		t.Fatalf("messages=%d want=%d", len(msgs), turns*2)
	}
	for i := 0; i < len(msgs); i += 2 {
		if msgs[i].Seq != int64(i) || msgs[i+1].Seq != int64(i+1) || msgs[i].Role != "user" || msgs[i+1].Role != "assistant" {
			t.Fatalf("turn at seq %d is partial or interleaved: %+v %+v", i, msgs[i], msgs[i+1])
		}
		questionID := strings.TrimPrefix(msgs[i].Content, "question-")
		answerID := strings.TrimPrefix(msgs[i+1].Content, "answer-")
		if questionID != answerID {
			t.Fatalf("turn mismatch at seq %d: %q / %q", i, msgs[i].Content, msgs[i+1].Content)
		}
	}
}

func TestChatContinueUsesStoredModelAndReportsJSON(t *testing.T) {
	oldInteractive := interactiveStdin
	interactiveStdin = func() bool { return false }
	t.Cleanup(func() { interactiveStdin = oldInteractive })

	dir := t.TempDir()
	path := filepath.Join(dir, "hollis.db")
	seed, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := seed.CreateConversation("cloud-pro", "stored model")
	if err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return store.Open(path) }
	t.Cleanup(func() { openStore = oldOpen })
	r := &recordingRunner{response: "answer"}
	cmd := NewRootCmd(func() runner.Runner { return r })
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"chat", "--continue", conv.ID, "--json", "hello"})
	data, execErr := captureStdout(t, cmd, out)
	if execErr != nil {
		t.Fatalf("chat --continue: %v", execErr)
	}
	if len(r.requested) != 1 || r.requested[0] != runner.ModelCloudPro {
		t.Fatalf("requested models = %v, want [cloud-pro]", r.requested)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("chat JSON: %v (%q)", err, data)
	}
	for _, field := range []string{"conversation_id", "model_requested", "model_used", "response"} {
		if _, ok := got[field]; !ok {
			t.Fatalf("chat JSON missing %q: %v", field, got)
		}
	}
	if got["conversation_id"] != conv.ID || got["model_requested"] != "cloud-pro" || got["model_used"] != "cloud-pro" || got["response"] != "answer" {
		t.Fatalf("chat JSON = %+v", got)
	}
}

func TestChatContinueRejectsExplicitModel(t *testing.T) {
	for _, extra := range [][]string{
		{"--model", "cloud"},
		{"model", "cloud"},
	} {
		cmd := NewRootCmd(func() runner.Runner { return &recordingRunner{} })
		args := []string{"chat", "--continue", "missing"}
		args = append(args, extra...)
		args = append(args, "hello")
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.Execute()
		if ExitCode(err) != 2 {
			t.Fatalf("args %v: exit code = %d, want 2 (%v)", args, ExitCode(err), err)
		}
	}
}

func TestChatHumanResponseUsesCobraWriters(t *testing.T) {
	oldInteractive := interactiveStdin
	interactiveStdin = func() bool { return false }
	t.Cleanup(func() { interactiveStdin = oldInteractive })
	dir := t.TempDir()
	path := filepath.Join(dir, "hollis.db")
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return store.Open(path) }
	t.Cleanup(func() { openStore = oldOpen })
	r := &recordingRunner{response: "answer"}
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs([]string{"chat", "--model", "cloud", "hello"})
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if out.String() != "answer\n" {
		t.Fatalf("stdout = %q, want only response", out.String())
	}
	if !strings.Contains(errOut.String(), "conversation_id:") {
		t.Fatalf("stderr = %q, want conversation id", errOut.String())
	}
	if strings.Contains(errOut.String(), "answer") {
		t.Fatalf("stderr leaked response: %q", errOut.String())
	}
}

func TestChatJSONReportsFallbackReason(t *testing.T) {
	stubConfigPath(t)
	oldInteractive := interactiveStdin
	interactiveStdin = func() bool { return false }
	t.Cleanup(func() { interactiveStdin = oldInteractive })
	st := openTempStore(t)
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return st, nil }
	t.Cleanup(func() { openStore = oldOpen })
	r := &recordingRunner{response: "local answer", used: runner.ModelOnDevice}
	cmd := NewRootCmd(func() runner.Runner { return r })
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"chat", "--json", "hello"})
	data, execErr := captureStdout(t, cmd, out)
	if execErr != nil {
		t.Fatalf("chat: %v", execErr)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("chat JSON: %v (%q)", err, data)
	}
	if got["model_requested"] != "auto" || got["model_used"] != "on-device" || got["fallback_reason"] == nil {
		t.Fatalf("fallback JSON = %+v", got)
	}
}

func TestChatsRenameAndDeleteJSONAreStructured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hollis.db")
	seed, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := seed.CreateConversation("cloud", "before")
	if err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	oldOpen := openStore
	openStore = func() (*store.Store, error) { return store.Open(path) }
	t.Cleanup(func() { openStore = oldOpen })

	renameOut := &bytes.Buffer{}
	rename := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	rename.SetArgs([]string{"chats", "rename", conv.ID, "after", "title", "--json"})
	rename.SetOut(renameOut)
	if err := rename.Execute(); err != nil {
		t.Fatalf("chats rename: %v", err)
	}
	var renamed map[string]any
	if err := json.Unmarshal(renameOut.Bytes(), &renamed); err != nil {
		t.Fatalf("rename JSON: %v (%q)", err, renameOut.String())
	}
	if renamed["ok"] != true || renamed["conversation_id"] != conv.ID || renamed["title"] != "after title" {
		t.Fatalf("rename JSON = %+v", renamed)
	}

	deleteOut := &bytes.Buffer{}
	remove := NewRootCmd(func() runner.Runner { return &fakeRunner{} })
	remove.SetArgs([]string{"chats", "delete", conv.ID, "--yes", "--json"})
	remove.SetOut(deleteOut)
	if err := remove.Execute(); err != nil {
		t.Fatalf("chats delete: %v", err)
	}
	var deleted map[string]any
	if err := json.Unmarshal(deleteOut.Bytes(), &deleted); err != nil {
		t.Fatalf("delete JSON: %v (%q)", err, deleteOut.String())
	}
	if deleted["ok"] != true || deleted["conversation_id"] != conv.ID || deleted["deleted"] != true {
		t.Fatalf("delete JSON = %+v", deleted)
	}
	check, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	if _, err := check.GetConversation(conv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted conversation lookup = %v, want ErrNotFound", err)
	}
}

func captureStdout(t *testing.T, cmd interface{ Execute() error }, out *bytes.Buffer) ([]byte, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	execErr := cmd.Execute()
	os.Stdout = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		data = append([]byte(nil), out.Bytes()...)
	}
	return data, execErr
}
