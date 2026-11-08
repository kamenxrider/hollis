// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestShortcutRenderedPrompts(t *testing.T) {
	want := "Prompt:\nYou are continuing an existing conversation.\n\nUSER:\nSay hello.\n\nRespond to the final USER message while preserving the conversation context.\n\n\nInstructions:\nReply READY.\n\nNo punctuation."
	for _, model := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		for _, responses := range []bool{false, true} {
			path := "/v1/chat/completions"
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"system","content":"Reply READY."},{"role":"developer","content":"No punctuation."},{"role":"user","content":"Say hello."}]}`, model)
			if responses {
				path = "/v1/responses"
				body = fmt.Sprintf(`{"model":%q,"instructions":"Reply READY.","input":[{"role":"developer","content":"No punctuation."},{"role":"user","content":"Say hello."}]}`, model)
			}
			res := post(t, NewUnauthenticated(&echoRunner{}).Handler(), path, body)
			var got struct {
				Choices []struct{ Message struct{ Content string } }
				Output  []struct{ Content []struct{ Text string } }
			}
			if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &got) != nil {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			var prompt string
			if responses {
				prompt = got.Output[0].Content[0].Text
			} else {
				prompt = got.Choices[0].Message.Content
			}
			if prompt != want {
				t.Fatalf("%s %s got=%q want=%q", model, path, prompt, want)
			}
			t.Logf("rendered-prompt http-%s-responses-%t %q", model, responses, prompt)
		}
	}
}
