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
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

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

	if apiURL == "" || apiToken == "" || toolsJSON == "" {
		log.Fatal("MCP_API_URL, MCP_API_TOKEN, and MCP_TOOLS env vars are required")
	}

	var toolDefs []ToolDef
	if err := json.Unmarshal([]byte(toolsJSON), &toolDefs); err != nil {
		log.Fatalf("Failed to parse MCP_TOOLS: %v", err)
	}

	s := server.NewMCPServer("mcpbridge", "1.0.0",
		server.WithToolCapabilities(false),
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
			diffBytes := -1
			filepathArg := ""
			if def.Name == "diff_patch_workspace_file" {
				if v, ok := args["diff"].(string); ok {
					diffBytes = len(v)
				}
				if v, ok := args["filepath"].(string); ok {
					filepathArg = v
				}
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
			log.Printf("mcpbridge: tool call start type=%s tool=%s url=%s args_bytes=%d diff_bytes=%d filepath=%q session=%s", def.Type, def.Name, url, len(argsJSON), diffBytes, filepathArg, sessionID)
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
				return mcp.NewToolResultText(string(body)), nil
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
			return mcp.NewToolResultText(result.Result), nil
		})
	}

	log.Printf("mcpbridge: starting with %d tools, API URL: %s, default_http_timeout=%s, long_running_http_timeout=%s", len(toolDefs), apiURL, defaultHTTPClient.Timeout, longRunningTimeout)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("mcpbridge: stdio server error: %v", err)
	}
}
