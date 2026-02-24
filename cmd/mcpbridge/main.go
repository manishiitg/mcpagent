// mcpbridge is a stdio MCP server that bridges tool calls to HTTP API endpoints.
// It receives tool definitions via the MCP_TOOLS env var and forwards each tool
// call to the appropriate per-tool HTTP endpoint with bearer token authentication.
//
// This binary is launched by Claude Code via --mcp-config as a subprocess.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

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

func main() {
	apiURL := os.Getenv("MCP_API_URL")
	apiToken := os.Getenv("MCP_API_TOKEN")
	toolsJSON := os.Getenv("MCP_TOOLS")

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

	httpClient := &http.Client{Timeout: 5 * time.Minute}

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
			argsJSON, err := json.Marshal(req.GetArguments())
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}

			// Make HTTP POST request
			httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(argsJSON)))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to create request: %v", err)), nil
			}
			httpReq.Header.Set("Authorization", "Bearer "+apiToken)
			httpReq.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(httpReq)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("HTTP request failed: %v", err)), nil
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to read response: %v", err)), nil
			}

			if resp.StatusCode >= 400 {
				return mcp.NewToolResultError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))), nil
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
				return mcp.NewToolResultError(result.Error), nil
			}
			return mcp.NewToolResultText(result.Result), nil
		})
	}

	log.Printf("mcpbridge: starting with %d tools, API URL: %s", len(toolDefs), apiURL)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("mcpbridge: stdio server error: %v", err)
	}
}
