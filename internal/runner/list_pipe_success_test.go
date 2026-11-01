package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestListShortcutsSuccessfulSubprocessClosesPipes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "list-shortcuts")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'first\\nsecond\\r\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	names, err := (&ShortcutRunner{ShortcutsPath: path}).ListShortcuts(ctx)
	elapsed := time.Since(start)
	if err != nil || !reflect.DeepEqual(names, []string{"first", "second"}) {
		t.Fatalf("successful subprocess returned names=%v err=%v", names, err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("successful subprocess waited for its deadline: elapsed=%v", elapsed)
	}
}
