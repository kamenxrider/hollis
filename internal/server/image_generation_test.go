// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/imagegen"
)

type recordingGenerator struct {
	mu      sync.Mutex
	calls   []imagegen.Request
	paths   []string
	cleaned int
	err     error
	data    []byte
	width   int
	height  int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *recordingGenerator) Generate(ctx context.Context, request imagegen.Request) (imagegen.Result, error) {
	g.mu.Lock()
	g.calls = append(g.calls, request)
	g.mu.Unlock()
	if g.started != nil {
		g.once.Do(func() { close(g.started) })
		select {
		case <-g.release:
		case <-ctx.Done():
			return imagegen.Result{}, ctx.Err()
		}
	}
	data := g.data
	if data == nil {
		data = testGeneratedPNG()
	}
	width, height := g.width, g.height
	if width == 0 {
		width = 2
	}
	if height == 0 {
		height = 1
	}
	file, err := os.CreateTemp("", "hollis-server-generation-*.png")
	if err != nil {
		return imagegen.Result{}, err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return imagegen.Result{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return imagegen.Result{}, err
	}
	g.mu.Lock()
	g.paths = append(g.paths, path)
	g.mu.Unlock()
	sum := sha256.Sum256(data)
	result := imagegen.Result{
		Path: path, Bytes: int64(len(data)), Width: width, Height: height,
		SHA256: hex.EncodeToString(sum[:]),
	}
	return result, g.err
}

func (g *recordingGenerator) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

func (g *recordingGenerator) lastCall() imagegen.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[len(g.calls)-1]
}

