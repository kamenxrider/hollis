// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

type recordingImageGenerator struct {
	dir       string
	err       error
	delay     time.Duration
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
	requests  []imagegen.Request
}

func (g *recordingImageGenerator) Generate(ctx context.Context, request imagegen.Request) (imagegen.Result, error) {
	g.mu.Lock()
	g.calls++
	call := g.calls
	g.active++
	if g.active > g.maxActive {
		g.maxActive = g.active
	}
	g.requests = append(g.requests, request)
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.active--
		g.mu.Unlock()
	}()
	if g.delay > 0 {
		select {
		case <-ctx.Done():
			return imagegen.Result{}, ctx.Err()
		case <-time.After(g.delay):
		}
	}
	if g.err != nil {
		return imagegen.Result{}, g.err
	}
	path := filepath.Join(g.dir, "staged-"+time.Now().Format("150405.000000000")+"-"+string(rune('a'+call))+".png")
	file, err := os.Create(path)
	if err != nil {
		return imagegen.Result{}, err
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		return imagegen.Result{}, err
	}
	if err := file.Close(); err != nil {
		return imagegen.Result{}, err
	}
	return imagegen.Result{Path: path, Width: 2, Height: 3}, nil
}

func (g *recordingImageGenerator) snapshot() (calls, maxActive int, requests []imagegen.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls, g.maxActive, append([]imagegen.Request(nil), g.requests...)
}

func explicitChatImageOptions(generator imagegen.Generator, output string) chatImageOptions {
	return chatImageOptions{
		Reference:      "none",
		Bridge:         "fixture image bridge",
		ResolvedBridge: "fixture image bridge",
		Output:         output,
		Timeout:        time.Second,
		Generator:      generator,
	}
}

func TestChatImageTurnUsesTextHistoryAndPersistsHonestArtifact(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "image context")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "The bicycle is red."); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "assistant", "I will remember that text."); err != nil {
		t.Fatal(err)
	}

	generator := &recordingImageGenerator{dir: t.TempDir()}
	output := filepath.Join(t.TempDir(), "bicycle.png")
	result, err := runChatImageTurn(context.Background(), st, conv, "Draw it beside a blue wall.", explicitChatImageOptions(generator, output))
	if err != nil {
		t.Fatalf("runChatImageTurn: %v", err)
	}
	if result.Published.Path != output || result.Style != "bridge-defined" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("published image: %v", err)
	}
	calls, _, requests := generator.snapshot()
	if calls != 1 || len(requests) != 1 {
		t.Fatalf("generator calls=%d requests=%d, want 1", calls, len(requests))
	}
	wantPrompt := "USER:\nThe bicycle is red.\n\nASSISTANT:\nI will remember that text.\n\nUSER:\nDraw it beside a blue wall."
	if requests[0].Prompt != wantPrompt {
		t.Fatalf("image prompt = %q, want %q", requests[0].Prompt, wantPrompt)
	}
	for _, unwanted := range []string{"TEXT conversation only", "You are continuing an existing conversation", "Respond to the final USER message"} {
		if strings.Contains(requests[0].Prompt, unwanted) {
			t.Fatalf("image prompt contains text-chat instruction %q: %q", unwanted, requests[0].Prompt)
		}
	}

	messages, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" || !strings.HasPrefix(last.Content, "HOLLIS_IMAGE_ARTIFACT ") {
		t.Fatalf("stored artifact = %+v", last)
	}
	var artifact chatImageArtifact
	if err := json.Unmarshal([]byte(strings.TrimPrefix(last.Content, "HOLLIS_IMAGE_ARTIFACT ")), &artifact); err != nil {
		t.Fatalf("artifact JSON: %v", err)
	}
	if artifact.Path != output || artifact.SHA256 == "" || artifact.Style != "bridge-defined" || artifact.NativeWidth != 2 || artifact.NativeHeight != 3 || artifact.VisualContentObserved {
		t.Fatalf("artifact = %+v", artifact)
	}

	// A later text turn receives the artifact metadata as conversation text. It
	// receives no image bytes and the marker explicitly says pixels were not
	// observed, so continuation cannot be presented as visual inspection.
	echo := &echoRunner{}
	if _, err := runTurn(context.Background(), st, conv, "Which file did we create?", func() runner.Runner { return echo }, time.Second); err != nil {
		t.Fatalf("follow-up text turn: %v", err)
	}
	for _, want := range []string{"HOLLIS_IMAGE_ARTIFACT", output, `"visual_content_observed":false`} {
		if !strings.Contains(echo.lastPrompt, want) {
			t.Fatalf("follow-up transcript missing %q: %q", want, echo.lastPrompt)
		}
	}
}

