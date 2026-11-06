// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestRequestTimeoutAdvice(t *testing.T) {
	for _, model := range []runner.Model{runner.ModelAuto, runner.ModelCloud, runner.ModelCloudPro, runner.ModelOnDevice, runner.ModelChatGPT} {
		for _, timeout := range []time.Duration{30 * time.Second, runner.MaxTimeout - time.Nanosecond, runner.MaxTimeout} {
			t.Run(string(model)+"/"+timeout.String(), func(t *testing.T) {
				original := &runner.Error{Kind: runner.KindTimeout, ExitCode: -1, Err: errors.New("synthetic timeout")}
				err := toCLIRequestError(original, model, timeout)
				if ExitCode(err) != 7 || errorCode(err) != "timeout" {
					t.Fatalf("classification: %v", err)
				}
				var re *runner.Error
				if !errors.As(err, &re) || re != original {
					t.Fatal("lost underlying timeout classification")
				}
				ceiling := timeout == runner.MaxTimeout
				if strings.Contains(err.Error(), "larger --timeout") == ceiling ||
					strings.Contains(err.Error(), "the 120s maximum --timeout was already selected") != ceiling {
					t.Fatalf("wrong ceiling advice: %v", err)
				}
				if !strings.Contains(err.Error(), "does not identify the cause or confirm whether it completed. Do not automatically retry") {
					t.Fatalf("lost uncertainty: %v", err)
				}
				fields := commandErrorFields(err)
				if model == runner.ModelChatGPT {
					if fields["suggestion"] != chatGPTApprovalHint || strings.Count(err.Error(), chatGPTApprovalHint) != 1 {
						t.Fatalf("missing approval advice: %v", fields)
					}
				} else if fields["suggestion"] != nil || strings.Contains(err.Error(), "approval") {
					t.Fatalf("ChatGPT advice on another route: %v", fields)
				}
			})
		}
	}
}

func TestRespondTimeoutAdviceJSON(t *testing.T) {
	for _, timeout := range []string{"30s", "120s"} {
		for _, image := range []bool{false, true} {
			t.Run(timeout+map[bool]string{false: "/text", true: "/image"}[image], func(t *testing.T) {
				stubConfigPath(t)
				stubResolution(t, allImportedNames(), true, 27)
				r := &timeoutImageRunner{}
				cmd, flags := newRootCmdWithFlags(func() runner.Runner { return r })
				args := []string{"respond", "--model", "chatgpt", "--timeout", timeout, "--json", "--select", "response", "synthetic prompt"}
				if image {
					args = append(args, "--image", writeCLIImage(t, "fixture.png"))
				}
				var out, stderr bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&stderr)
				cmd.SetArgs(args)
				err := executeCommand(cmd, flags)
				if ExitCode(err) != 7 || r.calls != 1 || stderr.Len() != 0 {
					t.Fatalf("err=%v calls=%d stderr=%s", err, r.calls, stderr.String())
				}
				var body struct {
					Error map[string]any `json:"error"`
				}
				if err := json.Unmarshal(out.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error["suggestion"] != chatGPTApprovalHint || body.Error["code"] != "timeout" ||
					strings.Contains(body.Error["message"].(string), "larger --timeout") != (timeout == "30s") {
					t.Fatalf("wrong JSON: %s", out.String())
				}
			})
		}
	}
}

type timeoutImageRunner struct{ calls int }

func (r *timeoutImageRunner) Run(_ context.Context, model runner.Model, _ string) (string, runner.Model, error) {
	r.calls++
	return "", model, &runner.Error{Kind: runner.KindTimeout, ExitCode: -1, Err: errors.New("synthetic timeout")}
}

func (r *timeoutImageRunner) RunWithImages(ctx context.Context, model runner.Model, prompt string, _ []string) (string, runner.Model, error) {
	return r.Run(ctx, model, prompt)
}

func TestChatAndBatchTimeoutAdvice(t *testing.T) {
	for _, timeout := range []time.Duration{30 * time.Second, runner.MaxTimeout} {
		r := &timeoutImageRunner{}
		_, _, err := executeTurn(context.Background(), nil, runner.ModelChatGPT, "synthetic prompt", func() runner.Runner { return r }, timeout)
		if ExitCode(err) != 7 || commandErrorFields(err)["suggestion"] != chatGPTApprovalHint ||
			strings.Contains(err.Error(), "larger --timeout") != (timeout < runner.MaxTimeout) || r.calls != 1 {
			t.Fatalf("chat advice: %v calls=%d", err, r.calls)
		}
	}
	original := &runner.Error{Kind: runner.KindTimeout, ExitCode: -1, Err: errors.New("synthetic timeout")}
	err := batchRequestError(original, runner.ModelChatGPT)
	var re *runner.Error
	if !errors.As(err, &re) || re != original || commandErrorFields(err)["suggestion"] != chatGPTApprovalHint ||
		strings.Contains(err.Error(), "larger --timeout") {
		t.Fatalf("batch advice: %v", err)
	}
	for _, original := range []error{errors.New("synthetic failure"), &runner.Error{Kind: runner.KindRateLimited}} {
		if batchRequestError(original, runner.ModelChatGPT) != original {
			t.Fatal("batch changed a non-timeout error")
		}
	}
}

func TestRequestTimeoutAdviceExcludesDiscoveryProbeAndOtherFailures(t *testing.T) {
	for _, original := range []*runner.Error{
		{Kind: runner.KindTimeout, ListTimeout: true, ExitCode: -1, Err: errors.New("listing")},
		{Kind: runner.KindTimeout, ProbeTimeout: true, ExitCode: -1, Err: errors.New("probe")},
		{Kind: runner.KindContextCanceled, ExitCode: -1, Err: errors.New("canceled")},
	} {
		got := toCLIRequestError(original, runner.ModelChatGPT, runner.MaxTimeout)
		want := toCLIError(original)
		if got.Error() != want.Error() || commandErrorFields(got)["suggestion"] != nil {
			t.Fatalf("non-request advice changed: %v", got)
		}
	}
	if toCLIRequestError(nil, runner.ModelChatGPT, runner.MaxTimeout) != nil {
		t.Fatal("success became failure")
	}
}