func testGenerationServer(generator *recordingGenerator, token string) *Server {
	server := New(&echoRunner{}, token)
	server.ImageGenerator = generator
	server.ImageBridges = map[string]string{
		"animation": "Hollis Image Animation", "illustration": "Hollis Image Illustration",
	}
	server.cleanupImage = func(result imagegen.Result) error {
		generator.mu.Lock()
		generator.cleaned++
		paths := append([]string(nil), generator.paths...)
		generator.mu.Unlock()
		var cleanupErr error
		for _, path := range append(paths, result.Path) {
			if path == "" {
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
		return cleanupErr
	}
	return server
}

func testGeneratedPNG() []byte {
	var buffer bytes.Buffer
	imageValue := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	imageValue.Set(0, 0, color.NRGBA{R: 255, A: 255})
	imageValue.Set(1, 0, color.NRGBA{B: 255, A: 255})
	if err := png.Encode(&buffer, imageValue); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func TestImageGenerationsSuccessUsesConfiguredDefaultStyle(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "")
	res := post(t, server.Handler(), "/v1/images/generations", `{"model":"hollis-image","prompt":"a red kite","n":1,"response_format":"b64_json"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		Created int64 `json:"created"`
		Data    []struct {
			Base64           string                 `json:"b64_json"`
			Style            string                 `json:"style"`
			Width            int                    `json:"width"`
			Height           int                    `json:"height"`
			SHA256           string                 `json:"sha256"`
			NativeWidth      int                    `json:"native_width"`
			NativeHeight     int                    `json:"native_height"`
			OutputProcessing imagegen.OutputOptions `json:"output_processing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if body.Created == 0 || len(body.Data) != 1 || body.Data[0].Style != "animation" || body.Data[0].Width != 2 || body.Data[0].Height != 1 || body.Data[0].NativeWidth != 2 || body.Data[0].NativeHeight != 1 {
		t.Fatalf("unexpected response: %+v", body)
	}
	decoded, err := base64.StdEncoding.DecodeString(body.Data[0].Base64)
	if err != nil || !bytes.Equal(decoded, testGeneratedPNG()) {
		t.Fatalf("unexpected PNG: decode=%v bytes=%d", err, len(decoded))
	}
	call := generator.lastCall()
	if call.Prompt != "a red kite" || call.BridgeRef != "Hollis Image Animation" || call.Timeout != imagegen.MaxTimeout {
		t.Fatalf("generator request: %+v", call)
	}
	if strings.Contains(res.Body.String(), `"model"`) || strings.Contains(res.Body.String(), `"usage"`) {
		t.Fatalf("invented generation metadata: %s", res.Body.String())
	}
	if generator.cleaned != 1 {
		t.Fatalf("cleanup calls=%d", generator.cleaned)
	}
	if _, err := os.Stat(call.BridgeRef); err == nil {
		t.Fatal("bridge unexpectedly treated as a filesystem output")
	}
}

func TestUnifiedImageBridgeCarriesStyleAcrossGenerationRoutes(t *testing.T) {
	for _, test := range []struct {
		path, body, style string
	}{
		{"/v1/images/generations", `{"prompt":"draw one","style":"genmoji"}`, "genmoji"},
		{"/v1/chat/completions", `{"model":"hollis-image","messages":[{"role":"user","content":"draw two"}],"image_generation":{"style":"sketch"}}`, "sketch"},
		{"/v1/responses", `{"model":"hollis-image","input":"draw three","image_generation":{"style":"chatgpt"}}`, "chatgpt"},
	} {
		t.Run(test.style, func(t *testing.T) {
			generator := &recordingGenerator{}
			server := testGenerationServer(generator, "")
			server.ImageBridges = nil
			server.ImageBridge = "Hollis Image Unified"
			res := post(t, server.Handler(), test.path, test.body)
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			call := generator.lastCall()
			if call.BridgeRef != server.ImageBridge || call.Style != test.style {
				t.Fatalf("request = %+v", call)
			}
		})
	}
}

func TestImageGenerationsRejectsInvalidRequestsWithoutCallingGenerator(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty prompt", `{"prompt":" "}`},
		{"multiple", `{"prompt":"x","n":2}`},
		{"null count", `{"prompt":"x","n":null}`},
		{"URL response", `{"prompt":"x","response_format":"url"}`},
		{"unknown style", `{"prompt":"x","style":"oil"}`},
		{"invalid native size", `{"prompt":"x","size":"1024"}`},
		{"invalid aspect ratio", `{"prompt":"x","aspect_ratio":"16"}`},
		{"fit alone", `{"prompt":"x","fit":"crop"}`},
		{"caller bridge", `{"prompt":"x","bridge":"My Shortcut"}`},
		{"caller path", `{"prompt":"x","output":"/tmp/x.png"}`},
		{"text model route", `{"model":"cloud","prompt":"x"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := &recordingGenerator{}
			res := post(t, testGenerationServer(generator, "").Handler(), "/v1/images/generations", test.body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if generator.callCount() != 0 {
				t.Fatalf("generator called %d times", generator.callCount())
			}
		})
	}
}

func TestImageGenerationLockedSessionReturnsActionableRedactedConflict(t *testing.T) {
	generator := &recordingGenerator{err: &imagegen.Error{
		Kind: imagegen.KindSessionLocked, Stderr: "Error: This shortcut requires your Mac to be unlocked.",
		Err: imagegen.ErrSessionLocked,
	}}
	res := post(t, testGenerationServer(generator, "").Handler(), "/v1/images/generations", `{"prompt":"draw"}`)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"code":"image_session_locked"`) || !strings.Contains(res.Body.String(), "check the active session") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "This shortcut requires") || strings.Contains(res.Body.String(), "Error:") {
		t.Fatalf("raw provider diagnostic leaked: %s", res.Body.String())
	}
}

func TestModelsListsConfiguredHollisImageRoute(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "")
	res := do(t, server.Handler(), http.MethodGet, "/v1/models", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `{"id":"hollis-image","object":"model","owned_by":"hollis"}`) {
		t.Fatalf("image route missing or mislabeled: status=%d body=%s", res.Code, res.Body.String())
	}
	server.ImageBridges = map[string]string{"made-up": "A Shortcut"}
	res = do(t, server.Handler(), http.MethodGet, "/v1/models", "")
	if strings.Contains(res.Body.String(), "hollis-image") {
		t.Fatalf("invalid image configuration surfaced: %s", res.Body.String())
	}
}

func TestImageGenerationsAppliesExplicitLocalOutputProcessing(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "")
	res := post(t, server.Handler(), "/v1/images/generations", `{"prompt":"square framing","aspect_ratio":"1:1","fit":"pad"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		Data []struct {
			Width            int                    `json:"width"`
			Height           int                    `json:"height"`
			NativeWidth      int                    `json:"native_width"`
			NativeHeight     int                    `json:"native_height"`
			OutputProcessing imagegen.OutputOptions `json:"output_processing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || len(body.Data) != 1 {
		t.Fatalf("response JSON: err=%v body=%s", err, res.Body.String())
	}
	image := body.Data[0]
	if image.Width != 2 || image.Height != 2 || image.NativeWidth != 2 || image.NativeHeight != 1 {
		t.Fatalf("native/final dimensions: %+v", image)
	}
	if image.OutputProcessing.AspectRatio != "1:1" || image.OutputProcessing.Fit != "pad" {
		t.Fatalf("output processing metadata: %+v", image.OutputProcessing)
	}
	if generator.callCount() != 1 || generator.cleaned != 1 {
		t.Fatalf("calls=%d cleanup=%d", generator.callCount(), generator.cleaned)
	}
}

func TestImageGenerationAuthAndUnconfiguredStyleRefuseBeforeGenerator(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "secret")
	unauthorized := post(t, server.Handler(), "/v1/images/generations", `{"prompt":"x"}`)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"x","style":"sketch"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer secret")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, request)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"style_unavailable"`) {
		t.Fatalf("unconfigured status=%d body=%s", res.Code, res.Body.String())
	}
	if generator.callCount() != 0 {
		t.Fatalf("generator called %d times", generator.callCount())
	}
}

