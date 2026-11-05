package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

type sampledCLIRunner struct {
	toolFixtureRunner
	generations []runner.GenerationRequest
}

func (r *sampledCLIRunner) RunGeneration(_ context.Context, model runner.Model, request runner.GenerationRequest, emit func(string) error) (runner.Completion, error) {
	r.generations = append(r.generations, request)
	if emit != nil {
		if err := emit("Sampled."); err != nil {
			return runner.Completion{}, err
		}
	}
	return runner.Completion{Text: "Sampled.", Model: model}, nil
}

func TestRespondNativeSampling(t *testing.T) {
	r := &sampledCLIRunner{}
	out, err := executeTools(t, r, "respond", "--model", "local", "--temperature", "0.7", "--top-p", "0.8", "--seed", "18446744073709551615", "--max-tokens", "12", "Hello")
	if err != nil || out != "Sampled.\n" || len(r.generations) != 1 {
		t.Fatalf("out=%q err=%v generations=%d", out, err, len(r.generations))
	}
	raw, _ := json.Marshal(r.generations[0].Options)
	if string(raw) != `{"temperature":0.7,"top_p":0.8,"max_tokens":12,"seed":18446744073709551615}` {
		t.Fatalf("options changed: %s", raw)
	}
	for _, args := range [][]string{
		{"--temperature", "-1"}, {"--temperature", "NaN"}, {"--top-p", "0"},
		{"--temperature", "0", "--seed", "1"}, {"--seed", "-1"}, {"--max-tokens", "0"},
		{"--model", "cloud", "--seed", "1"}, {"--model", "on-device", "--temperature", "0"},
	} {
		r := &sampledCLIRunner{}
		_, err := executeTools(t, r, append(append([]string{"respond", "--model", "local"}, args...), "Hello")...)
		if ExitCode(err) != 2 || len(r.generations) != 0 || len(r.requests) != 0 {
			t.Fatalf("invalid options dispatched: %v err=%v", args, err)
		}
	}
}

func TestRespondRequiredAndNamedNativeChoice(t *testing.T) {
	tools := writeToolsFixture(t, cliTools)
	for _, choice := range []string{"required", "function:get_weather"} {
		r := &toolFixtureRunner{replies: []string{`{"tool_calls":[{"function":"get_weather","arguments":{"city":"Tokyo"}}]}`}}
		out, err := executeTools(t, r, "respond", "--model", "local", "--tools", tools, "--tool-choice", choice, "--temperature", "0", "Weather")
		if err != nil || len(r.requests) != 1 {
			t.Fatalf("choice=%s err=%v out=%s", choice, err, out)
		}
		step := r.requests[0].Native
		if step.Options == nil || step.Options.Temperature == nil || *step.Options.Temperature != 0 ||
			(choice == "required" && step.Choice.Type != "required") ||
			(choice != "required" && (step.Choice.Type != "function" || step.Choice.Name != "get_weather")) {
			t.Fatalf("choice or sampling lost: %+v", step)
		}
	}
}
