// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type recordingImageRunner struct {
	mu sync.Mutex

	textCalls  int
	imageCalls int
	model      runner.Model
	prompt     string
	digests    [][32]byte
	paths      []string
	err        error
	entered    chan struct{}
	release    chan struct{}
	waitForCtx bool
}

func (r *recordingImageRunner) Run(_ context.Context, model runner.Model, _ string) (string, runner.Model, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.textCalls++
	return "text runner must not serve image input", model, errors.New("text runner called")
}

func (r *recordingImageRunner) RunWithImages(ctx context.Context, model runner.Model, prompt string, imagePaths []string) (string, runner.Model, error) {
	digests := make([][32]byte, len(imagePaths))
	for i, path := range imagePaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", model, err
		}
		digests[i] = sha256.Sum256(data)
	}

	r.mu.Lock()
	r.imageCalls++
	r.model = model
	r.prompt = prompt
	r.digests = digests
	r.paths = append([]string(nil), imagePaths...)
	entered := r.entered
	release := r.release
	waitForCtx := r.waitForCtx
	r.mu.Unlock()

	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if waitForCtx {
		<-ctx.Done()
		return "", model, &runner.Error{Kind: runner.KindContextCanceled, ExitCode: -1, Err: ctx.Err()}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return "", model, &runner.Error{Kind: runner.KindContextCanceled, ExitCode: -1, Err: ctx.Err()}
		}
	}
	if r.err != nil {
		return "", model, r.err
	}
	return "image answer", model, nil
}

func (r *recordingImageRunner) snapshot() (int, int, runner.Model, string, [][32]byte, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.textCalls, r.imageCalls, r.model, r.prompt, append([][32]byte(nil), r.digests...), append([]string(nil), r.paths...)
}

func TestImageEndpointsStageOrderedBytesAndPreserveResponseEnvelopes(t *testing.T) {
	pngBytes := encodeTestPNG(t, color.RGBA{R: 0x31, A: 0xff})
	jpegBytes := encodeTestJPEG(t, color.RGBA{B: 0x72, A: 0xff})
	pngURL := imageDataURL("image/png", pngBytes)
	jpegURL := imageDataURL("image/jpeg", jpegBytes)
	wantDigests := [][32]byte{sha256.Sum256(pngBytes), sha256.Sum256(jpegBytes)}

	tests := []struct {
		name       string
		path       string
		body       string
		wantObject string
	}{
		{
			name:       "chat completions",
			path:       "/v1/chat/completions",
			body:       fmt.Sprintf(`{"model":"cloud-pro","messages":[{"role":"system","content":"Be brief"},{"role":"user","content":[{"type":"text","text":"Compare "},{"type":"image_url","image_url":{"url":%q}},{"type":"text","text":"these"},{"type":"image_url","image_url":{"url":%q}}]}]}`, pngURL, jpegURL),
			wantObject: "chat.completion",
		},
		{
			name:       "responses",
			path:       "/v1/responses",
			body:       fmt.Sprintf(`{"model":"cloud-pro","instructions":"Be brief","input":[{"role":"user","content":[{"type":"input_text","text":"Compare "},{"type":"input_image","image_url":%q},{"type":"input_text","text":"these"},{"type":"input_image","image_url":%q}]}]}`, pngURL, jpegURL),
			wantObject: "response",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &recordingImageRunner{}
			res := post(t, NewUnauthenticated(r).Handler(), tc.path, tc.body)
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), `"object":"`+tc.wantObject+`"`) || !strings.Contains(res.Body.String(), `"model":"cloud-pro"`) {
				t.Fatalf("unexpected response envelope: %s", res.Body.String())
			}
			textCalls, imageCalls, model, prompt, digests, paths := r.snapshot()
			if textCalls != 0 || imageCalls != 1 || model != runner.ModelCloudPro {
				t.Fatalf("calls text=%d image=%d model=%q", textCalls, imageCalls, model)
			}
			if len(digests) != len(wantDigests) || digests[0] != wantDigests[0] || digests[1] != wantDigests[1] {
				t.Fatalf("image digests/order changed: %x", digests)
			}
			for _, want := range []string{"SYSTEM:\nBe brief", "USER:\nCompare these"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("prompt missing %q: %q", want, prompt)
				}
			}
			assertPathsRemoved(t, paths)
		})
	}
}

