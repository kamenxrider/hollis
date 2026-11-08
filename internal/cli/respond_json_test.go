// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/jsonoutput"
	"github.com/kamenxrider/hollis/internal/runner"
)

func TestRespondJSONObject(t *testing.T) {
	stubConfigPath(t)
	cases := []struct {
		output, want string
		valid        bool
	}{
		{" \n{\"answer\":42}\n ", `{"answer":42}`, true},
		{"```json\n{\"answer\":42}\n```", `{"answer":42}`, true},
		{"```\n{}\n```", `{}`, true},
		{"Here is JSON:\n{}", "", false},
		{"[]", "", false}, {`{"broken":`, "", false}, {"", "", false},
	}
	for _, model := range []string{"cloud", "cloud-pro", "on-device", "chatgpt"} {
		for i, tc := range cases {
			for _, envelope := range []string{"", "--json", "--agent"} {
				t.Run(model+"/"+strconv.Itoa(i)+"/"+envelope, func(t *testing.T) {
					r := &recordingRunner{response: tc.output}
					args := []string{"respond", "-m", model, "--format", "json", "Return an object."}
					if envelope != "" {
						args = append(args, envelope)
					}
					out, _, err := runContextCommand(t, r, "", args...)
					if r.calls != 1 || r.prompts[0] != "Prompt:\nReturn an object.\n\nInstructions:\n"+jsonoutput.Instruction {
						t.Fatalf("calls=%d prompts=%q", r.calls, r.prompts)
					}
					if tc.valid {
						if err != nil {
							t.Fatal(err)
						}
						if envelope == "" {
							if out != tc.want+"\n" {
								t.Fatalf("out=%q", out)
							}
						} else {
							var got map[string]any
							if err := json.Unmarshal([]byte(out), &got); err != nil {
								t.Fatal(err)
							}
							if envelope == "--agent" {
								got = got["results"].(map[string]any)
							}
							if got["response"] != tc.want {
								t.Fatalf("out=%s", out)
							}
						}
					} else {
						if ExitCode(err) != 5 || errorCode(err) != "invalid_json_output" || err.Error() != jsonoutput.InvalidMessage {
							t.Fatalf("err=%v out=%s", err, out)
						}
						if envelope != "" {
							var got struct {
								Error struct {
									Details struct {
										Raw string `json:"raw_response"`
									} `json:"details"`
								} `json:"error"`
							}
							if json.Unmarshal([]byte(out), &got) != nil || got.Error.Details.Raw != tc.output {
								t.Fatalf("raw output lost: %q", out)
							}
						} else if out != "" {
							t.Fatalf("partial output: %q", out)
						}
					}
				})
			}
		}
	}
}

func TestRespondJSONObjectRefusalsBeforeInference(t *testing.T) {
	stubConfigPath(t)
	for _, extra := range [][]string{
		{"--model", "local"}, {"--stream"}, {"--schema", "missing"},
		{"--tools", "missing"}, {"--code"}, {"--count-tokens"}, {"--image", "missing"},
		{"--format", "xml"},
	} {
		r := &recordingRunner{}
		args := append([]string{"respond", "--format", "json", "hi"}, extra...)
		_, _, err := runContextCommand(t, r, "", args...)
		if ExitCode(err) != 2 || r.calls != 0 {
			t.Fatalf("args=%v err=%v calls=%d", args, err, r.calls)
		}
		if len(extra) > 1 && extra[1] == "local" && !strings.Contains(err.Error(), "--schema <file>") {
			t.Fatal(err)
		}
	}
	r := &recordingRunner{err: &runner.Error{Kind: runner.KindNoOutput}}
	_, _, err := runContextCommand(t, r, "", "respond", "-m", "cloud", "--format", "json", "hi")
	if errorCode(err) != "invalid_json_output" || r.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, r.calls)
	}
}
