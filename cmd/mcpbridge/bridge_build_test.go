package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildLargeOutputE2EBridge compiles the real mcpbridge binary for the e2e tests.
func buildLargeOutputE2EBridge(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve mcpbridge source directory")
	}
	binary := filepath.Join(t.TempDir(), "mcpbridge")
	// #nosec G204 -- the executable and arguments are fixed; binary is a test-owned temporary path.
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = filepath.Dir(filename)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build real mcpbridge: %v\n%s", err, output)
	}
	return binary
}