func TestImageModelSelectionAndLimitsHappenBeforeStaging(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	imageURL := imageDataURL("image/png", encodeTestPNG(t, color.RGBA{G: 0x51, A: 0xff}))

	r := &recordingImageRunner{}
	omitted := fmt.Sprintf(`{"messages":[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":%q}}]}]}`, imageURL)
	res := post(t, NewUnauthenticated(r).Handler(), "/v1/chat/completions", omitted)
	if res.Code != http.StatusOK {
		t.Fatalf("omitted model: status=%d body=%s", res.Code, res.Body.String())
	}
	_, _, gotModel, _, _, _ := r.snapshot()
	if gotModel != runner.ModelCloud || !strings.Contains(res.Body.String(), `"model":"cloud"`) {
		t.Fatalf("omitted image model used %q: %s", gotModel, res.Body.String())
	}

	for _, model := range []string{"auto", "on-device"} {
		t.Run(model, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":%q,"input":[{"role":"user","content":[{"type":"input_text","text":"Describe"},{"type":"input_image","image_url":%q}]}]}`, model, imageURL)
			before := tempEntries(t, root)
			res := post(t, NewUnauthenticated(&recordingImageRunner{}).Handler(), "/v1/responses", body)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"unsupported_parameter"`) {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if got := tempEntries(t, root); got != before {
				t.Fatalf("invalid model staged files: before=%d after=%d", before, got)
			}
		})
	}

	twoImages := fmt.Sprintf(`{"model":"chatgpt","input":[{"role":"user","content":[{"type":"input_text","text":"Compare"},{"type":"input_image","image_url":%q},{"type":"input_image","image_url":%q}]}]}`, imageURL, imageURL)
	before := tempEntries(t, root)
	res = post(t, NewUnauthenticated(&recordingImageRunner{}).Handler(), "/v1/responses", twoImages)
	if res.Code != http.StatusRequestEntityTooLarge || !strings.Contains(res.Body.String(), `"code":"context_too_large"`) {
		t.Fatalf("chatgpt count: status=%d body=%s", res.Code, res.Body.String())
	}
	if got := tempEntries(t, root); got != before {
		t.Fatalf("over-limit request staged files: before=%d after=%d", before, got)
	}
}

func TestImageAuthenticationAndCapacityPrecedeStaging(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	imageURL := imageDataURL("image/png", encodeTestPNG(t, color.RGBA{R: 0x88, A: 0xff}))
	body := fmt.Sprintf(`{"model":"cloud","input":[{"role":"user","content":[{"type":"input_text","text":"Describe"},{"type":"input_image","image_url":%q}]}]}`, imageURL)

	authRunner := &recordingImageRunner{}
	authServer := New(authRunner, "secret-token")
	stageCalls := 0
	authServer.stageImages = func(context.Context, []encodedImage) (stagedImages, error) {
		stageCalls++
		return stagedImages{}, errors.New("unauthorized staging must not run")
	}
	unauthorized := post(t, authServer.Handler(), "/v1/responses", body)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	if got := tempEntries(t, root); got != 0 || stageCalls != 0 {
		t.Fatalf("unauthorized request: staging entries=%d calls=%d", got, stageCalls)
	}

	blocking := &recordingImageRunner{entered: make(chan struct{}, 1), release: make(chan struct{})}
	srv := NewUnauthenticated(blocking)
	handler := srv.Handler()
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- post(t, handler, "/v1/responses", body) }()
	<-blocking.entered
	if got := tempEntries(t, root); got != 1 {
		t.Fatalf("active request staging entries=%d, want 1", got)
	}
	second := post(t, handler, "/v1/responses", body)
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body.String(), `"code":"server_busy"`) {
		t.Fatalf("busy status=%d body=%s", second.Code, second.Body.String())
	}
	if got := tempEntries(t, root); got != 1 {
		t.Fatalf("busy request created staging: entries=%d", got)
	}
	close(blocking.release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if got := tempEntries(t, root); got != 0 {
		t.Fatalf("successful request left %d staging entries", got)
	}
	_, imageCalls, _, _, _, _ := blocking.snapshot()
	if imageCalls != 1 {
		t.Fatalf("image calls=%d, want 1", imageCalls)
	}
}

func TestImageInternalStagingFailureIsServerError(t *testing.T) {
	imageURL := imageDataURL("image/png", encodeTestPNG(t, color.RGBA{A: 0xff}))
	body := fmt.Sprintf(`{"model":"cloud","input":[{"role":"user","content":[{"type":"input_text","text":"Describe"},{"type":"input_image","image_url":%q}]}]}`, imageURL)
	r := &recordingImageRunner{}
	srv := NewUnauthenticated(r)
	srv.stageImages = func(context.Context, []encodedImage) (stagedImages, error) {
		return stagedImages{}, errors.New("private filesystem detail")
	}
	res := post(t, srv.Handler(), "/v1/responses", body)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), `"code":"image_staging_failed"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "private filesystem detail") {
		t.Fatalf("staging error leaked internal detail: %s", res.Body.String())
	}
	_, imageCalls, _, _, _, _ := r.snapshot()
	if imageCalls != 0 || len(srv.slots()) != 0 {
		t.Fatalf("image calls=%d capacity=%d", imageCalls, len(srv.slots()))
	}
}

func TestImageRequestRequiresImageRunner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	imageURL := imageDataURL("image/png", encodeTestPNG(t, color.RGBA{A: 0xff}))
	body := fmt.Sprintf(`{"model":"cloud","messages":[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":%q}}]}]}`, imageURL)
	res := post(t, NewUnauthenticated(&echoRunner{}).Handler(), "/v1/chat/completions", body)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"model_unavailable"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if got := tempEntries(t, root); got != 0 {
		t.Fatalf("non-image runner request staged %d entries", got)
	}
}

