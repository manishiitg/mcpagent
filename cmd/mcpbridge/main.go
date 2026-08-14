// mcpbridge is a stdio MCP server that bridges tool calls to HTTP API endpoints.
// It receives tool definitions via the MCP_TOOLS env var and forwards each tool
// call to the appropriate per-tool HTTP endpoint with bearer token authentication.
//
// This binary is launched by Claude Code via --mcp-config as a subprocess.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// writeReadyFileOnce, when MCP_READY_FILE is set, creates that file the first
// time the CLI completes its tools/list handshake — i.e. the instant the bridge
// tools become connected and callable. The parent adapter waits for this file
// before sending a cold session's first prompt, closing the race where the
// model's opening turn runs before its tools exist. Best-effort and idempotent:
// a failed write must never take down the bridge, and repeated tools/list calls
// only write once.
func makeReadyFileWriter(readyPath string) func() {
	var once sync.Once
	return func() {
		if strings.TrimSpace(readyPath) == "" {
			return
		}
		once.Do(func() {
			if dir := filepath.Dir(readyPath); dir != "" {
				//nolint:gosec // dir derives from a parent-controlled temp path.
				_ = os.MkdirAll(dir, 0o750)
			}
			if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil {
				log.Printf("mcpbridge: failed to write MCP_READY_FILE %q: %v", readyPath, err)
				return
			}
			log.Printf("mcpbridge: wrote MCP readiness marker %q (tools connected)", readyPath)
		})
	}
}

// ToolDef is the serialized tool definition passed via MCP_TOOLS env var.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Server      string          `json:"server"` // MCP server name (empty for custom/virtual)
	Type        string          `json:"type"`   // "mcp", "custom", or "virtual"
}

func isLongRunningDelegationTool(toolType, toolName string) bool {
	if toolType != "custom" {
		return false
	}
	switch toolName {
	case "call_sub_agent", "call_generic_agent", "execute_shell_command":
		return true
	default:
		return false
	}
}

func truncateBridgeErrorText(s string) string {
	const maxBytes = 16 * 1024
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + fmt.Sprintf("\n... truncated %d bytes ...", len(s)-maxBytes)
}

// maxBridgeToolResultBytes bounds the text returned over stdio MCP. Coding
// agents call mcpbridge directly, outside mcpagent's ToolOutputHandler, so the
// normal large-output offloading path cannot protect this boundary. A single
// large result is embedded in several JSON envelopes (HTTP response, MCP
// content, and the CLI's JSONL event stream); keeping the inner text at 128 KiB
// leaves ample room below adapters' per-event limits after escaping.
const maxBridgeToolResultBytes = 128 * 1024

func truncateBridgeToolResultText(toolName, value, fullOutputPath string) (string, bool) {
	if len(value) <= maxBridgeToolResultBytes {
		return value, false
	}

	// execute_shell_command returns a JSON object. Truncate its text fields
	// individually so the model still receives valid JSON and retains exit_code,
	// execution_time_ms, and other small diagnostic fields.
	if toolName == "execute_shell_command" {
		var shellResult map[string]any
		if json.Unmarshal([]byte(value), &shellResult) == nil {
			shellResult["output_truncated"] = true
			shellResult["original_result_bytes"] = len(value)
			if fullOutputPath != "" {
				shellResult["full_output_path"] = fullOutputPath
			}
			shellResult["truncation_note"] = bridgeTruncationNote(len(value), fullOutputPath)

			originalFields := make(map[string]string, 3)
			limits := make(map[string]int, 3)
			for _, field := range []string{"stdout", "stderr", "error"} {
				if text, ok := shellResult[field].(string); ok {
					originalFields[field] = text
					limits[field] = len(text)
				}
			}
			for attempts := 0; attempts < 64; attempts++ {
				encoded, err := json.Marshal(shellResult)
				if err == nil && len(encoded) <= maxBridgeToolResultBytes {
					return string(encoded), true
				}

				largestField := ""
				largestLimit := 0
				for field, limit := range limits {
					if limit > largestLimit {
						largestField, largestLimit = field, limit
					}
				}
				if largestField == "" || largestLimit == 0 {
					break
				}
				newLimit := largestLimit / 2
				limits[largestField] = newLimit
				shellResult[largestField] = truncateBridgeTextBytes(originalFields[largestField], newLimit)
			}
		}
	}

	return truncateBridgeTextBytesWithPath(value, maxBridgeToolResultBytes, fullOutputPath), true
}

// truncateBridgeTextBytes keeps both the beginning and end of a result. The
// beginning usually contains headers/context, while the end commonly contains
// command errors and summaries. UTF-8 boundaries are preserved.
func truncateBridgeTextBytes(value string, maxBytes int) string {
	return truncateBridgeTextBytesWithPath(value, maxBytes, "")
}

func truncateBridgeTextBytesWithPath(value string, maxBytes int, fullOutputPath string) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}

	marker := "\n... [" + bridgeTruncationNote(len(value), fullOutputPath) + "] ...\n"
	if len(marker) >= maxBytes {
		return validUTF8Prefix(marker, maxBytes)
	}
	payloadBytes := maxBytes - len(marker)
	headBytes := payloadBytes * 3 / 4
	tailBytes := payloadBytes - headBytes
	head := validUTF8Prefix(value, headBytes)
	tail := validUTF8Suffix(value, tailBytes)
	return head + marker + tail
}

