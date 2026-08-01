package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestMCPBridgeLargeToolOutputE2E exercises the complete production transport:
// a real mcpbridge subprocess speaks stdio MCP to a real MCP client, forwards
// execute_shell_command to an HTTP server, persists the oversized response,
// and returns a bounded result. No truncation helper is called directly.
func TestMCPBridgeLargeToolOutputE2E(t *testing.T) {
	const (
		token     = "large-output-e2e-token" // #nosec G101 -- test-only credential for the local httptest server.
		sessionID = "large-output-e2e-session"
	)
	workingDir := t.TempDir()
	outputDir := filepath.Join(workingDir, "tool_output_folder")
	largeStdout := "BEGIN-LARGE-OUTPUT\n" + strings.Repeat("result-row-🙂-"+strings.Repeat("x", 1024)+"\n", 1200) + "END-LARGE-OUTPUT\n"
	largeShellResultBytes, err := json.Marshal(map[string]any{
		"stdout":            largeStdout,
		"stderr":            "warning retained",
		"exit_code":         0,
		"execution_time_ms": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(largeShellResultBytes) <= 1024*1024 {
		t.Fatalf("fixture is only %d bytes; it must exceed the historical 1 MiB failure boundary", len(largeShellResultBytes))
	}
	const smallShellResult = `{"stdout":"ok","stderr":"","exit_code":0,"execution_time_ms":1}`

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/custom/execute_shell_command" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			http.Error(w, "bad authorization", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("X-Session-ID"); got != sessionID {
			http.Error(w, "bad session", http.StatusBadRequest)
			return
		}
		var args map[string]any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result := string(largeShellResultBytes)
		if args["command"] == "printf ok" {
			result = smallShellResult
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	defer api.Close()

	bridgeBinary := buildLargeOutputE2EBridge(t)
	toolsJSON, err := json.Marshal([]map[string]any{{
		"name":        "execute_shell_command",
		"description": "Run a shell command.",
		"type":        "custom",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
			"required":   []string{"command"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	bridgeClient, err := client.NewStdioMCPClient(bridgeBinary, append(os.Environ(),
		"MCP_API_URL="+api.URL,
		"MCP_API_TOKEN="+token,
		"MCP_SESSION_ID="+sessionID,
		"MCP_TOOL_OUTPUT_DIR="+outputDir,
		"MCP_TOOLS="+string(toolsJSON),
	))
	if err != nil {
		t.Fatalf("start real mcpbridge: %v", err)
	}
	defer bridgeClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := bridgeClient.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "large-output-e2e", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize real mcpbridge: %v", err)
	}

	largeResult := callLargeOutputE2ETool(t, ctx, bridgeClient, "produce large output")
	largeText := largeOutputE2EText(t, largeResult)
	if len(largeText) > maxBridgeToolResultBytes {
		t.Fatalf("MCP result is %d bytes, transport limit is %d", len(largeText), maxBridgeToolResultBytes)
	}
	if wire, err := json.Marshal(largeResult); err != nil || len(wire) >= 1024*1024 {
		t.Fatalf("encoded MCP result bytes=%d err=%v; must stay below 1 MiB", len(wire), err)
	}

	var bounded map[string]any
	if err := json.Unmarshal([]byte(largeText), &bounded); err != nil {
		t.Fatalf("bounded shell result is not valid JSON: %v", err)
	}
	if bounded["output_truncated"] != true {
		t.Fatalf("missing output_truncated marker: %#v", bounded)
	}
	savedPath, _ := bounded["full_output_path"].(string)
	if savedPath == "" || !strings.HasPrefix(savedPath, outputDir+string(os.PathSeparator)) {
		t.Fatalf("full output path %q is not under working-directory output folder %q", savedPath, outputDir)
	}
	// #nosec G304 -- savedPath is produced by the bridge under the test-owned outputDir and checked above.
	full, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read persisted full output: %v", err)
	}
	if string(full) != string(largeShellResultBytes) {
		t.Fatalf("persisted result is not byte-exact: got=%d want=%d", len(full), len(largeShellResultBytes))
	}
	if info, err := os.Stat(savedPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("persisted result mode must be 0600: info=%v err=%v", info, err)
	}

	// A second call through the same real process proves truncation did not
	// poison the stdio stream or terminate the bridge.
	smallResult := callLargeOutputE2ETool(t, ctx, bridgeClient, "printf ok")
	if got := largeOutputE2EText(t, smallResult); got != smallShellResult {
		t.Fatalf("small follow-up result changed: %q", got)
	}
	files, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly one offloaded file, got %d", len(files))
	}
}

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

func callLargeOutputE2ETool(t *testing.T, ctx context.Context, bridgeClient *client.Client, command string) *mcp.CallToolResult {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Name = "execute_shell_command"
	request.Params.Arguments = map[string]any{"command": command}
	result, err := bridgeClient.CallTool(ctx, request)
	if err != nil {
		t.Fatalf("real mcpbridge tool call %q: %v", command, err)
	}
	if result.IsError {
		t.Fatalf("real mcpbridge tool call %q returned MCP error: %#v", command, result.Content)
	}
	return result
}

func largeOutputE2EText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("MCP result has no text content: %#v", result.Content)
	return ""
}