func TestImageCancellationMapsToTimeoutAndCleansUp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	imageURL := imageDataURL("image/png", encodeTestPNG(t, color.RGBA{B: 0x45, A: 0xff}))
	body := fmt.Sprintf(`{"model":"cloud","messages":[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":%q}}]}]}`, imageURL)

	stagingCanceled := &recordingImageRunner{}
	srv := NewUnauthenticated(stagingCanceled)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusGatewayTimeout || !strings.Contains(res.Body.String(), `"code":"shortcut_timeout"`) {
		t.Fatalf("canceled staging: status=%d body=%s", res.Code, res.Body.String())
	}
	if len(srv.slots()) != 0 || tempEntries(t, root) != 0 {
		t.Fatalf("canceled staging leaked capacity or files")
	}

	waiting := &recordingImageRunner{entered: make(chan struct{}, 1), waitForCtx: true}
	srv = NewUnauthenticated(waiting)
	ctx, cancel = context.WithCancel(t.Context())
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(res, req)
		close(done)
	}()
	<-waiting.entered
	_, _, _, _, _, paths := waiting.snapshot()
	cancel()
	<-done
	if res.Code != http.StatusGatewayTimeout || !strings.Contains(res.Body.String(), `"code":"shortcut_timeout"`) {
		t.Fatalf("canceled run: status=%d body=%s", res.Code, res.Body.String())
	}
	assertPathsRemoved(t, paths)
	if len(srv.slots()) != 0 || tempEntries(t, root) != 0 {
		t.Fatalf("canceled run leaked capacity or files")
	}
}

func TestImageRunnerFailureCleansUpAndNeverFallsBackToText(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	r := &recordingImageRunner{err: errors.New("synthetic image runner failure")}
	imageURL := imageDataURL("image/png", encodeTestPNG(t, color.RGBA{A: 0xff}))
	body := fmt.Sprintf(`{"model":"cloud","input":[{"role":"user","content":[{"type":"input_text","text":"Describe"},{"type":"input_image","image_url":%q}]}]}`, imageURL)
	res := post(t, NewUnauthenticated(r).Handler(), "/v1/responses", body)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), `"code":"shortcut_failed"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	textCalls, imageCalls, _, _, _, paths := r.snapshot()
	if textCalls != 0 || imageCalls != 1 {
		t.Fatalf("calls text=%d image=%d", textCalls, imageCalls)
	}
	assertPathsRemoved(t, paths)
	if got := tempEntries(t, root); got != 0 {
		t.Fatalf("runner failure left %d staging entries", got)
	}
}

func assertPathsRemoved(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("staged path remains after request: %s (%v)", path, err)
		}
	}
}

func tempEntries(t *testing.T, root string) int {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(root, imageStagingPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
