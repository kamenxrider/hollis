package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type tokenLimitRunner struct {
	nativeFake
	tokens int
}

func (f *tokenLimitRunner) RunGeneration(_ context.Context, model runner.Model, _ runner.GenerationRequest, emit func(string) error) (runner.Completion, error) {
	if emit != nil {
		if err := emit("Answer"); err != nil {
			return runner.Completion{}, err
		}
	}
	return runner.Completion{Text: "Answer", Model: model, Usage: &runner.Usage{InputTokens: 5, OutputTokens: f.tokens}}, nil
}

// Dispatch only on a blank line, like an SSE client, not on individual data
// lines. Missing event separators must fail even when the JSON itself is valid.
func parseSSE(t *testing.T, wire string) []map[string]any {
	t.Helper()
	var data []string
	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(wire))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data = append(data, strings.TrimPrefix(line, "data: "))
		} else if line == "" && len(data) != 0 {
			value := strings.Join(data, "\n")
			if value != "[DONE]" {
				var event map[string]any
				if err := json.Unmarshal([]byte(value), &event); err != nil {
					t.Fatalf("invalid SSE event: %v", err)
				}
				events = append(events, event)
			}
			data = nil
		}
	}
	if scanner.Err() != nil || len(data) != 0 {
		t.Fatal("unterminated SSE event")
	}
	return events
}

func TestTokenLimitTermination(t *testing.T) {
	for _, endpoint := range nativeEndpoints {
		for _, stream := range []bool{false, true} {
			for _, tokens := range []int{2, 12, 14} {
				t.Run(fmt.Sprintf("%s/stream=%v/tokens=%d", endpoint.path, stream, tokens), func(t *testing.T) {
					key := "max_tokens"
					if endpoint.path == "/v1/responses" {
						key = "max_output_tokens"
					}
					body := strings.Replace(endpoint.body, "%s", fmt.Sprintf(`,"%s":12,"stream":%v`, key, stream), 1)
					res := post(t, NewUnauthenticated(&tokenLimitRunner{tokens: tokens}).Handler(), endpoint.path, body)
					if res.Code != 200 {
						t.Fatalf("%d: %s", res.Code, res.Body.String())
					}
					var result map[string]any
					if stream {
						events := parseSSE(t, res.Body.String())
						if len(events) < 3 {
							t.Fatalf("too few SSE events: %d", len(events))
						}
						result = events[len(events)-1]
						if endpoint.path == "/v1/responses" {
							want := "response.completed"
							if tokens >= 12 {
								want = "response.incomplete"
							}
							if result["type"] != want {
								t.Fatalf("terminal type: %v", result)
							}
							result = result["response"].(map[string]any)
						}
					} else if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if endpoint.path == "/v1/chat/completions" {
						finish := result["choices"].([]any)[0].(map[string]any)["finish_reason"]
						want := "stop"
						if tokens >= 12 {
							want = "length"
						}
						if finish != want {
							t.Fatalf("finish=%v want %s", finish, want)
						}
					} else {
						want := "completed"
						if tokens >= 12 {
							want = "incomplete"
							if result["incomplete_details"].(map[string]any)["reason"] != "max_output_tokens" {
								t.Fatal(result)
							}
						}
						if result["status"] != want {
							t.Fatalf("status=%v want %s", result["status"], want)
						}
						if tokens < 12 && result["incomplete_details"] != nil {
							t.Fatal("natural answer marked incomplete")
						}
					}
				})
			}
		}
	}
}

func TestUnauthenticatedLoopbackAuthorities(t *testing.T) {
	for _, host := range []string{"localhost", "localhost:1978", "127.0.0.1", "127.0.0.1:1978", "[::1]", "[::1]:1978", "attacker.example", "localhost.attacker.example", "127.0.0.2", "localhost:", ""} {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Host = host
		res := httptest.NewRecorder()
		NewUnauthenticated(&echoRunner{}).Handler().ServeHTTP(res, req)
		allowed := host == "localhost" || host == "localhost:1978" || host == "127.0.0.1" || host == "127.0.0.1:1978" || host == "[::1]" || host == "[::1]:1978"
		if (res.Code == 200) != allowed {
			t.Errorf("host=%q status=%d", host, res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/v1/models", nil)
	req.Header.Set("Origin", "http://attacker.example")
	res := httptest.NewRecorder()
	NewUnauthenticated(&echoRunner{}).Handler().ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatalf("foreign origin status=%d", res.Code)
	}
}

func TestRequestBodyEightMiBCap(t *testing.T) {
	if MaxRequestBytes != 8<<20 {
		t.Fatalf("body limit changed: %d", MaxRequestBytes)
	}
	for _, endpoint := range nativeEndpoints {
		prefix := `{"model":"local","input":"x"}`
		if endpoint.path == "/v1/chat/completions" {
			prefix = `{"model":"local","messages":[{"role":"user","content":"x"}]}`
		}
		for _, excess := range []int{0, 1} {
			fake := &nativeFake{}
			body := prefix + strings.Repeat(" ", (8<<20)-len(prefix)+excess)
			res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path, body)
			want := 200
			if excess != 0 {
				want = 413
			}
			if res.Code != want || (excess != 0 && fake.calls.Load() != 0) {
				t.Fatalf("%s excess=%d status=%d calls=%d", endpoint.path, excess, res.Code, fake.calls.Load())
			}
		}
	}
}
