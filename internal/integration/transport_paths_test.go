// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/cli"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/server"
)

// transportResolvedRunner represents already resolved fixture bridge references;
// it avoids real host discovery while retaining the actual ShortcutRunner.
type transportResolvedRunner struct{ *runner.ShortcutRunner }

// This fake is a child process, not a Runner stub: each public surface must
// reach the actual shared runner and deliver its complete rendered prompt in a
// file. A CLI that reads --prompt-file then forwards stdin still fails here.
func transportFileEcho(t *testing.T) (*runner.ShortcutRunner, string) {
	t.Helper()
	dir := t.TempDir()
	captured := filepath.Join(dir, "captured")
	script := `#!/bin/sh
if [ "$1" = list ]; then
  printf 'AFM Bridge - Cloud\nAFM Bridge - On-Device\n'
  exit 0
fi
[ "$#" = 6 ] && [ "$1" = run ] && [ "$3" = --output-type ] && [ "$4" = public.plain-text ] && [ "$5" = --input-path ] || { echo 'expected file transport' >&2; exit 64; }
[ -f "$6" ] || exit 65
cat > "` + captured + `.stdin"
[ ! -s "` + captured + `.stdin" ] || { echo 'unexpected child stdin' >&2; exit 66; }
cp "$6" "` + captured + `"
cat "$6"
`
	path := filepath.Join(dir, "shortcuts")
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := runner.New()
	r.ShortcutsPath = path
	return r, captured
}

func TestSharedTextTransportPublicSurfaces(t *testing.T) {
	const prompt = "Output exactly these two lines and nothing else. Preserve indentation. Do not add a code fence or explanation:\n\ndef answer():\n    return \"READY\""
	for _, surface := range []string{"positional", "stdin", "prompt-file", "document", "chat", "batch", "http-chat", "http-responses"} {
		t.Run(surface, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOLLIS_STATE_DIR", filepath.Join(root, "state"))
			r, captured := transportFileEcho(t)
			execute := func(args []string, stdin string) []byte {
				t.Helper()
				cmd := cli.NewRootCmd(func() runner.Runner { return transportResolvedRunner{r} })
				cmd.SetArgs(args)
				cmd.SetIn(strings.NewReader(stdin))
				var out, stderr bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&stderr)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("execute: %v; stderr=%s", err, stderr.String())
				}
				return out.Bytes()
			}
			write := func(name, text string) string {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			expected := prompt
			var output []byte
			switch surface {
			case "positional":
				output = execute([]string{"respond", prompt, "--model", "cloud", "--agent"}, "")
			case "stdin":
				output = execute([]string{"respond", "--model", "cloud", "--json"}, prompt)
			case "prompt-file":
				output = execute([]string{"respond", "--prompt-file", write("prompt.txt", prompt), "--model", "cloud", "--agent"}, "")
			case "document":
				const body = "Quoted \"value\"\r\n\tUnicode café ☁\n\n"
				path := write("note.txt", body)
				var err error
				expected, err = docinput.Prepare(prompt, []docinput.Document{{Name: "note.txt", Text: body}}, chat.MaxRenderedPromptBytes)
				if err != nil {
					t.Fatal(err)
				}
				output = execute([]string{"respond", prompt, "--file", path, "--model", "cloud", "--agent"}, "")
			case "chat":
				expected = chat.RenderTranscript(nil, prompt)
				output = execute([]string{"chat", prompt, "--model", "cloud", "--agent"}, "")
			case "batch":
				input := filepath.Join(root, "input")
				if err := os.Mkdir(input, 0700); err != nil {
					t.Fatal(err)
				}
				const body = "\tinput \"café\"\r\n\n"
				if err := os.WriteFile(filepath.Join(input, "note.txt"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				job := filepath.Join(root, "job.json")
				execute([]string{"batch", "plan", "--input-dir", input, "--prompt-file", write("prompt.txt", prompt), "--model", "cloud", "--output-dir", filepath.Join(root, "results"), "--job", job, "--json"}, "")
				execute([]string{"batch", "run", "--job", job, "--max-calls", "1", "--json"}, "")
				loaded, err := batch.NewLocalStore().Load(t.Context(), job)
				if err != nil {
					t.Fatal(err)
				}
				output, err = os.ReadFile(loaded.Items[0].Result.Path)
				if err != nil {
					t.Fatal(err)
				}
				expected, err = docinput.Prepare(prompt, []docinput.Document{{Name: "note.txt", Text: body}}, chat.MaxRenderedPromptBytes)
				if err != nil {
					t.Fatal(err)
				}
			case "http-chat", "http-responses":
				path := "/v1/responses"
				body := map[string]any{"model": "cloud", "input": prompt}
				expected = chat.RenderTranscript(nil, prompt)
				if surface == "http-chat" {
					path = "/v1/chat/completions"
					body = map[string]any{"model": "cloud", "messages": []map[string]string{{"role": "user", "content": prompt}}}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				server.NewUnauthenticated(r).Handler().ServeHTTP(recorder, req)
				if recorder.Code != http.StatusOK {
					t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
				}
				output = recorder.Body.Bytes()
			}
			staged, err := os.ReadFile(captured)
			if err != nil {
				t.Fatal(err)
			}
			if string(staged) != expected {
				t.Fatalf("staged bytes differ: got %q want %q", staged, expected)
			}
			// The fake returns the exact staged bytes. Public JSON surfaces must keep
			// them intact after their own wrapping, escaping and serialization.
			var decoded struct {
				Response string `json:"response"`
				Results  struct {
					Response string `json:"response"`
				} `json:"results"`
				Content string `json:"content"`
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
				Output []struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"output"`
			}
			if err := json.Unmarshal(output, &decoded); err != nil {
				t.Fatalf("invalid public JSON: %v: %s", err, output)
			}
			response := decoded.Results.Response
			switch surface {
			case "stdin":
				response = decoded.Response
			case "batch":
				response = decoded.Content
			case "http-chat":
				if len(decoded.Choices) != 1 {
					t.Fatalf("unexpected choices: %s", output)
				}
				response = decoded.Choices[0].Message.Content
			case "http-responses":
				if len(decoded.Output) != 1 || len(decoded.Output[0].Content) != 1 {
					t.Fatalf("unexpected output: %s", output)
				}
				response = decoded.Output[0].Content[0].Text
			}
			if response != expected {
				t.Fatalf("public result lost response bytes: got %q want %q", response, expected)
			}
		})
	}
}
