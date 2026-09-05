package mcpagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOldFilesStaysInsideOutputRoot(t *testing.T) {
	output, outside := t.TempDir(), t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{filepath.Join(output, "old.txt"), filepath.Join(outside, "keep.txt")} {
		if err := os.WriteFile(name, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(output, "recent.txt"), []byte("recent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(output, "external")); err != nil {
		t.Fatal(err)
	}
	h := &ToolOutputHandler{OutputFolder: output}
	if err := h.CleanupOldFiles(24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old file remains: %v", err)
	}
	for _, name := range []string{filepath.Join(output, "recent.txt"), filepath.Join(outside, "keep.txt")} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("retained file missing: %v", err)
		}
	}
}

func TestCleanupOldFilesMissingOutputRoot(t *testing.T) {
	h := &ToolOutputHandler{OutputFolder: filepath.Join(t.TempDir(), "missing")}
	if err := h.CleanupOldFiles(time.Hour); err != nil {
		t.Fatal(err)
	}
}
