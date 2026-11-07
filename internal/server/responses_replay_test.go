// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func responsesOutput(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var decoded struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Output) == 0 {
		t.Fatalf("no output items in %s", body)
	}
	return decoded.Output
}

func replayInput(t *testing.T, items ...any) string {
	t.Helper()
	var flat []any
	for _, item := range items {
		if many, ok := item.([]json.RawMessage); ok {
			for _, raw := range many {
				flat = append(flat, raw)
			}
			continue
		}
		flat = append(flat, item)
	}
	encoded, err := json.Marshal(flat)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestResponsesOutputReplaysAsPlainInput(t *testing.T) {
	handler := NewUnauthenticated(&nativeFake{}).Handler()
	user := map[string]any{"type": "message", "role": "user", "content": "What is six times seven?"}
	first := post(t, handler, "/v1/responses", `{"model":"local","stream":false,"input":"What is six times seven?"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("%d %s", first.Code, first.Body.String())
	}
	input := replayInput(t, user, responsesOutput(t, first.Body.Bytes()), map[string]any{"role": "user", "content": "And doubled?"})
	second := post(t, handler, "/v1/responses", `{"model":"local","stream":false,"input":`+input+`}`)
	if second.Code != http.StatusOK {
		t.Fatalf("replayed output item refused: %d %s", second.Code, second.Body.String())
	}
}

func TestResponsesOutputReplaysAsToolInput(t *testing.T) {
	fake := &toolFake{replies: []string{weatherReply, "It is sunny.", "You're welcome."}}
	handler := NewUnauthenticated(fake).Handler()
	user := map[string]any{"role": "user", "content": "Weather in Tokyo?"}
	first := post(t, handler, "/v1/responses", `{"model":"local","tools":`+responsesHTTPTools+`,"input":"Weather in Tokyo?"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("%d %s", first.Code, first.Body.String())
	}
	calls := responsesOutput(t, first.Body.Bytes())
	var call struct {
		CallID string `json:"call_id"`
	}
	if err := json.Unmarshal(calls[0], &call); err != nil || call.CallID == "" {
		t.Fatalf("no call_id in %s", calls[0])
	}
	result := map[string]any{"type": "function_call_output", "call_id": call.CallID, "output": "sunny"}
	second := post(t, handler, "/v1/responses", `{"model":"local","tools":`+responsesHTTPTools+`,"input":`+replayInput(t, user, calls, result)+`}`)
	if second.Code != http.StatusOK {
		t.Fatalf("%d %s", second.Code, second.Body.String())
	}
	answer := responsesOutput(t, second.Body.Bytes())
	input := replayInput(t, user, calls, result, answer, map[string]any{"role": "user", "content": "Thanks"})
	third := post(t, handler, "/v1/responses", `{"model":"local","tools":`+responsesHTTPTools+`,"input":`+input+`}`)
	if third.Code != http.StatusOK {
		t.Fatalf("replayed output message refused: %d %s", third.Code, third.Body.String())
	}
}

func TestResponsesReplayRefusesNonEmptyAnnotations(t *testing.T) {
	fake := &nativeFake{}
	body := `{"model":"local","stream":false,"input":[{"type":"message","role":"assistant","id":"msg_1","status":"completed",` +
		`"content":[{"type":"output_text","text":"x","annotations":[{"type":"url_citation"}]}]},{"role":"user","content":"hi"}]}`
	res := post(t, NewUnauthenticated(fake).Handler(), "/v1/responses", body)
	if res.Code != http.StatusBadRequest || fake.calls.Load() != 0 {
		t.Fatalf("%d calls=%d %s", res.Code, fake.calls.Load(), res.Body.String())
	}
}