func TestChatImageContinuationUsesCleanProvenRevisionPrompt(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "clean image continuation")
	if err != nil {
		t.Fatal(err)
	}
	original := "A small turtle beside a glass greenhouse."
	revision := "Move the turtle inside the greenhouse."
	artifact := `HOLLIS_IMAGE_ARTIFACT {"type":"hollis.image_artifact.v1","path":"/private/secret/turtle.png","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	for _, message := range []struct{ role, content string }{
		{"user", original}, {"assistant", artifact},
	} {
		if _, err := st.AppendMessage(conv.ID, message.role, message.content); err != nil {
			t.Fatal(err)
		}
	}
	generator := &recordingImageGenerator{dir: t.TempDir()}
	output := filepath.Join(t.TempDir(), "revision.png")
	if _, err := runChatImageTurn(context.Background(), st, conv, revision, explicitChatImageOptions(generator, output)); err != nil {
		t.Fatal(err)
	}
	_, _, requests := generator.snapshot()
	want := "Original image description:\n" + original + "\n\nRequested revision:\n" + revision
	if len(requests) != 1 || requests[0].Prompt != want {
		t.Fatalf("requests = %+v, want prompt %q", requests, want)
	}
	if strings.Contains(requests[0].Prompt, "/private/secret") || strings.Contains(requests[0].Prompt, "aaaaaaaa") {
		t.Fatalf("artifact metadata leaked: %q", requests[0].Prompt)
	}
}

func TestRenderChatImagePromptKeepsUnfilteredLimits(t *testing.T) {
	history := make([]store.Message, chat.MaxHistoryMessages-1)
	for i := range history {
		history[i] = store.Message{Role: "assistant", Content: `HOLLIS_IMAGE_ARTIFACT {"type":"hollis.image_artifact.v1"}`}
	}
	if _, err := renderChatImagePrompt(history, "draw"); err == nil {
		t.Fatal("history made smaller by artifact filtering bypassed message limit")
	}

	hugeArtifact := `HOLLIS_IMAGE_ARTIFACT {"type":"hollis.image_artifact.v1","path":"` + strings.Repeat("x", chat.MaxRenderedPromptBytes) + `"}`
	if _, err := renderChatImagePrompt([]store.Message{{Role: "assistant", Content: hugeArtifact}}, "draw"); err == nil {
		t.Fatal("artifact filtering bypassed rendered byte limit")
	}
}

func TestChatImageTurnCarriesUnifiedBridgeStyle(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "style context")
	if err != nil {
		t.Fatal(err)
	}
	generator := &recordingImageGenerator{dir: t.TempDir()}
	options := explicitChatImageOptions(generator, filepath.Join(t.TempDir(), "styled.png"))
	options.Style = "illustration"
	options.Bridge = ""
	options.ResolvedBridge = "Hollis Image Unified"
	options.ResolvedStyle = "illustration"
	if _, err := runChatImageTurn(context.Background(), st, conv, "Draw it", options); err != nil {
		t.Fatal(err)
	}
	_, _, requests := generator.snapshot()
	if len(requests) != 1 || requests[0].BridgeRef != "Hollis Image Unified" || requests[0].Style != "illustration" {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestFirstChatImageFailureLeavesNoConversation(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	generator := &recordingImageGenerator{
		dir: t.TempDir(),
		err: &imagegen.Error{Kind: imagegen.KindNonZeroExit, Err: errors.New("fixture failed")},
	}
	_, _, err := runFirstChatImageTurn(
		context.Background(), st, runner.ModelCloud, "draw a tree",
		explicitChatImageOptions(generator, filepath.Join(t.TempDir(), "tree.png")),
	)
	if err == nil {
		t.Fatal("image failure unexpectedly succeeded")
	}
	conversations, err := st.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 0 {
		t.Fatalf("failed image run created %d conversations", len(conversations))
	}
}

func TestChatImagePreflightRejectsCollisionBeforeGenerator(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "collision")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "existing.png")
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	generator := &recordingImageGenerator{dir: t.TempDir()}
	if _, err := runChatImageTurn(context.Background(), st, conv, "draw", explicitChatImageOptions(generator, output)); err == nil {
		t.Fatal("existing destination was accepted")
	}
	calls, _, _ := generator.snapshot()
	if calls != 0 {
		t.Fatalf("generator calls=%d, want 0", calls)
	}
	content, err := os.ReadFile(output)
	if err != nil || string(content) != "keep" {
		t.Fatalf("existing output changed: content=%q err=%v", content, err)
	}
}

func TestChatImageOutputProcessingPreservesNativeDimensions(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "processed image")
	if err != nil {
		t.Fatal(err)
	}
	generator := &recordingImageGenerator{dir: t.TempDir()}
	options := explicitChatImageOptions(generator, filepath.Join(t.TempDir(), "square.png"))
	options.OutputOptions = imagegen.OutputOptions{Size: "4x4", Fit: "pad"}
	result, err := runChatImageTurn(context.Background(), st, conv, "draw", options)
	if err != nil {
		t.Fatalf("processed image turn: %v", err)
	}
	if result.Published.Width != 4 || result.Published.Height != 4 || result.NativeWidth != 2 || result.NativeHeight != 3 {
		t.Fatalf("dimensions output=%dx%d native=%dx%d", result.Published.Width, result.Published.Height, result.NativeWidth, result.NativeHeight)
	}
	messages, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	var artifact chatImageArtifact
	if err := json.Unmarshal([]byte(strings.TrimPrefix(messages[len(messages)-1].Content, "HOLLIS_IMAGE_ARTIFACT ")), &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.OutputProcessing.Size != "4x4" || artifact.OutputProcessing.Fit != "pad" || artifact.NativeWidth != 2 || artifact.Width != 4 {
		t.Fatalf("artifact = %+v", artifact)
	}
}

func TestConcurrentChatImageContinuationsAreSerialized(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "serialize images")
	if err != nil {
		t.Fatal(err)
	}
	generator := &recordingImageGenerator{dir: t.TempDir(), delay: 25 * time.Millisecond}
	outputs := []string{filepath.Join(t.TempDir(), "one.png"), filepath.Join(t.TempDir(), "two.png")}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range outputs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := runChatImageTurn(context.Background(), st, conv, "draw", explicitChatImageOptions(generator, outputs[i]))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent image turn: %v", err)
		}
	}
	calls, maxActive, _ := generator.snapshot()
	if calls != 2 || maxActive != 1 {
		t.Fatalf("calls=%d maxActive=%d, want 2/1", calls, maxActive)
	}
	if count, err := st.MessageCount(conv.ID); err != nil || count != 4 {
		t.Fatalf("stored messages=%d err=%v, want 4", count, err)
	}
}

func TestChatGenerateImageJSONHonorsSelect(t *testing.T) {
	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "hollis.db")
	oldOpenStore := openStore
	openStore = func() (*store.Store, error) { return store.Open(dbPath) }
	t.Cleanup(func() { openStore = oldOpenStore })
	oldInteractive := interactiveStdin
	interactiveStdin = func() bool { return false }
	t.Cleanup(func() { interactiveStdin = oldInteractive })

	generator := &recordingImageGenerator{dir: t.TempDir()}
	output := filepath.Join(t.TempDir(), "selected.png")
	flags := &rootFlags{asJSON: true, selectFields: "conversation_id,path,style,checksum,visual_content_observed"}
	cmd := newChatCmdWithImageGenerator(flags, func() runner.Runner { return &recordingRunner{response: "must not run"} }, generator)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--generate-image", "--image-bridge", "fixture", "--output", output, "draw a green triangle"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("chat image command: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("JSON: %v (%q)", err, stdout.String())
	}
	for _, field := range []string{"conversation_id", "path", "style", "checksum", "visual_content_observed"} {
		if _, ok := got[field]; !ok {
			t.Fatalf("selected JSON missing %q: %+v", field, got)
		}
	}
	if len(got) != 5 || got["style"] != "bridge-defined" || got["visual_content_observed"] != false {
		t.Fatalf("selected JSON = %+v", got)
	}
	calls, _, requests := generator.snapshot()
	if calls != 1 {
		t.Fatalf("generator calls=%d, want 1", calls)
	}
	if requests[0].Timeout != imagegen.MaxTimeout {
		t.Fatalf("image timeout=%s, want %s", requests[0].Timeout, imagegen.MaxTimeout)
	}
}

func TestInteractiveImageCommandUsesExactSlashCommand(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	generator := &recordingImageGenerator{dir: t.TempDir()}
	output := filepath.Join(t.TempDir(), "interactive.png")
	var stdout, stderr bytes.Buffer
	err := runInteractiveChatWithImages(
		context.Background(), st, "cloud", "", func() runner.Runner { return &recordingRunner{response: "must not run"} }, time.Second,
		explicitChatImageOptions(generator, output), strings.NewReader("/image draw a yellow star\n"), &stdout, &stderr,
	)
	if err != nil {
		t.Fatalf("interactive image: %v", err)
	}
	if !strings.Contains(stdout.String(), "Saved PNG to "+output) || !strings.Contains(stderr.String(), "conversation_id:") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	calls, _, requests := generator.snapshot()
	if calls != 1 || !strings.Contains(requests[0].Prompt, "draw a yellow star") {
		t.Fatalf("calls=%d requests=%+v", calls, requests)
	}
	if _, isImage := parseInteractiveImagePrompt("/imageology is text"); isImage {
		t.Fatal("non-command slash prefix was parsed as /image")
	}
}