func TestChatImageGenerationIncludesConversationAndReplaysOwnOutput(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "")
	requestBody := `{
		"model":"hollis-image",
		"messages":[
			{"role":"system","content":"Use a plain background"},
			{"role":"user","content":"Draw a red boat"},
			{"role":"assistant","content":"I will keep it simple"},
			{"role":"user","content":"Add one white sail"}
		],
		"image_generation":{"style":"illustration","size":"4x4","fit":"pad"}
	}`
	first := post(t, server.Handler(), "/v1/chat/completions", requestBody)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	prompt := generator.lastCall().Prompt
	wantPrompt := "SYSTEM:\nUse a plain background\n\nUSER:\nDraw a red boat\n\nASSISTANT:\nI will keep it simple\n\nUSER:\nAdd one white sail"
	if prompt != wantPrompt {
		t.Fatalf("generation prompt = %q, want %q", prompt, wantPrompt)
	}
	for _, unwanted := range []string{"You are continuing an existing conversation", "Respond to the final USER message"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("generation prompt contains text-chat instruction %q: %q", unwanted, prompt)
		}
	}
	var body struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil || len(body.Choices) != 1 {
		t.Fatalf("first response: err=%v body=%s", err, first.Body.String())
	}
	if !bytes.Contains(body.Choices[0].Message, []byte("not retained or reused")) || !bytes.Contains(body.Choices[0].Message, []byte("Add one white sail")) || !bytes.Contains(body.Choices[0].Message, []byte(`"native_width":2`)) || !bytes.Contains(body.Choices[0].Message, []byte(`"width":4`)) {
		t.Fatalf("generation marker lacks prompt or memory boundary: %s", body.Choices[0].Message)
	}
	var replayMessage any
	if err := json.Unmarshal(body.Choices[0].Message, &replayMessage); err != nil {
		t.Fatal(err)
	}
	replay, _ := json.Marshal(map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "Draw a red boat"},
			replayMessage,
			map[string]any{"role": "user", "content": "Make the sail blue"},
		},
		"image_generation": map[string]any{"style": "animation"},
	})
	second := post(t, server.Handler(), "/v1/chat/completions", string(replay))
	if second.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", second.Code, second.Body.String())
	}
	secondPrompt := generator.lastCall().Prompt
	wantSecondPrompt := "Original image description:\nDraw a red boat\n\nRequested revision 1:\nAdd one white sail\n\nRequested revision 2:\nMake the sail blue"
	if secondPrompt != wantSecondPrompt {
		t.Fatalf("replay prompt = %q, want %q", secondPrompt, wantSecondPrompt)
	}
	if strings.Contains(secondPrompt, "not retained or reused") || strings.Contains(secondPrompt, "SHA-256") {
		t.Fatalf("replay prompt retained generated artifact metadata: %q", secondPrompt)
	}
}