func validUTF8Prefix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.ValidString(value[:maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}

func validUTF8Suffix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}

func bridgeTruncationNote(originalBytes int, fullOutputPath string) string {
	note := fmt.Sprintf("bridge tool output truncated; original=%d bytes", originalBytes)
	if fullOutputPath != "" {
		note += "; full output saved to: " + fullOutputPath
	}
	return note
}

func bridgeToolOutputDir() string {
	if configured := strings.TrimSpace(os.Getenv("MCP_TOOL_OUTPUT_DIR")); configured != "" {
		return configured
	}
	// Older parent processes do not set MCP_TOOL_OUTPUT_DIR. Prefer persistent
	// workflow paths already present in the coding-agent environment before
	// falling back to the bridge process's cwd.
	for _, key := range []string{"STEP_OUTPUT_DIR", "STEP_EXECUTION_DIR", "VAR_WORKSPACE_PATH"} {
		if dir := strings.TrimSpace(os.Getenv(key)); dir != "" {
			return filepath.Join(dir, "tool_output_folder")
		}
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		return filepath.Join(cwd, "tool_output_folder")
	}
	return ""
}

func persistBridgeToolResult(outputDir, toolName, value string) (string, error) {
	if strings.TrimSpace(outputDir) == "" {
		return "", fmt.Errorf("tool output directory is unavailable")
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", fmt.Errorf("create tool output directory: %w", err)
	}
	extension := ".txt"
	if json.Valid([]byte(value)) {
		extension = ".json"
	}
	prefix := "tool_" + safeBridgeFilenamePart(toolName) + "_"
	file, err := os.CreateTemp(outputDir, prefix+"*"+extension)
	if err != nil {
		return "", fmt.Errorf("create tool output file: %w", err)
	}
	path := file.Name()
	removeOnError := true
	defer func() {
		_ = file.Close()
		if removeOnError {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure tool output file: %w", err)
	}
	if _, err := file.WriteString(value); err != nil {
		return "", fmt.Errorf("write tool output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close tool output file: %w", err)
	}
	removeOnError = false
	return path, nil
}

func safeBridgeFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "tool"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

func prepareBridgeToolResult(toolName, value, outputDir string) (bounded, savedPath string, truncated bool, saveErr error) {
	if len(value) <= maxBridgeToolResultBytes {
		return value, "", false, nil
	}
	savedPath, saveErr = persistBridgeToolResult(outputDir, toolName, value)
	bounded, truncated = truncateBridgeToolResultText(toolName, value, savedPath)
	return bounded, savedPath, truncated, saveErr
}

func bridgeRequestError(toolType, toolName, sessionID string, timeout time.Duration, err error) string {
	layer := "mcpbridge_http"
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Sprintf("CANCELED: layer=%s type=%s tool=%s session=%s: %v", layer, toolType, toolName, sessionID, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("TIMEOUT: layer=%s type=%s tool=%s session=%s timeout=%s: %v", layer, toolType, toolName, sessionID, timeout, err)
	default:
		return fmt.Sprintf("ERROR: layer=%s type=%s tool=%s session=%s: HTTP request failed: %v", layer, toolType, toolName, sessionID, err)
	}
}

func main() {
	// If MCP_BRIDGE_LOG is set, tee all log output to that file in addition to stderr.
	// This lets the Go server capture mcpbridge startup/crash messages for debugging.
	if logPath := os.Getenv("MCP_BRIDGE_LOG"); logPath != "" {
		//nolint:gosec // MCP_BRIDGE_LOG is set by the parent process to a trusted log file path.
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}

	apiURL := os.Getenv("MCP_API_URL")
	apiToken := os.Getenv("MCP_API_TOKEN")
	toolsJSON := os.Getenv("MCP_TOOLS")
	virtualScopeID := os.Getenv("MCP_VIRTUAL_SCOPE_ID") // Per-agent scope for virtual tools (prevents parent/child overwrite)
	sessionID := os.Getenv("MCP_SESSION_ID")
	toolOutputDir := bridgeToolOutputDir()

	if apiURL == "" || apiToken == "" || toolsJSON == "" {
		log.Fatal("MCP_API_URL, MCP_API_TOKEN, and MCP_TOOLS env vars are required")
	}

	var toolDefs []ToolDef
	if err := json.Unmarshal([]byte(toolsJSON), &toolDefs); err != nil {
		log.Fatalf("Failed to parse MCP_TOOLS: %v", err)
	}

	// Signal readiness the moment the CLI finishes its tools/list handshake, so
	// the parent adapter can hold a cold session's first prompt until the tools
	// are actually connected. tools/list is the precise "tools are now callable"
	// signal; initialize is used as an earlier belt-and-suspenders fallback.
	markReady := makeReadyFileWriter(os.Getenv("MCP_READY_FILE"))
	hooks := &server.Hooks{}
	hooks.AddAfterListTools(func(_ context.Context, _ any, _ *mcp.ListToolsRequest, _ *mcp.ListToolsResult) {
		markReady()
	})

	s := server.NewMCPServer("mcpbridge", "1.0.0",
		server.WithToolCapabilities(false),
		server.WithHooks(hooks),
	)

	defaultHTTPClient := &http.Client{Timeout: codingtimeout.DefaultBridgeHTTPTimeout}
	longRunningTimeout := codingtimeout.LongRunningMCPToolTimeout()
	longRunningHTTPClient := &http.Client{Timeout: longRunningTimeout}

	for _, td := range toolDefs {
		def := td // capture loop variable

		// Use empty JSON object schema if none provided
		inputSchema := def.InputSchema
		if len(inputSchema) == 0 {
			inputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}

		mcpTool := mcp.NewToolWithRawSchema(def.Name, def.Description, inputSchema)

		s.AddTool(mcpTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Build endpoint URL based on tool type
			var url string
			switch def.Type {
			case "mcp":
				url = fmt.Sprintf("%s/tools/mcp/%s/%s", apiURL, def.Server, def.Name)
			case "virtual":
				url = fmt.Sprintf("%s/tools/virtual/%s", apiURL, def.Name)
			default:
				url = fmt.Sprintf("%s/tools/custom/%s", apiURL, def.Name)
			}

			// Marshal arguments
			args := req.GetArguments()
			argsJSON, err := json.Marshal(args)
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: failed to marshal arguments: %v", err)), nil
			}
			// Make HTTP POST request
			httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(argsJSON)))
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: failed to create request: %v", err)), nil
			}
			httpReq.Header.Set("Authorization", "Bearer "+apiToken)
			httpReq.Header.Set("Content-Type", "application/json")
			if def.Type == "virtual" && virtualScopeID != "" {
				httpReq.Header.Set("X-Virtual-Scope-ID", virtualScopeID)
			}
			if sessionID != "" {
				httpReq.Header.Set("X-Session-ID", sessionID)
			}

			httpClient := defaultHTTPClient
			if isLongRunningDelegationTool(def.Type, def.Name) {
				httpClient = longRunningHTTPClient
			}

			started := time.Now()
			log.Printf("mcpbridge: tool call start type=%s tool=%s url=%s args_bytes=%d session=%s", def.Type, def.Name, url, len(argsJSON), sessionID)
			resp, err := httpClient.Do(httpReq)
			if err != nil {
				log.Printf("mcpbridge: tool call http error type=%s tool=%s duration=%s error=%v", def.Type, def.Name, time.Since(started), err)
				return mcp.NewToolResultText(bridgeRequestError(def.Type, def.Name, sessionID, httpClient.Timeout, err)), nil
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("mcpbridge: tool call read error type=%s tool=%s status=%d duration=%s error=%v", def.Type, def.Name, resp.StatusCode, time.Since(started), err)
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: failed to read response: %v", err)), nil
			}
			log.Printf("mcpbridge: tool call response type=%s tool=%s status=%d duration=%s body_bytes=%d", def.Type, def.Name, resp.StatusCode, time.Since(started), len(body))

			if resp.StatusCode >= 400 {
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: HTTP %d: %s", resp.StatusCode, truncateBridgeErrorText(string(body)))), nil
			}

			var result struct {
				Success bool   `json:"success"`
				Result  string `json:"result"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(body, &result); err != nil {
				// If response isn't our expected format, return raw body
				bounded, savedPath, truncated, saveErr := prepareBridgeToolResult(def.Name, string(body), toolOutputDir)
				if truncated {
					log.Printf("mcpbridge: truncated raw tool result type=%s tool=%s original_bytes=%d returned_bytes=%d saved_path=%q save_error=%v", def.Type, def.Name, len(body), len(bounded), savedPath, saveErr)
				}
				return mcp.NewToolResultText(bounded), nil
			}

			if !result.Success {
				// Return error as regular text (not IsError) so the LLM always sees the
				// actual error details. Some CLI providers (for example Claude Code) show
				// only a generic "MCP tool reported an error" when IsError=true, hiding
				// the actual error content from the LLM.
				errorMsg := result.Error
				if errorMsg == "" {
					errorMsg = "unknown error (no details in response)"
				}
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: %s", truncateBridgeErrorText(errorMsg))), nil
			}
			bounded, savedPath, truncated, saveErr := prepareBridgeToolResult(def.Name, result.Result, toolOutputDir)
			if truncated {
				log.Printf("mcpbridge: truncated tool result type=%s tool=%s original_bytes=%d returned_bytes=%d saved_path=%q save_error=%v", def.Type, def.Name, len(result.Result), len(bounded), savedPath, saveErr)
			}
			return mcp.NewToolResultText(bounded), nil
		})
	}

	log.Printf("mcpbridge: starting with %d tools, API URL: %s, default_http_timeout=%s, long_running_http_timeout=%s", len(toolDefs), apiURL, defaultHTTPClient.Timeout, longRunningTimeout)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("mcpbridge: stdio server error: %v", err)
	}
}
