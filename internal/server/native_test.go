// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

type nativeFake struct {
	calls    atomic.Int32
	complete func(context.Context, string) (runner.Completion, error)
	stream   func(context.Context, string, func(string) error) (runner.Completion, error)
}

func (f *nativeFake) Run(context.Context, runner.Model, string) (string, runner.Model, error) {
	panic("local must use native interface")
}
func (f *nativeFake) RunComplete(ctx context.Context, _ runner.Model, prompt string) (runner.Completion, error) {
	f.calls.Add(1)
	if f.complete != nil {
		return f.complete(ctx, prompt)
	}
	return runner.Completion{Text: "42", Model: runner.ModelLocal, Usage: &runner.Usage{InputTokens: 84, OutputTokens: 19, ReasoningTokens: 3}}, nil
}
func (f *nativeFake) Stream(ctx context.Context, _ runner.Model, prompt string, emit func(string) error) (runner.Completion, error) {
	f.calls.Add(1)
	if f.stream != nil {
		return f.stream(ctx, prompt, emit)
	}
	for _, delta := range []string{"Hello", " 世界", "\n\u001b[2J"} {
		if err := emit(delta); err != nil {
			return runner.Completion{}, err
		}
	}
	return runner.Completion{Text: "Hello 世界\n\u001b[2J", Model: runner.ModelLocal, Usage: &runner.Usage{InputTokens: 123, OutputTokens: 123}}, nil
}

