package cli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/kamenxrider/hollis/internal/mcp"
	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/store"
)

type boundedToolResult struct {
	Server     string `json:"server"`
	Tool       string `json:"tool"`
	FullBytes  int    `json:"full_bytes"`
	ShownBytes int    `json:"shown_bytes"`
	Path       string `json:"path"`
}

const partialResultsSuggestion = "Re-run with --partial-results to let Local answer from the first part of the result (you will be told how much it saw)."
const localResultTooLargeCode = "tool_result_too_large_for_local"

func resultCapacity(err error) bool {
	var failure *runner.Error
	return errors.As(err, &failure) && failure.Kind == runner.KindContextCapacity
}

func (s *mcpSession) admitResult(tool mcp.Tool, id string, full mcp.Result, admit func(runner.ToolResult) error) (runner.ToolResult, error) {
	original := runner.ToolResult{CallID: id, Output: s.framer.Frame(tool.Server, tool.Name, full)}
	err := admit(original)
	if err == nil || !resultCapacity(err) {
		return original, err
	}
	if !s.partialResults {
		message := fmt.Sprintf("the result from %s/%s was %d bytes, too big for Local; nothing was truncated. %s",
			mcp.EscapeTerminal(tool.Server), mcp.EscapeTerminal(tool.Name), len(full.Text), partialResultsSuggestion)
		return original, &cliError{code: 5, machineCode: localResultTooLargeCode, err: errors.New(message), suggestion: partialResultsSuggestion}
	}
	if !s.boundResults {
		return original, err
	}
	originalErr := err
	limit := len(full.Text) - 1
	if full.TextBytes > 0 && full.TextBytes < len(full.Text) {
		// Text blocks precede structured content, which often repeats them.
		limit = full.TextBytes
	}
	for limit >= 0 {
		shown := excerptBoundary(full.Text, limit)
		if limit == full.TextBytes {
			shown = limit // A complete text-block prefix is itself a clean boundary.
		}
		partial := full
		partial.Text = full.Text[:shown] + fmt.Sprintf(
			"\n[partial result: showing the first %d of %d bytes; the rest was not shown to the model]\n",
			shown, len(full.Text))
		candidate := runner.ToolResult{CallID: id, Output: s.framer.Frame(tool.Server, tool.Name, partial)}
		if err := admit(candidate); err == nil {
			path, saveErr := saveFullToolResult(full.Text)
			if saveErr != nil {
				return original, transportErr(errors.New("could not save the full tool result privately; no excerpt was used"))
			}
			s.bounded = append(s.bounded, boundedToolResult{tool.Server, tool.Name, len(full.Text), shown, path})
			if _, err := fmt.Fprintf(s.diagnostic,
				"note: tool result from %s/%s was %d bytes; Local saw the first %d bytes. Full result saved to %q.\n",
				mcp.EscapeTerminal(tool.Server), mcp.EscapeTerminal(tool.Name), len(full.Text), shown, path); err != nil {
				return original, transportErr(errors.New("could not disclose the partial tool result"))
			}
			return candidate, nil
		} else if !resultCapacity(err) {
			return original, err
		}
		if shown == 0 {
			break
		}
		limit = min(shown/2, 16<<10)
	}
	return original, originalErr
}

// Prefer a complete line, then a JSON element, then a word. The marker and
// original nonce delimiters are added after cutting, never cut themselves.
func excerptBoundary(text string, limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit >= len(text) {
		return len(text)
	}
	if end := strings.LastIndexByte(text[:limit], '\n'); end >= 0 {
		return end + 1
	}
	quoted, escaped, element := false, false, 0
	for i := 0; i < limit; i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
		} else if c == '"' {
			quoted = true
		} else if c == ',' || c == '}' || c == ']' {
			element = i + 1
		}
	}
	if element > 0 {
		return element
	}
	end := 0
	for i, r := range text[:limit] {
		if unicode.IsSpace(r) {
			end = i
		}
	}
	return end
}

func saveFullToolResult(text string) (string, error) {
	state, err := store.DefaultStateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return "", err
	}
	state, err = filepath.EvalSymlinks(state)
	if err != nil {
		return "", err
	}
	for _, temp := range []string{"/tmp", "/private/tmp"} {
		if state == temp || strings.HasPrefix(state, temp+"/") {
			return "", errors.New("full tool results must not be saved under /tmp")
		}
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := root.Mkdir("tool-results", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := root.Lstat("tool-results")
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return "", errors.New("tool-results must be a private directory, not a symlink")
	}
	results, err := root.OpenRoot("tool-results")
	if err != nil {
		return "", err
	}
	defer results.Close()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	name := "result-" + hex.EncodeToString(random[:]) + ".txt"
	file, err := results.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(text)
	err = errors.Join(writeErr, file.Close())
	if err != nil {
		_ = results.Remove(name)
		return "", err
	}
	return filepath.Join(state, "tool-results", name), nil
}
