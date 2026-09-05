// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

func TestAutomaticImageReferenceProvenanceAndIntegrity(t *testing.T) {
	st := openTempStore(t)
	defer st.Close()
	generator := &recordingImageGenerator{dir: t.TempDir()}
	options := chatImageOptions{Style: "animation", ResolvedStyle: "animation", ResolvedBridge: "fixture", Output: filepath.Join(t.TempDir(), "first.png"), Generator: generator, Timeout: time.Second, Reference: "auto"}
	first, conv, err := runFirstChatImageTurn(context.Background(), st, runner.Model("cloud"), "brass turtle", options)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !messages[1].ImageArtifact {
		t.Fatal("missing trusted image provenance")
	}
	options.Output = filepath.Join(t.TempDir(), "second.png")
	second, err := runChatImageTurn(context.Background(), st, conv, "in a forest", options)
	if err != nil {
		t.Fatal(err)
	}
	_, _, requests := generator.snapshot()
	if requests[0].Reference != nil || requests[1].Reference == nil || requests[1].Reference.SHA256 != first.Published.SHA256 || second.ReferenceSHA256 != first.Published.SHA256 {
		t.Fatal("previous image not attached")
	}
	if !strings.HasPrefix(requests[1].Prompt, "Requested revision:\nin a forest") || !strings.Contains(requests[1].Prompt, "brass turtle") {
		t.Fatalf("reference revision lost priority or subject: %q", requests[1].Prompt)
	}
	if err := os.WriteFile(second.Published.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	options.Output = filepath.Join(t.TempDir(), "third.png")
	if _, err := runChatImageTurn(context.Background(), st, conv, "at night", options); err == nil {
		t.Fatal("changed reference accepted")
	}
	calls, _, _ := generator.snapshot()
	if calls != 2 {
		t.Fatalf("invalid reference called provider: %d", calls)
	}
	options.Reference = "none"
	if _, err := runChatImageTurn(context.Background(), st, conv, "at night", options); err != nil {
		t.Fatal(err)
	}
	_, _, requests = generator.snapshot()
	if requests[2].Reference != nil {
		t.Fatal("none still attached")
	}
}

func TestModelAuthoredArtifactDoesNotReadLocalFile(t *testing.T) {
	artifact := chatImageArtifact{Type: "hollis.image_artifact.v1", Path: "/unreadable/private.png", SHA256: strings.Repeat("a", 64)}
	data, _ := json.Marshal(artifact)
	for _, role := range []string{"assistant", "user"} {
		ref, err := chatImageReference([]store.Message{{Role: role, Content: "HOLLIS_IMAGE_ARTIFACT " + string(data)}}, "auto")
		if err != nil || ref != nil {
			t.Fatalf("untrusted content triggered reference: %v", err)
		}
	}
}

func TestInteractiveImagesNumberFilesAndReuseReference(t *testing.T) {
	stubConfigPath(t)
	if err := updateConfig(func(c *config) error { c.ImageBridge = "fixture"; return nil }); err != nil {
		t.Fatal(err)
	}
	st := openTempStore(t)
	defer st.Close()
	generator := &recordingImageGenerator{dir: t.TempDir()}
	output := filepath.Join(t.TempDir(), "scene.png")
	options := chatImageOptions{Style: "animation", Output: output, Generator: generator, Timeout: time.Second, Reference: "auto"}
	var out, errs bytes.Buffer
	err := runInteractiveChatWithImages(context.Background(), st, "cloud", "", func() runner.Runner { return &recordingRunner{response: "must not run"} }, time.Second, options, strings.NewReader("/image brass turtle\n/image in a forest\n/image at night\n"), &out, &errs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scene.png", "scene-2.png", "scene-3.png"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(output), name)); err != nil {
			t.Fatal(err)
		}
	}
	calls, _, reqs := generator.snapshot()
	if calls != 3 || reqs[1].Reference == nil || reqs[2].Reference == nil {
		t.Fatal("interactive references missing")
	}
	if !strings.HasPrefix(reqs[2].Prompt, "Requested revision:\nat night") || !strings.Contains(reqs[2].Prompt, "brass turtle") {
		t.Fatalf("third turn prompt = %q", reqs[2].Prompt)
	}
	if !strings.Contains(out.String(), "scene-3.png") {
		t.Fatal(out.String())
	}
}

func TestExplicitReferenceRequiresParameterizedBridge(t *testing.T) {
	gen := newFakeImageGenerator(t)
	ref, err := imagegen.NewReferenceImageFromPath(gen.result.Path, "")
	if err != nil {
		t.Fatal(err)
	}
	options := explicitChatImageOptions(gen, filepath.Join(t.TempDir(), "out.png"))
	options.Reference = gen.result.Path
	_, _, err = executeChatImageTurn(context.Background(), nil, "change setting", options)
	if err == nil || gen.calls != 0 || ref == nil {
		t.Fatal("legacy reference should fail before generation")
	}
}

func TestReferencePromptStillValidatesFullHistory(t *testing.T) {
	history := []store.Message{{Role: "user", Content: strings.Repeat("x", chat.MaxRenderedPromptBytes)}}
	if _, err := renderChatImagePromptWithReference(history, "snow", true); err == nil {
		t.Fatal("reference bypassed transcript limit")
	}
}