var nativeEndpoints = []struct{ path, body string }{
	{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"hi"}]%s}`},
	{"/v1/responses", `{"model":"local","input":"hi"%s}`},
}

func nativeBody(template string, stream bool) string {
	suffix := ""
	if stream {
		suffix = `,"stream":true`
	}
	return strings.Replace(template, "%s", suffix, 1)
}
func decodeEvents(t *testing.T, body string) ([]string, []map[string]any) {
	t.Helper()
	var names []string
	var values []map[string]any
	for _, frame := range strings.Split(strings.TrimSpace(body), "\n\n") {
		name := ""
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "event: ") {
				name = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					names = append(names, "[DONE]")
					continue
				}
				var value map[string]any
				if err := json.Unmarshal([]byte(data), &value); err != nil {
					t.Fatalf("invalid event %q: %v", line, err)
				}
				names = append(names, name)
				values = append(values, value)
			}
		}
	}
	return names, values
}

func TestNativeCompleteMeasuredUsage(t *testing.T) {
	for i, endpoint := range nativeEndpoints {
		t.Run(endpoint.path, func(t *testing.T) {
			fake := &nativeFake{}
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, nativeBody(endpoint.body, false))
			if res.Code != 200 || res.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("%d %s", res.Code, res.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			usage := result["usage"].(map[string]any)
			input, output := "prompt_tokens", "completion_tokens"
			if i == 1 {
				input, output = "input_tokens", "output_tokens"
			}
			if usage[input] != float64(84) || usage[output] != float64(19) || usage["total_tokens"] != float64(103) {
				t.Fatalf("usage: %#v", usage)
			}
			if fake.calls.Load() != 1 {
				t.Fatalf("calls %d", fake.calls.Load())
			}
		})
	}
}

func TestNativeStreamWireLifecycles(t *testing.T) {
	for i, endpoint := range nativeEndpoints {
		t.Run(endpoint.path, func(t *testing.T) {
			res := post(t, NewUnauthenticated(&nativeFake{}).Handler(), endpoint.path, nativeBody(endpoint.body, true))
			if res.Code != 200 || res.Header().Get("Content-Type") != "text/event-stream" || !res.Flushed {
				t.Fatalf("not SSE: %d %s", res.Code, res.Body.String())
			}
			if strings.ContainsRune(res.Body.String(), '\x1b') {
				t.Fatal("raw escape escaped JSON framing")
			}
			names, events := decodeEvents(t, res.Body.String())
			var text strings.Builder
			if i == 0 {
				if names[len(names)-1] != "[DONE]" || len(events) != 5 {
					t.Fatalf("events %#v", names)
				}
				id := events[0]["id"]
				for j, event := range events {
					if event["id"] != id || event["model"] != "local" || event["object"] != "chat.completion.chunk" {
						t.Fatalf("chunk %#v", event)
					}
					if _, ok := event["usage"]; ok {
						t.Fatal("unqualified streaming usage")
					}
					choice := event["choices"].([]any)[0].(map[string]any)
					delta := choice["delta"].(map[string]any)
					if content, ok := delta["content"].(string); ok {
						text.WriteString(content)
					}
					if j == 0 && delta["role"] != "assistant" {
						t.Fatal("missing role")
					}
					if j < len(events)-1 && choice["finish_reason"] != nil {
						t.Fatal("premature finish")
					}
					if j == len(events)-1 && choice["finish_reason"] != "stop" {
						t.Fatal("missing successful finish")
					}
				}
			} else {
				want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.delta", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"}
				if !reflect.DeepEqual(names, want) {
					t.Fatalf("events %v", names)
				}
				for j, event := range events {
					if event["sequence_number"] != float64(j) || event["type"] != names[j] {
						t.Fatalf("sequence %#v", event)
					}
					if names[j] == "response.output_text.delta" {
						text.WriteString(event["delta"].(string))
					}
				}
				final := events[len(events)-1]["response"].(map[string]any)
				if final["status"] != "completed" || final["usage"] != nil {
					t.Fatalf("final %#v", final)
				}
			}
			if text.String() != "Hello 世界\n\u001b[2J" {
				t.Fatalf("response changed: %q", text.String())
			}
		})
	}
}

func TestNativeStreamingFailureHasNoSuccess(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		for _, failure := range []error{io.ErrUnexpectedEOF, &runner.Error{Kind: runner.KindNativeProtocol}, &runner.Error{Kind: runner.KindRequestDeclined}} {
			t.Run(endpoint.path+failure.Error(), func(t *testing.T) {
				fake := &nativeFake{stream: func(_ context.Context, _ string, emit func(string) error) (runner.Completion, error) {
					if err := emit("partial"); err != nil {
						return runner.Completion{}, err
					}
					return runner.Completion{}, failure
				}}
				srv := NewUnauthenticated(fake)
				res := post(t, srv.Handler(), endpoint.path, nativeBody(endpoint.body, true))
				body := res.Body.String()
				if !strings.Contains(body, "partial") || !strings.Contains(body, "error") || strings.Contains(body, "response.completed") || strings.Contains(body, "[DONE]") || strings.Contains(body, `"finish_reason":"stop"`) {
					t.Fatalf("bad failure: %s", body)
				}
				if len(srv.slots()) != 0 {
					t.Fatal("capacity leaked")
				}
			})
		}
	}
}

func TestNativeMismatchedFinalFails(t *testing.T) {
	fake := &nativeFake{stream: func(_ context.Context, _ string, emit func(string) error) (runner.Completion, error) {
		if err := emit("partial"); err != nil {
			return runner.Completion{}, err
		}
		return runner.Completion{Model: runner.ModelLocal, Text: "different"}, nil
	}}
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/responses", `{"model":"local","input":"hi","stream":true}`)
	if !strings.Contains(res.Body.String(), "response.failed") || strings.Contains(res.Body.String(), "response.completed") {
		t.Fatal(res.Body.String())
	}
}

func TestNativeCapacityAndContextErrors(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, kind := range []runner.Kind{runner.KindContextCapacity, runner.KindLocalUnavailable} {
			failure := &runner.Error{Kind: kind, Err: errors.New("PRIVATE HELPER STDERR")}
			fake := &nativeFake{complete: func(context.Context, string) (runner.Completion, error) { return runner.Completion{}, failure }, stream: func(context.Context, string, func(string) error) (runner.Completion, error) {
				return runner.Completion{}, failure
			}}
			srv := NewUnauthenticated(fake)
			res := post(t, srv.Handler(), "/v1/responses", nativeBody(nativeEndpoints[1].body, stream))
			want := http.StatusRequestEntityTooLarge
			if kind == runner.KindLocalUnavailable {
				want = http.StatusServiceUnavailable
			}
			if res.Code != want || strings.Contains(res.Body.String(), "PRIVATE") {
				t.Fatalf("%d: %s", res.Code, res.Body.String())
			}
			if kind == runner.KindContextCapacity && !strings.Contains(res.Body.String(), "no messages were shortened or dropped") {
				t.Fatal(res.Body.String())
			}
			if fake.calls.Load() != 1 || len(srv.slots()) != 0 {
				t.Fatal("retry or leaked capacity")
			}
		}
	}
}

func TestNativeInputValidationBeforeDispatch(t *testing.T) {
	for _, body := range []string{
		`{"model":"local","input":[{"role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"data:image/png;base64,invalid"}]}]}`,
		`{"model":"local","input":"hi","tools":[]}`,
		`{"model":"local","input":"hi","image_generation":{"style":"sketch"}}`,
		`{"model":"local","input":"` + strings.Repeat("x", MaxPromptBytes) + `"}`,
	} {
		fake := &nativeFake{}
		res := post(t, NewUnauthenticated(fake).Handler(), "/v1/responses", body)
		if res.Code < 400 || fake.calls.Load() != 0 {
			t.Fatalf("%d calls=%d %s", res.Code, fake.calls.Load(), res.Body.String())
		}
	}
}

func TestNativePreservesWholeConversation(t *testing.T) {
	messages := []reqMessage{{Role: "system", Content: "rules\r\n\t"}, {Role: "user", Content: "first"}, {Role: "assistant", Content: "reply"}, {Role: "user", Content: "last"}}
	want := transcriptFrom(messages)
	for _, stream := range []bool{false, true} {
		check := func(prompt string) {
			if prompt != want {
				t.Errorf("changed prompt %q; want %q", prompt, want)
			}
		}
		fake := &nativeFake{complete: func(_ context.Context, prompt string) (runner.Completion, error) {
			check(prompt)
			return runner.Completion{Model: runner.ModelLocal, Text: "ok"}, nil
		}, stream: func(_ context.Context, prompt string, emit func(string) error) (runner.Completion, error) {
			check(prompt)
			err := emit("ok")
			return runner.Completion{Model: runner.ModelLocal, Text: "ok"}, err
		}}
		body := `{"model":"local","messages":[{"role":"system","content":"rules\r\n\t"},{"role":"user","content":"first"},{"role":"assistant","content":"reply"},{"role":"user","content":"last"}]%s}`
		res := post(t, NewUnauthenticated(fake).Handler(), "/v1/chat/completions", nativeBody(body, stream))
		if res.Code != 200 || fake.calls.Load() != 1 {
			t.Fatalf("%d: %s", res.Code, res.Body.String())
		}
	}
}

func TestNativeStreamCancellationReleasesCapacity(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{true: "deadline", false: "cancel"}[deadline], func(t *testing.T) {
			started := make(chan struct{})
			fake := &nativeFake{stream: func(ctx context.Context, _ string, emit func(string) error) (runner.Completion, error) {
				if err := emit("partial"); err != nil {
					return runner.Completion{}, err
				}
				close(started)
				<-ctx.Done()
				return runner.Completion{}, ctx.Err()
			}}
			srv := NewUnauthenticated(fake)
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"local","input":"hi","stream":true}`)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { srv.Handler().ServeHTTP(res, req); close(done) }()
			<-started
			busy := post(t, srv.Handler(), "/v1/responses", `{"model":"local","input":"hi"}`)
			if busy.Code != 429 {
				t.Fatalf("busy status %d", busy.Code)
			}
			if !deadline {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancellation stalled")
			}
			if len(srv.slots()) != 0 || strings.Contains(res.Body.String(), "response.completed") {
				t.Fatal("failed stream treated as successful or leaked capacity")
			}
			next := post(t, srv.Handler(), "/v1/responses", `{"model":"local","input":"hi"}`)
			if next.Code != 200 {
				t.Fatalf("next status %d", next.Code)
			}
		})
	}
}

type failedStreamWriter struct{ header http.Header }

func (w *failedStreamWriter) Header() http.Header     { return w.header }
func (*failedStreamWriter) WriteHeader(int)           {}
func (*failedStreamWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (*failedStreamWriter) Flush()                    {}
func TestNativeDisconnectStopsEmitter(t *testing.T) {
	fake := &nativeFake{stream: func(ctx context.Context, _ string, emit func(string) error) (runner.Completion, error) {
		err := emit("first")
		if err == nil {
			t.Error("lost write error")
		}
		if ctx.Err() == nil {
			t.Error("model context not canceled")
		}
		return runner.Completion{}, err
	}}
	srv := NewUnauthenticated(fake)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"local","input":"hi","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(&failedStreamWriter{header: http.Header{}}, req)
	if len(srv.slots()) != 0 {
		t.Fatal("capacity leaked")
	}
}

func TestNativeDiscoverySeparateFromShortcuts(t *testing.T) {
	srv := NewUnauthenticated(&nativeFake{})
	srv.Available = map[string]bool{"local": true}
	res := do(t, srv.Handler(), http.MethodGet, "/v1/models", "")
	var body struct {
		Data []struct {
			ID           string         `json:"id"`
			Capabilities map[string]any `json:"capabilities"`
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "local" || body.Data[0].Capabilities["backend"] != "native" || body.Data[0].Capabilities["streaming"] != true {
		t.Fatalf("models: %s", res.Body.String())
	}
}

func TestNativeHTTPClientReceivesDeltaBeforeCompletion(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		t.Run(endpoint.path, func(t *testing.T) {
			finish := make(chan struct{})
			fake := &nativeFake{stream: func(ctx context.Context, _ string, emit func(string) error) (runner.Completion, error) {
				if err := emit("FIRST_VISIBLE"); err != nil {
					return runner.Completion{}, err
				}
				select {
				case <-finish:
				case <-ctx.Done():
					return runner.Completion{}, ctx.Err()
				}
				if err := emit("_END"); err != nil {
					return runner.Completion{}, err
				}
				return runner.Completion{Model: runner.ModelLocal, Text: "FIRST_VISIBLE_END"}, nil
			}}
			server := httptest.NewServer(NewUnauthenticated(fake).Handler())
			defer server.Close()
			client := &http.Client{Timeout: time.Second}
			response, err := client.Post(server.URL+endpoint.path, "application/json", strings.NewReader(nativeBody(endpoint.body, true)))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			// Read one byte at a time so the test observes the delta while the fake
			// provider is intentionally unable to finish. A buffered-at-end response
			// would time out rather than reaching the release below.
			var received strings.Builder
			one := make([]byte, 1)
			for !strings.Contains(received.String(), "FIRST_VISIBLE") {
				if _, err := io.ReadFull(response.Body, one); err != nil {
					t.Fatalf("no incremental delta: %v; %s", err, received.String())
				}
				received.Write(one)
			}
			if strings.Contains(received.String(), "response.completed") || strings.Contains(received.String(), "[DONE]") {
				t.Fatal("completion preceded delta")
			}
			close(finish)
			remaining, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			received.Write(remaining)
			if !strings.Contains(received.String(), "_END") {
				t.Fatal("missing last delta")
			}
		})
	}
}
