package imagegen

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReferenceRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "reference.png"), referencePNG(t, 2, 2), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}

	_, err := NewReferenceImageFromPath(filepath.Join(alias, "reference.png"), "")
	if !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("symlinked parent was accepted: err=%v", err)
	}
}