func TestResponsesImageGenerationIncludesConversationAndReplaysOwnOutput(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "")
	first := post(t, server.Handler(), "/v1/responses", `{
		"model":"hollis-image",
		"instructions":"Use flat colors",
		"input":[{"role":"user","content":"Draw a green tree"}],
		"image_generation":{"aspect_ratio":"1:1","fit":"crop"}
	}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if prompt := generator.lastCall().Prompt; !strings.Contains(prompt, "SYSTEM:\nUse flat colors") || !strings.Contains(prompt, "USER:\nDraw a green tree") {
		t.Fatalf("generation prompt lost conversation: %q", prompt)
	}
	var body struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil || len(body.Output) != 1 {
		t.Fatalf("first response: err=%v body=%s", err, first.Body.String())
	}
	if !bytes.Contains(body.Output[0], []byte(`"native_width":2`)) || !bytes.Contains(body.Output[0], []byte(`"width":1`)) || !bytes.Contains(body.Output[0], []byte(`"aspect_ratio":"1:1"`)) {
		t.Fatalf("responses output lacks explicit native/final processing metadata: %s", body.Output[0])
	}
	var outputItem any
	if err := json.Unmarshal(body.Output[0], &outputItem); err != nil {
		t.Fatal(err)
	}
	replay, _ := json.Marshal(map[string]any{
		"input": []any{
			map[string]any{"role": "user", "content": "Draw a green tree"},
			outputItem,
			map[string]any{"role": "user", "content": "Make it autumn"},
		},
		"image_generation": map[string]any{},
	})
	second := post(t, server.Handler(), "/v1/responses", string(replay))
	if second.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", second.Code, second.Body.String())
	}
	if prompt := generator.lastCall().Prompt; prompt != "Original image description:\nDraw a green tree\n\nRequested revision:\nMake it autumn" {
		t.Fatalf("replay prompt retained marker or lost context: %q", prompt)
	}
}

func TestImageGenerationReplayFilteringDoesNotWeakenPromptLimit(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "")
	marker := generationMarker(generatedImage{
		Style: "animation", NativeWidth: 1024, NativeHeight: 1024,
		Width: 1024, Height: 1024,
		SHA256: strings.Repeat("a", 64),
	}, strings.Repeat("x", MaxPromptBytes))
	request, err := json.Marshal(map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "Draw a tree"},
			{"role": "assistant", "content": marker},
			{"role": "user", "content": "Make it autumn"},
		},
		"image_generation": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := post(t, server.Handler(), "/v1/chat/completions", string(request))
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if generator.callCount() != 0 {
		t.Fatalf("generator called %d times", generator.callCount())
	}
}

func TestImageGenerationSharesCapacityAndCleansResultOnFailure(t *testing.T) {
	blocking := &recordingGenerator{started: make(chan struct{}), release: make(chan struct{})}
	server := testGenerationServer(blocking, "")
	server.MaxConcurrency = 1
	handler := server.Handler()
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- post(t, handler, "/v1/images/generations", `{"prompt":"first"}`)
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("first generation did not start")
	}
	second := post(t, handler, "/v1/images/generations", `{"prompt":"second"}`)
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") != "1" {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	close(blocking.release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if blocking.callCount() != 1 || blocking.cleaned != 1 {
		t.Fatalf("calls=%d cleanup=%d", blocking.callCount(), blocking.cleaned)
	}

	failing := &recordingGenerator{err: errors.New("private prompt and Apple stderr")}
	failureServer := testGenerationServer(failing, "")
	failure := post(t, failureServer.Handler(), "/v1/images/generations", `{"prompt":"secret prompt"}`)
	if failure.Code != http.StatusBadGateway || strings.Contains(failure.Body.String(), "private prompt") || strings.Contains(failure.Body.String(), "Apple stderr") {
		t.Fatalf("unsanitized failure: status=%d body=%s", failure.Code, failure.Body.String())
	}
	if failing.cleaned != 1 {
		t.Fatalf("failure cleanup=%d", failing.cleaned)
	}

	timedOut := &recordingGenerator{err: context.DeadlineExceeded}
	timeoutServer := testGenerationServer(timedOut, "")
	timeout := post(t, timeoutServer.Handler(), "/v1/images/generations", `{"prompt":"bounded"}`)
	if timeout.Code != http.StatusGatewayTimeout || !strings.Contains(timeout.Body.String(), `"code":"image_generation_timeout"`) {
		t.Fatalf("timeout status=%d body=%s", timeout.Code, timeout.Body.String())
	}
	if timedOut.cleaned != 1 {
		t.Fatalf("timeout cleanup=%d", timedOut.cleaned)
	}
}

func TestImageGenerationCleansOriginalResultWhenTransformFails(t *testing.T) {
	generator := &recordingGenerator{data: []byte("not a PNG"), width: 2, height: 1}
	server := testGenerationServer(generator, "")
	res := post(t, server.Handler(), "/v1/images/generations", `{"prompt":"square","aspect_ratio":"1:1","fit":"crop"}`)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), `"code":"image_processing_failed"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if generator.callCount() != 1 || generator.cleaned != 1 {
		t.Fatalf("calls=%d cleanup=%d", generator.callCount(), generator.cleaned)
	}
	for _, path := range generator.paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("original result not cleaned: path=%s err=%v", path, err)
		}
	}
}

func TestImageGenerationExtensionRejectsModelAndInputImagesWithoutCalls(t *testing.T) {
	generator := &recordingGenerator{}
	server := testGenerationServer(generator, "")
	for _, test := range []struct {
		path string
		body string
	}{
		{"/v1/chat/completions", `{"model":"cloud","messages":[{"role":"user","content":"x"}],"image_generation":{}}`},
		{"/v1/responses", `{"model":"cloud","input":"x","image_generation":{}}`},
		{"/v1/chat/completions", `{"messages":[{"role":"user","content":"x"}],"image_generation":{"quality":"hd"}}`},
		{"/v1/chat/completions", `{"messages":[{"role":"user","content":[{"type":"text","text":"edit"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],"image_generation":{}}`},
		{"/v1/responses", `{"input":[{"role":"user","content":[{"type":"input_text","text":"edit"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"image_generation":{}}`},
	} {
		res := post(t, server.Handler(), test.path, test.body)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", test.path, res.Code, res.Body.String())
		}
	}
	if generator.callCount() != 0 {
		t.Fatalf("generator called %d times", generator.callCount())
	}
}
