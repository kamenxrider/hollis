package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExitZeroRateLimitNearMissIsAnswer(t *testing.T) {
	const answer = "Error: Too many incoming requests!"
	r, _ := runnerWithFake(t, "empty")
	quoted := strings.ReplaceAll(answer, "'", "'\\''")
	if err := os.WriteFile(r.ShortcutsPath, []byte("#!/bin/sh\nprintf '%s' '"+quoted+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r.failureLog = func(context.Context, time.Time, time.Time) ShortcutFailureInfo {
		return ShortcutFailureInfo{}
	}

	text, _, err := r.Run(t.Context(), ModelCloud, "explain this phrase")
	if err != nil || text != answer {
		var classified *Error
		errors.As(err, &classified)
		t.Fatalf("near-miss answer was discarded: text=%q kind=%s", text, classified.Kind)
	}
}

func TestNonUTF8OutputBytesArePreserved(t *testing.T) {
	r, _ := runnerWithFake(t, "empty")
	if err := os.WriteFile(r.ShortcutsPath, []byte("#!/bin/sh\nprintf '\\377answer'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	text, _, err := r.Run(t.Context(), ModelCloud, "return bytes")
	if err != nil || string([]byte(text)) != "\xffanswer" {
		t.Fatalf("text bytes=%x err=%v", []byte(text), err)
	}
}
