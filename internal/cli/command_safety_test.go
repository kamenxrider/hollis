package cli

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestLoneCommandLikePromptsRequireEscape(t *testing.T) {
	root := NewRootCmd(func() runner.Runner { t.Fatal("runner constructed"); return nil })
	for _, text := range []string{"doctor --json", "doctor --model=bogus", "serve --addr 127.0.0.1:18102", "chats search --limit=1", "respond prompt --model=local", "help-agent"} {
		_, err := ResolveCommandLine(root, []string{"--model", "local", text})
		if ExitCode(err) != 2 {
			t.Fatalf("%q err=%v exit=%d", text, err, ExitCode(err))
		}
		for _, args := range [][]string{{"--model", "local", "--", text}, {"respond", "--model", "local", text}} {
			if _, err := ResolveCommandLine(root, args); err != nil {
				t.Fatalf("escape %q: %v", args, err)
			}
		}
	}
	got, err := ResolveCommandLine(root, []string{"--model", "local", "Explain closures"})
	if err != nil || !reflect.DeepEqual(got, []string{"respond", "--model", "local", "Explain closures"}) {
		t.Fatalf("natural prompt changed: %q %v", got, err)
	}
}

func TestHelpKeepsSemanticFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"doctor", "--model=bogus"}, {"chats", "search", "--limit=0"},
		{"batch", "run", "--max-calls=101"}, {"mcp", "add", "--approval=bogus"},
		{"models", "--model=bogus"}, {"image", "generate", "--timeout=121s"},
		{"image", "generate", "--size=bad"},
	} {
		cmd := NewRootCmd(func() runner.Runner { t.Fatal("help constructed runner"); return nil })
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append(args, "--help"))
		if err := cmd.Execute(); ExitCode(err) != 2 {
			t.Fatalf("%q help: %v exit=%d", args, err, ExitCode(err))
		}
	}
}
