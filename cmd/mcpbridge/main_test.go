package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBridgeRequestErrorIdentifiesTimeoutLayer(t *testing.T) {
	got := bridgeRequestError("custom", "execute_shell_command", "session-1", 90*time.Minute, context.DeadlineExceeded)
	for _, want := range []string{"TIMEOUT", "layer=mcpbridge_http", "tool=execute_shell_command", "session=session-1", "timeout=1h30m0s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q missing %q", got, want)
		}
	}
}

func TestBridgeRequestErrorIdentifiesCancellation(t *testing.T) {
	got := bridgeRequestError("virtual", "call_generic_agent", "session-2", time.Minute, context.Canceled)
	for _, want := range []string{"CANCELED", "layer=mcpbridge_http", "tool=call_generic_agent", "session=session-2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q missing %q", got, want)
		}
	}
}

func TestTruncateBridgeToolResultTextLeavesSmallResultUnchanged(t *testing.T) {
	const input = `{"stdout":"ok","stderr":"","exit_code":0}`
	got, truncated := truncateBridgeToolResultText("execute_shell_command", input, "")
	if truncated {
		t.Fatal("small result must not be marked truncated")
	}
	if got != input {
		t.Fatalf("small result changed: got %q", got)
	}
}

func TestTruncateBridgeShellResultPreservesValidJSONAndMetadata(t *testing.T) {
	inputBytes, err := json.Marshal(map[string]any{
		"stdout":            strings.Repeat("large stdout line\n", 80000),
		"stderr":            "warning at the end",
		"exit_code":         7,
		"execution_time_ms": 12.5,
	})
	if err != nil {
		t.Fatal(err)
	}

	fullPath := "/workspace/tool_output_folder/full.json"
	got, truncated := truncateBridgeToolResultText("execute_shell_command", string(inputBytes), fullPath)
	if !truncated {
		t.Fatal("large shell result was not truncated")
	}
	if len(got) > maxBridgeToolResultBytes {
		t.Fatalf("result is %d bytes, limit is %d", len(got), maxBridgeToolResultBytes)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("truncated shell result is invalid JSON: %v", err)
	}
	if decoded["exit_code"] != float64(7) || decoded["execution_time_ms"] != 12.5 {
		t.Fatalf("shell metadata was not preserved: %#v", decoded)
	}
	if decoded["stderr"] != "warning at the end" {
		t.Fatalf("stderr changed unexpectedly: %q", decoded["stderr"])
	}
	if decoded["output_truncated"] != true {
		t.Fatalf("missing output_truncated marker: %#v", decoded)
	}
	if decoded["full_output_path"] != fullPath {
		t.Fatalf("full_output_path = %q, want %q", decoded["full_output_path"], fullPath)
	}
	stdout, _ := decoded["stdout"].(string)
	if !strings.Contains(stdout, "bridge tool output truncated") {
		t.Fatalf("stdout lacks truncation notice: %q", stdout[len(stdout)-200:])
	}
}

func TestTruncateBridgeToolResultTextKeepsHeadAndTail(t *testing.T) {
	input := "HEAD-" + strings.Repeat("x", maxBridgeToolResultBytes*2) + "-TAIL"
	fullPath := "/workspace/tool_output_folder/full.txt"
	got, truncated := truncateBridgeToolResultText("some_tool", input, fullPath)
	if !truncated {
		t.Fatal("large result was not truncated")
	}
	if len(got) > maxBridgeToolResultBytes {
		t.Fatalf("result is %d bytes, limit is %d", len(got), maxBridgeToolResultBytes)
	}
	for _, want := range []string{"HEAD-", "bridge tool output truncated", fullPath, "-TAIL"} {
		if !strings.Contains(got, want) {
			t.Fatalf("truncated result missing %q", want)
		}
	}
}

func TestPrepareBridgeToolResultPersistsFullOutput(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "tool_output_folder")
	input := strings.Repeat("full-result-line\n", maxBridgeToolResultBytes)

	got, savedPath, truncated, saveErr := prepareBridgeToolResult("execute_shell_command", input, outputDir)
	if saveErr != nil {
		t.Fatalf("prepareBridgeToolResult() save error: %v", saveErr)
	}
	if !truncated || savedPath == "" {
		t.Fatalf("truncated=%v savedPath=%q", truncated, savedPath)
	}
	if !strings.HasPrefix(savedPath, outputDir+string(os.PathSeparator)) {
		t.Fatalf("saved path %q is not under %q", savedPath, outputDir)
	}
	// #nosec G304 -- savedPath is returned by prepareBridgeToolResult for the test-owned outputDir.
	full, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read saved output: %v", err)
	}
	if string(full) != input {
		t.Fatalf("saved output length=%d, want exact %d bytes", len(full), len(input))
	}
	if !strings.Contains(got, savedPath) {
		t.Fatalf("truncated response does not point to saved output: %q", got[len(got)-500:])
	}
	info, err := os.Stat(savedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("saved file mode=%o, want 600", info.Mode().Perm())
	}
}

func TestTruncateBridgeTextBytesPreservesUTF8(t *testing.T) {
	input := strings.Repeat("🙂", maxBridgeToolResultBytes)
	got := truncateBridgeTextBytes(input, 257)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got)
	}
	if len(got) > 257 {
		t.Fatalf("result is %d bytes, limit is 257", len(got))
	}
}
