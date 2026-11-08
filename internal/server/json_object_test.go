// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/jsonoutput"
	"github.com/kamenxrider/hollis/internal/runner"
)

type objectRunner struct {
	output, prompt string
	err            error
	calls          int
}

func (r *objectRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	r.prompt = prompt
	return r.output, model, r.err
}

func TestShortcutJSONObject(t *testing.T) {
	for _, model := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		for _, tc := range []struct {
			output, want string
			valid        bool
		}{
			{" \n{\"answer\":42}\n ", `{"answer":42}`, true},
			{"```json\n{\"answer\":42}\n```", `{"answer":42}`, true},
			{"```\n{}\n```", `{}`, true},
			{"Here is JSON:\n{}", "", false},
			{"[]", "", false}, {`{"broken":`, "", false}, {"", "", false},
		} {
			r := &objectRunner{output: tc.output}
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"system","content":"No invented fields."},{"role":"user","content":"Return an object."}],"response_format":{"type":"json_object"}}`, model)
			res := post(t, NewUnauthenticated(r).Handler(), "/v1/chat/completions", body)
			wantPrompt := "Prompt:\nYou are continuing an existing conversation.\n\nUSER:\nReturn an object.\n\nRespond to the final USER message while preserving the conversation context.\n\n\nInstructions:\n" + jsonoutput.Instruction + "\n\nNo invented fields."
			if r.calls != 1 || r.prompt != wantPrompt {
				t.Fatalf("calls=%d prompt=%q", r.calls, r.prompt)
			}
			var got struct {
				Choices []struct{ Message struct{ Content string } }
				Error   struct {
					Code, Message string
					Details       struct {
						Raw string `json:"raw_response"`
					}
				}
			}
			if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if tc.valid {
				if res.Code != http.StatusOK || got.Choices[0].Message.Content != tc.want {
					t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
				}
			} else if res.Code != http.StatusBadGateway || got.Error.Code != "invalid_json_output" || got.Error.Message != jsonoutput.InvalidMessage || got.Error.Details.Raw != tc.output {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		}
	}
}

func TestShortcutJSONObjectRefusals(t *testing.T) {
	for _, extra := range []string{`,"stream":true`, `,"tools":[]`, `,"image_generation":{}`} {
		r := &objectRunner{}
		body := `{"model":"cloud","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}` + extra + `}`
		res := post(t, NewUnauthenticated(r).Handler(), "/v1/chat/completions", body)
		if res.Code != http.StatusBadRequest || r.calls != 0 {
			t.Fatalf("status=%d calls=%d body=%s", res.Code, r.calls, res.Body.String())
		}
	}
	r := &objectRunner{err: &runner.Error{Kind: runner.KindNoOutput}}
	res := post(t, NewUnauthenticated(r).Handler(), "/v1/chat/completions", `{"model":"cloud","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`)
	if res.Code != 502 || !strings.Contains(res.Body.String(), `"code":"invalid_json_output"`) || r.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", res.Code, r.calls, res.Body.String())
	}
}
