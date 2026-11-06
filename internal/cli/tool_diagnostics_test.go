package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestMissingNamedToolDiagnostic(t *testing.T) {
	r := &toolFixtureRunner{}
	out, err := executeTools(t, r, "respond", "--model", "local",
		"--tools", writeToolsFixture(t, cliTools), "--tool-choice", "function:missing", "Weather?")
	if ExitCode(err) != 2 || out != "" || len(r.requests) != 0 ||
		!strings.Contains(err.Error(), `function "missing" was not offered`) ||
		!strings.Contains(err.Error(), "offered functions: get_weather") {
		t.Fatalf("out=%q err=%v requests=%d", out, err, len(r.requests))
	}
}

func TestBusyToolDecisionCLIExit(t *testing.T) {
	r := &toolFixtureRunner{err: &runner.Error{Kind: runner.KindModelBusy,
		Err: errors.New("the native local model is temporarily busy")}}
	_, err := executeTools(t, r, "respond", "--model", "local",
		"--tools", writeToolsFixture(t, cliTools), "Weather?")
	if ExitCode(err) != 5 || errorCode(err) != "model_busy" || len(r.requests) != 1 {
		t.Fatalf("err=%v requests=%d", err, len(r.requests))
	}
}
