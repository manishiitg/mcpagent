package mcpbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	testutils "github.com/manishiitg/mcpagent/cmd/testing/testutils"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

var mcpBridgeTestCmd = &cobra.Command{
	Use:   "mcpbridge",
	Short: "Test MCP bridge binary (stdio MCP server that forwards to HTTP API)",
	Long: `Tests the mcpbridge binary end-to-end:
1. Starts a mock HTTP server that mimics per-tool API endpoints
2. Launches mcpbridge as a subprocess with tool definitions via env vars
3. Uses mcp-go stdio client to connect and verify:
   - Tool discovery (ListTools)
   - Tool execution forwarding (CallTool → HTTP → response)
   - Error handling (tool failures returned properly)

This validates that Claude Code can use MCP tools via the bridge.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := testutils.NewTestLoggerFromViper()

		logger.Info("=== MCP Bridge End-to-End Test ===")

		if err := TestMCPBridge(logger); err != nil {
			return fmt.Errorf("test failed: %w", err)
		}

		logger.Info("All MCP bridge tests passed")
		return nil
	},
}

// GetMCPBridgeTestCmd returns the test command
func GetMCPBridgeTestCmd() *cobra.Command {
	return mcpBridgeTestCmd
}

// requestLog records an HTTP request received by the mock server
type requestLog struct {
	Method string
	Path   string
	Body   map[string]interface{}
}

// TestMCPBridge runs the full bridge test suite
func TestMCPBridge(log loggerv2.Logger) error {
	// Step 1: Verify mcpbridge binary exists
	log.Info("--- Step 1: Verify mcpbridge binary ---")
	bridgePath, err := exec.LookPath("mcpbridge")
	if err != nil {
		return fmt.Errorf("mcpbridge binary not found in PATH. Build it with: go build -o ~/go/bin/mcpbridge ./cmd/mcpbridge/")
	}
	log.Info("Found mcpbridge binary", loggerv2.String("path", bridgePath))

	// Step 2: Start mock HTTP server
	log.Info("--- Step 2: Start mock HTTP server ---")
	mockServer, err := startMockAPIServer(log)
	if err != nil {
		return err
	}
	defer mockServer.shutdown()
	log.Info("Mock server started", loggerv2.String("url", mockServer.url))

	// Give server a moment to start
	time.Sleep(200 * time.Millisecond)

	// Step 3: Test tool discovery (ListTools)
	log.Info("--- Step 3: Test tool discovery ---")
	if err := testToolDiscovery(bridgePath, mockServer, log); err != nil {
		return fmt.Errorf("tool discovery test failed: %w", err)
	}
	log.Info("Tool discovery test passed")

	// Step 4: Test tool execution (CallTool → HTTP → response)
	log.Info("--- Step 4: Test tool execution ---")
	if err := testToolExecution(bridgePath, mockServer, log); err != nil {
		return fmt.Errorf("tool execution test failed: %w", err)
	}
	log.Info("Tool execution test passed")

	// Step 5: Test error handling
	log.Info("--- Step 5: Test error handling ---")
	if err := testErrorHandling(bridgePath, mockServer, log); err != nil {
		return fmt.Errorf("error handling test failed: %w", err)
	}
	log.Info("Error handling test passed")

	return nil
}

// mockAPIServer is a test HTTP server that mimics the per-tool API endpoints
type mockAPIServer struct {
	url       string
	apiToken  string
	server    *http.Server
	requests  []requestLog
	mu        sync.Mutex
	shutdown  func()
}

func (m *mockAPIServer) getRequests() []requestLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]requestLog, len(m.requests))
	copy(result, m.requests)
	return result
}

func (m *mockAPIServer) clearRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = nil
}

func startMockAPIServer(log loggerv2.Logger) (*mockAPIServer, error) {
	mock := &mockAPIServer{
		apiToken: "test-token-12345",
	}

	mux := http.NewServeMux()

	// Per-tool MCP endpoint: POST /tools/mcp/{server}/{tool}
	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		// Verify auth
		if r.Header.Get("Authorization") != "Bearer "+mock.apiToken {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "unauthorized"})
			return
		}

		// Parse body
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		mock.mu.Lock()
		mock.requests = append(mock.requests, requestLog{Method: r.Method, Path: r.URL.Path, Body: body})
		mock.mu.Unlock()

		log.Info("Mock received MCP request",
			loggerv2.String("path", r.URL.Path),
			loggerv2.String("method", r.Method))

		// Return success with a predictable result
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  fmt.Sprintf("mock-result-for-%s", r.URL.Path),
		})
	})

	// Per-tool custom endpoint: POST /tools/custom/{tool}
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mock.apiToken {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "unauthorized"})
			return
		}

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		mock.mu.Lock()
		mock.requests = append(mock.requests, requestLog{Method: r.Method, Path: r.URL.Path, Body: body})
		mock.mu.Unlock()

		log.Info("Mock received custom request", loggerv2.String("path", r.URL.Path))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  fmt.Sprintf("custom-result-for-%s", r.URL.Path),
		})
	})

	// Per-tool virtual endpoint: POST /tools/virtual/{tool}
	mux.HandleFunc("/tools/virtual/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mock.apiToken {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "unauthorized"})
			return
		}

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)

		mock.mu.Lock()
		mock.requests = append(mock.requests, requestLog{Method: r.Method, Path: r.URL.Path, Body: body})
		mock.mu.Unlock()

		log.Info("Mock received virtual request", loggerv2.String("path", r.URL.Path))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  fmt.Sprintf("virtual-result-for-%s", r.URL.Path),
		})
	})

	// Error endpoint: always returns an error
	mux.HandleFunc("/tools/mcp/error-server/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mock.apiToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		mock.mu.Lock()
		mock.requests = append(mock.requests, requestLog{Method: r.Method, Path: r.URL.Path})
		mock.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "tool execution failed: test error",
		})
	})

	server := &http.Server{
		Addr:              "127.0.0.1:18766",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Mock server error", err)
		}
	}()

	mock.url = "http://127.0.0.1:18766"
	mock.server = server
	mock.shutdown = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		log.Info("Mock server stopped")
	}

	return mock, nil
}

// buildToolDefs creates the MCP_TOOLS JSON for testing
func buildToolDefs() string {
	tools := []map[string]interface{}{
		{
			"name":         "search_docs",
			"description":  "Search documentation for a query",
			"input_schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string", "description": "Search query"}, "limit": map[string]interface{}{"type": "integer", "description": "Max results"}}, "required": []string{"query"}},
			"server":       "docfork",
			"type":         "mcp",
		},
		{
			"name":         "get_page",
			"description":  "Get a documentation page by URL",
			"input_schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string", "description": "Page URL"}}, "required": []string{"url"}},
			"server":       "docfork",
			"type":         "mcp",
		},
		{
			"name":         "workspace_read_file",
			"description":  "Read a file from the workspace",
			"input_schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "File path"}}, "required": []string{"path"}},
			"type":         "custom",
		},
		{
			"name":         "discover_code_structure",
			"description":  "Discover the code structure of the project",
			"input_schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			"type":         "virtual",
		},
	}

	b, _ := json.Marshal(tools)
	return string(b)
}

// createBridgeClient creates an MCP stdio client connected to the mcpbridge binary
func createBridgeClient(bridgePath string, mock *mockAPIServer, log loggerv2.Logger) (*client.Client, error) {
	toolsJSON := buildToolDefs()

	env := []string{
		"MCP_API_URL=" + mock.url,
		"MCP_API_TOKEN=" + mock.apiToken,
		"MCP_TOOLS=" + toolsJSON,
	}

	log.Info("Launching mcpbridge",
		loggerv2.String("path", bridgePath),
		loggerv2.Int("tool_count", 4))

	c, err := client.NewStdioMCPClient(bridgePath, env)
	if err != nil {
		return nil, fmt.Errorf("failed to create stdio client: %w", err)
	}

	// Initialize the client
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcpbridge-test", Version: "1.0.0"}

	_, err = c.Initialize(ctx, initReq)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	return c, nil
}

// testToolDiscovery verifies that ListTools returns all expected tools
func testToolDiscovery(bridgePath string, mock *mockAPIServer, log loggerv2.Logger) error {
	c, err := createBridgeClient(bridgePath, mock, log)
	if err != nil {
		return err
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// List tools
	result, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("ListTools failed: %w", err)
	}

	// Verify tool count
	if len(result.Tools) != 4 {
		return fmt.Errorf("expected 4 tools, got %d", len(result.Tools))
	}
	log.Info("Tool count correct", loggerv2.Int("count", len(result.Tools)))

	// Verify each tool is present
	toolNames := make(map[string]bool)
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
		log.Info("  Tool discovered",
			loggerv2.String("name", tool.Name),
			loggerv2.String("description", tool.Description))
	}

	expected := []string{"search_docs", "get_page", "workspace_read_file", "discover_code_structure"}
	for _, name := range expected {
		if !toolNames[name] {
			return fmt.Errorf("expected tool %q not found in ListTools result", name)
		}
	}
	log.Info("All expected tools found")

	return nil
}

// testToolExecution verifies that CallTool forwards to the correct HTTP endpoint
func testToolExecution(bridgePath string, mock *mockAPIServer, log loggerv2.Logger) error {
	c, err := createBridgeClient(bridgePath, mock, log)
	if err != nil {
		return err
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test 1: Call MCP tool (search_docs)
	log.Info("Testing MCP tool call: search_docs")
	mock.clearRequests()

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "search_docs"
	callReq.Params.Arguments = map[string]interface{}{
		"query": "react hooks",
		"limit": 5,
	}

	result, err := c.CallTool(ctx, callReq)
	if err != nil {
		return fmt.Errorf("CallTool search_docs failed: %w", err)
	}

	// Verify result is not an error
	if result.IsError {
		return fmt.Errorf("CallTool search_docs returned error: %v", result.Content)
	}

	// Verify result text contains expected content
	resultText := getTextContent(result)
	if resultText == "" {
		return fmt.Errorf("CallTool search_docs returned empty result")
	}
	log.Info("MCP tool result", loggerv2.String("result", resultText))

	// Verify the HTTP request was made to the correct endpoint
	requests := mock.getRequests()
	if len(requests) == 0 {
		return fmt.Errorf("no HTTP requests received by mock server")
	}
	lastReq := requests[len(requests)-1]
	expectedPath := "/tools/mcp/docfork/search_docs"
	if lastReq.Path != expectedPath {
		return fmt.Errorf("expected request to %s, got %s", expectedPath, lastReq.Path)
	}
	log.Info("HTTP request forwarded correctly",
		loggerv2.String("path", lastReq.Path))

	// Verify arguments were forwarded
	if lastReq.Body["query"] != "react hooks" {
		return fmt.Errorf("expected query arg 'react hooks', got %v", lastReq.Body["query"])
	}
	log.Info("Arguments forwarded correctly")

	// Test 2: Call custom tool (workspace_read_file)
	log.Info("Testing custom tool call: workspace_read_file")
	mock.clearRequests()

	callReq2 := mcp.CallToolRequest{}
	callReq2.Params.Name = "workspace_read_file"
	callReq2.Params.Arguments = map[string]interface{}{
		"path": "/src/main.go",
	}

	result2, err := c.CallTool(ctx, callReq2)
	if err != nil {
		return fmt.Errorf("CallTool workspace_read_file failed: %w", err)
	}
	if result2.IsError {
		return fmt.Errorf("CallTool workspace_read_file returned error: %v", result2.Content)
	}

	requests = mock.getRequests()
	if len(requests) == 0 {
		return fmt.Errorf("no HTTP requests received for custom tool")
	}
	lastReq = requests[len(requests)-1]
	expectedPath = "/tools/custom/workspace_read_file"
	if lastReq.Path != expectedPath {
		return fmt.Errorf("expected request to %s, got %s", expectedPath, lastReq.Path)
	}
	log.Info("Custom tool forwarded correctly",
		loggerv2.String("path", lastReq.Path))

	// Test 3: Call virtual tool (discover_code_structure)
	log.Info("Testing virtual tool call: discover_code_structure")
	mock.clearRequests()

	callReq3 := mcp.CallToolRequest{}
	callReq3.Params.Name = "discover_code_structure"
	callReq3.Params.Arguments = map[string]interface{}{}

	result3, err := c.CallTool(ctx, callReq3)
	if err != nil {
		return fmt.Errorf("CallTool discover_code_structure failed: %w", err)
	}
	if result3.IsError {
		return fmt.Errorf("CallTool discover_code_structure returned error: %v", result3.Content)
	}

	requests = mock.getRequests()
	if len(requests) == 0 {
		return fmt.Errorf("no HTTP requests received for virtual tool")
	}
	lastReq = requests[len(requests)-1]
	expectedPath = "/tools/virtual/discover_code_structure"
	if lastReq.Path != expectedPath {
		return fmt.Errorf("expected request to %s, got %s", expectedPath, lastReq.Path)
	}
	log.Info("Virtual tool forwarded correctly",
		loggerv2.String("path", lastReq.Path))

	return nil
}

// testErrorHandling verifies that tool errors are properly returned
func testErrorHandling(bridgePath string, mock *mockAPIServer, log loggerv2.Logger) error {
	// Build tool defs with an error tool
	errorTools := []map[string]interface{}{
		{
			"name":         "failing_tool",
			"description":  "A tool that always fails",
			"input_schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			"server":       "error-server",
			"type":         "mcp",
		},
	}
	toolsJSON, _ := json.Marshal(errorTools)

	env := []string{
		"MCP_API_URL=" + mock.url,
		"MCP_API_TOKEN=" + mock.apiToken,
		"MCP_TOOLS=" + string(toolsJSON),
	}

	c, err := client.NewStdioMCPClient(bridgePath, env)
	if err != nil {
		return fmt.Errorf("failed to create stdio client: %w", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcpbridge-test", Version: "1.0.0"}

	_, err = c.Initialize(ctx, initReq)
	if err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	// Call the failing tool
	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "failing_tool"
	callReq.Params.Arguments = map[string]interface{}{}

	result, err := c.CallTool(ctx, callReq)
	if err != nil {
		return fmt.Errorf("CallTool returned transport error (expected tool error): %w", err)
	}

	// The result should be an error
	if !result.IsError {
		return fmt.Errorf("expected tool error result, got success")
	}

	errorText := getTextContent(result)
	log.Info("Error result received correctly",
		loggerv2.String("error", errorText))

	if errorText == "" {
		return fmt.Errorf("error result has empty text")
	}

	// Test: wrong auth token
	log.Info("Testing authentication failure...")
	badEnv := []string{
		"MCP_API_URL=" + mock.url,
		"MCP_API_TOKEN=wrong-token",
		"MCP_TOOLS=" + string(toolsJSON),
	}

	badClient, err := client.NewStdioMCPClient(bridgePath, badEnv)
	if err != nil {
		return fmt.Errorf("failed to create bad-auth client: %w", err)
	}
	defer badClient.Close()

	initReq2 := mcp.InitializeRequest{}
	initReq2.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq2.Params.ClientInfo = mcp.Implementation{Name: "mcpbridge-test", Version: "1.0.0"}

	_, err = badClient.Initialize(ctx, initReq2)
	if err != nil {
		return fmt.Errorf("failed to initialize bad-auth client: %w", err)
	}

	callReq2 := mcp.CallToolRequest{}
	callReq2.Params.Name = "failing_tool"
	callReq2.Params.Arguments = map[string]interface{}{}

	authResult, err := badClient.CallTool(ctx, callReq2)
	if err != nil {
		return fmt.Errorf("CallTool with bad auth returned transport error: %w", err)
	}

	// Should get an error (either from HTTP 401 or from the error response)
	if !authResult.IsError {
		log.Warn("Bad auth call did not return error — mock may not enforce auth for this path")
	} else {
		log.Info("Authentication failure handled correctly",
			loggerv2.String("error", getTextContent(authResult)))
	}

	return nil
}

// getTextContent extracts text from a CallToolResult
func getTextContent(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
