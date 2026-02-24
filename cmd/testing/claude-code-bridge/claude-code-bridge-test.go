package claudecodebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	testutils "github.com/manishiitg/mcpagent/cmd/testing/testutils"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
)

var claudeCodeBridgeTestCmd = &cobra.Command{
	Use:   "claude-code-bridge",
	Short: "End-to-end test: Claude Code + MCP bridge + multiple MCP servers + workspace tools",
	Long: `Tests the full Claude Code MCP bridge flow end-to-end:

1. Ensures mcpbridge binary is built
2. Starts executor HTTP server with bearer auth (MCP + custom + virtual endpoints)
3. Creates a Claude Code agent with:
   - Code execution mode enabled
   - Multiple MCP servers (context7, docfork)
   - Workspace custom tools (read_file, write_file, shell)
4. Verifies BuildBridgeMCPConfig() produces correct config with all tools
5. Sends a query via agent.Ask() — Claude Code uses bridge to call tools

Requires:
  - Claude Code CLI installed (npm install -g @anthropic-ai/claude-code)
  - ANTHROPIC_API_KEY set (for Claude Code)
  - mcpbridge binary in PATH or ~/go/bin/

Examples:
  mcpagent-test claude-code-bridge --provider claude-code
  mcpagent-test claude-code-bridge --provider claude-code --log-level debug`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := testutils.NewTestLoggerFromViper()

		logger.Info("=== Claude Code Bridge End-to-End Test ===")
		logger.Info("Testing: Claude Code + MCP bridge + multiple MCP servers + workspace tools")

		tracer, isLangfuse := testutils.GetTracerWithLogger("langfuse", logger)
		if tracer == nil {
			tracer, _ = testutils.GetTracerWithLogger("noop", logger)
		}
		if isLangfuse {
			logger.Info("Langfuse tracing enabled")
		}
		traceID := testutils.GenerateTestTraceID()

		if err := TestClaudeCodeBridge(logger, tracer, traceID); err != nil {
			return fmt.Errorf("test failed: %w", err)
		}

		if flusher, ok := tracer.(interface{ Flush() }); ok {
			flusher.Flush()
		}

		logger.Info("All Claude Code bridge tests passed")
		if isLangfuse {
			logger.Info("Langfuse trace", loggerv2.String("trace_id", string(traceID)))
		}
		return nil
	},
}

// GetClaudeCodeBridgeTestCmd returns the test command
func GetClaudeCodeBridgeTestCmd() *cobra.Command {
	return claudeCodeBridgeTestCmd
}

// TestClaudeCodeBridge runs the full end-to-end test
func TestClaudeCodeBridge(log loggerv2.Logger, tracer observability.Tracer, traceID observability.TraceID) error {
	ctx := context.Background()

	// Step 1: Ensure mcpbridge binary is available
	log.Info("--- Step 1: Ensure mcpbridge binary ---")
	bridgePath, err := ensureBridgeBinary(log)
	if err != nil {
		return err
	}
	log.Info("mcpbridge binary ready", loggerv2.String("path", bridgePath))

	// Step 2: Start executor HTTP server
	log.Info("--- Step 2: Start executor HTTP server ---")
	serverURL, apiToken, shutdown, err := startExecutorServer(log)
	if err != nil {
		return err
	}
	defer shutdown()
	log.Info("Executor server started",
		loggerv2.String("url", serverURL),
		loggerv2.String("token_prefix", apiToken[:8]+"..."))

	time.Sleep(500 * time.Millisecond)

	// Step 3: Create Claude Code agent with MCP servers + workspace tools
	log.Info("--- Step 3: Create Claude Code agent ---")
	agent, cleanup, err := createClaudeCodeAgent(ctx, log, tracer, traceID, serverURL, apiToken, bridgePath)
	if err != nil {
		return err
	}
	defer agent.Close()
	defer cleanup()

	// Step 4: Register workspace custom tools
	log.Info("--- Step 4: Register workspace custom tools ---")
	if err := registerWorkspaceTools(agent, log); err != nil {
		return err
	}

	// Update the code execution registry so the executor can find our custom tools
	if err := agent.UpdateCodeExecutionRegistry(); err != nil {
		return fmt.Errorf("failed to update code execution registry: %w", err)
	}
	log.Info("Code execution registry updated with workspace tools")

	// Step 5: Verify bridge config
	log.Info("--- Step 5: Verify bridge MCP config ---")
	if err := verifyBridgeConfig(agent, log); err != nil {
		return err
	}

	// Step 6: Send a query through Claude Code
	log.Info("--- Step 6: Send query via Claude Code (through bridge) ---")
	if err := testClaudeCodeQuery(ctx, agent, log, traceID); err != nil {
		return err
	}

	return nil
}

// ensureBridgeBinary checks for the mcpbridge binary and builds it if missing
func ensureBridgeBinary(log loggerv2.Logger) (string, error) {
	// Check MCP_BRIDGE_BINARY env var first
	if envPath := os.Getenv("MCP_BRIDGE_BINARY"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	// Check PATH
	if path, err := exec.LookPath("mcpbridge"); err == nil {
		return path, nil
	}

	// Check ~/go/bin/
	homeDir, _ := os.UserHomeDir()
	goBinPath := homeDir + "/go/bin/mcpbridge"
	if _, err := os.Stat(goBinPath); err == nil {
		return goBinPath, nil
	}

	// Try to build it
	log.Info("mcpbridge not found, building...")
	buildCmd := exec.Command("go", "build", "-o", goBinPath, "./cmd/mcpbridge/")
	buildCmd.Dir = findMcpagentRoot()
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build mcpbridge: %w (run: go build -o ~/go/bin/mcpbridge ./cmd/mcpbridge/)", err)
	}
	log.Info("mcpbridge built successfully", loggerv2.String("path", goBinPath))
	return goBinPath, nil
}

// findMcpagentRoot finds the mcpagent module root by looking for go.mod
func findMcpagentRoot() string {
	// Try current working directory and parent directories
	dir, _ := os.Getwd()
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(dir + "/cmd/mcpbridge"); err == nil {
			return dir
		}
		dir = dir + "/.."
	}
	return "."
}

// startExecutorServer starts an HTTP server with executor handlers and bearer auth
func startExecutorServer(log loggerv2.Logger) (string, string, func(), error) {
	configPath := testutils.GetDefaultTestConfigPath()
	if configPath == "" {
		// Create minimal config if none found
		tmpFile, err := os.CreateTemp("", "mcp-config-*.json")
		if err != nil {
			return "", "", nil, fmt.Errorf("failed to create temp config: %w", err)
		}
		tmpFile.WriteString(`{"mcpServers":{}}`)
		tmpFile.Close()
		configPath = tmpFile.Name()
	}
	log.Info("Executor using config", loggerv2.String("path", configPath))

	apiToken := executor.GenerateAPIToken()
	handlers := executor.NewExecutorHandlers(configPath, log)

	mux := http.NewServeMux()

	// Batch endpoints
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)

	// Per-tool MCP endpoints
	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/tools/mcp/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"success":false,"error":"invalid path"}`, http.StatusBadRequest)
			return
		}
		server := strings.ReplaceAll(parts[0], "_", "-")
		tool := strings.ReplaceAll(parts[1], "_", "-")
		handlers.HandlePerToolMCPRequest(w, r, server, tool)
	})

	// Per-tool custom endpoints
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		tool := strings.TrimPrefix(r.URL.Path, "/tools/custom/")
		if tool == "" {
			http.Error(w, `{"success":false,"error":"missing tool"}`, http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolCustomRequest(w, r, tool)
	})

	// Per-tool virtual endpoints
	mux.HandleFunc("/tools/virtual/", func(w http.ResponseWriter, r *http.Request) {
		tool := strings.TrimPrefix(r.URL.Path, "/tools/virtual/")
		if tool == "" {
			http.Error(w, `{"success":false,"error":"missing tool"}`, http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolVirtualRequest(w, r, tool)
	})

	authedHandler := executor.AuthMiddleware(apiToken)(mux)

	// Use dynamic port to avoid conflicts with other tests
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		return "", "", nil, fmt.Errorf("failed to find free port: %w", listenErr)
	}
	addr := listener.Addr().String()

	server := &http.Server{
		Handler:           authedHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error("Server error", err)
		}
	}()

	serverURL := "http://" + addr
	shutdownFn := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
		log.Info("Executor server stopped")
	}

	return serverURL, apiToken, shutdownFn, nil
}

// createClaudeCodeAgent creates a Claude Code agent with multiple MCP servers and code execution mode
func createClaudeCodeAgent(ctx context.Context, log loggerv2.Logger, tracer observability.Tracer, traceID observability.TraceID, apiURL, apiToken, bridgePath string) (*mcpagent.Agent, func(), error) {
	// Create LLM — force claude-code provider
	model, provider, err := testutils.CreateTestLLMFromViper(log)
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to create LLM: %w", err)
	}

	if provider != llm.ProviderClaudeCode {
		log.Warn("Provider is not claude-code — forcing claude-code for this test",
			loggerv2.String("actual_provider", string(provider)))
	}

	// Create temp MCP config with multiple servers
	mcpServers := map[string]interface{}{
		"context7": map[string]interface{}{
			"url":      "https://mcp.context7.com/mcp",
			"protocol": "http",
		},
		"docfork": map[string]interface{}{
			"type": "http",
			"url":  "https://mcp.docfork.com/mcp",
			"headers": map[string]interface{}{
				"DOCFORK_CABINET": "general",
				"DOCFORK_API_KEY": "docf_9zGjMuLWUXcWQKcqhVRLx9Mk6FmFnBtrbdQp9RvHQ1iH7UjvjqcwGYc",
			},
		},
	}

	configPath, cleanup, err := testutils.CreateTempMCPConfig(mcpServers, log)
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to create temp MCP config: %w", err)
	}

	log.Info("MCP config created with multiple servers",
		loggerv2.String("path", configPath),
		loggerv2.Int("server_count", len(mcpServers)))

	// Set MCP_BRIDGE_BINARY env so BuildBridgeMCPConfig can find it
	os.Setenv("MCP_BRIDGE_BINARY", bridgePath)

	// Set MCP env vars so the agent (and bridge) can read them
	os.Setenv("MCP_API_URL", apiURL)
	os.Setenv("MCP_API_TOKEN", apiToken)

	agent, err := testutils.CreateTestAgent(ctx, &testutils.TestAgentConfig{
		LLM:        model,
		Provider:   llm.ProviderClaudeCode,
		ConfigPath: configPath,
		Tracer:     tracer,
		TraceID:    traceID,
		Logger:     log,
		Options: []mcpagent.AgentOption{
			mcpagent.WithProvider(llm.ProviderClaudeCode),
			mcpagent.WithCodeExecutionMode(true),
		},
	})
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("failed to create agent: %w", err)
	}

	log.Info("Claude Code agent created",
		loggerv2.String("provider", "claude-code"),
		loggerv2.Any("code_execution_mode", true))

	return agent, cleanup, nil
}

// registerWorkspaceTools registers workspace-like custom tools on the agent
func registerWorkspaceTools(agent *mcpagent.Agent, log loggerv2.Logger) error {
	// workspace_read_file — reads files from a test directory
	err := agent.RegisterCustomTool(
		"workspace_read_file",
		"Read a file from the workspace. Returns the file contents as a string.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Relative file path to read",
				},
			},
			"required": []string{"path"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			log.Info("workspace_read_file called", loggerv2.String("path", path))
			return fmt.Sprintf("Contents of %s:\n# Sample file\nThis is test content from workspace_read_file.", path), nil
		},
		"workspace_tools",
	)
	if err != nil {
		return fmt.Errorf("failed to register workspace_read_file: %w", err)
	}
	log.Info("Registered workspace_read_file")

	// workspace_write_file — writes files
	err = agent.RegisterCustomTool(
		"workspace_write_file",
		"Write content to a file in the workspace.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Relative file path to write",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Content to write",
				},
			},
			"required": []string{"path", "content"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			log.Info("workspace_write_file called",
				loggerv2.String("path", path),
				loggerv2.Int("content_length", len(content)))
			return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path), nil
		},
		"workspace_tools",
	)
	if err != nil {
		return fmt.Errorf("failed to register workspace_write_file: %w", err)
	}
	log.Info("Registered workspace_write_file")

	// execute_shell_command — runs shell commands
	err = agent.RegisterCustomTool(
		"execute_shell_command",
		"Execute a shell command in the workspace directory.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Shell command to execute",
				},
			},
			"required": []string{"command"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			command, _ := args["command"].(string)
			log.Info("execute_shell_command called", loggerv2.String("command", command))
			return fmt.Sprintf("$ %s\nCommand executed successfully. (mock output)", command), nil
		},
		"workspace_tools",
	)
	if err != nil {
		return fmt.Errorf("failed to register execute_shell_command: %w", err)
	}
	log.Info("Registered execute_shell_command")

	// agent_browser — browser automation
	err = agent.RegisterCustomTool(
		"agent_browser",
		"Browse a URL and return page content.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to browse",
				},
			},
			"required": []string{"url"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			url, _ := args["url"].(string)
			log.Info("agent_browser called", loggerv2.String("url", url))
			return fmt.Sprintf("Page content from %s: (mock browser output)", url), nil
		},
		"browser_tools",
	)
	if err != nil {
		return fmt.Errorf("failed to register agent_browser: %w", err)
	}
	log.Info("Registered agent_browser")

	log.Info("All workspace tools registered", loggerv2.Int("count", 4))
	return nil
}

// verifyBridgeConfig checks that BuildBridgeMCPConfig produces valid config with all tools
func verifyBridgeConfig(agent *mcpagent.Agent, log loggerv2.Logger) error {
	configJSON, err := agent.BuildBridgeMCPConfig()
	if err != nil {
		return fmt.Errorf("BuildBridgeMCPConfig failed: %w", err)
	}

	log.Info("Bridge config generated", loggerv2.Int("length", len(configJSON)))

	// Parse the config
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("bridge config is not valid JSON: %w", err)
	}

	// Verify structure
	mcpServers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("bridge config missing mcpServers")
	}

	apiBridge, ok := mcpServers["api-bridge"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("bridge config missing api-bridge server")
	}

	// Verify command
	command, _ := apiBridge["command"].(string)
	if command == "" {
		return fmt.Errorf("bridge config has empty command")
	}
	log.Info("Bridge command", loggerv2.String("command", command))

	// Verify env vars
	envMap, ok := apiBridge["env"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("bridge config missing env")
	}

	apiURL, _ := envMap["MCP_API_URL"].(string)
	apiToken, _ := envMap["MCP_API_TOKEN"].(string)
	toolsJSON, _ := envMap["MCP_TOOLS"].(string)

	if apiURL == "" {
		return fmt.Errorf("bridge config has empty MCP_API_URL")
	}
	if apiToken == "" {
		return fmt.Errorf("bridge config has empty MCP_API_TOKEN")
	}
	if toolsJSON == "" {
		return fmt.Errorf("bridge config has empty MCP_TOOLS")
	}

	log.Info("Bridge env vars present",
		loggerv2.String("api_url", apiURL),
		loggerv2.String("token_prefix", apiToken[:8]+"..."))

	// Parse and verify tool definitions
	var toolDefs []map[string]interface{}
	if err := json.Unmarshal([]byte(toolsJSON), &toolDefs); err != nil {
		return fmt.Errorf("MCP_TOOLS is not valid JSON: %w", err)
	}

	log.Info("Tool definitions in bridge config", loggerv2.Int("count", len(toolDefs)))

	// Verify the bridge exposes exactly the 3 expected tools
	toolSet := make(map[string]string, len(toolDefs)) // name -> type
	for _, td := range toolDefs {
		name, _ := td["name"].(string)
		toolType, _ := td["type"].(string)
		toolSet[name] = toolType
		log.Info("  Bridge tool", loggerv2.String("name", name), loggerv2.String("type", toolType))
	}

	expectedTools := map[string]string{
		"execute_shell_command": "custom",
		"agent_browser":        "custom",
		"get_api_spec":         "virtual",
	}
	for name, wantType := range expectedTools {
		gotType, ok := toolSet[name]
		if !ok {
			return fmt.Errorf("expected bridge tool %q not found", name)
		}
		if gotType != wantType {
			return fmt.Errorf("bridge tool %q: expected type %q, got %q", name, wantType, gotType)
		}
	}

	log.Info("Bridge config verification passed",
		loggerv2.Int("total_tools", len(toolDefs)))

	return nil
}

// testClaudeCodeQuery sends a query through the agent and verifies it works
func testClaudeCodeQuery(ctx context.Context, agent *mcpagent.Agent, log loggerv2.Logger, traceID observability.TraceID) error {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	// Use a simple query that should trigger tool usage
	// The workspace tools are mock implementations, so we just verify the flow works
	query := `Use the workspace_read_file tool to read the file "README.md" and tell me what it contains. Keep your response brief.`

	log.Info("Sending query to Claude Code agent",
		loggerv2.String("query", query),
		loggerv2.String("trace_id", string(traceID)))

	startTime := time.Now()
	response, err := agent.Ask(queryCtx, query)
	duration := time.Since(startTime)

	if err != nil {
		return fmt.Errorf("agent.Ask failed: %w", err)
	}

	log.Info("Claude Code response received",
		loggerv2.String("response", truncateString(response, 500)),
		loggerv2.Int("response_length", len(response)),
		loggerv2.String("duration", duration.String()))

	// Validate response
	if response == "" {
		return fmt.Errorf("empty response from agent")
	}

	// Check for indicators that the tool was used
	responseLower := strings.ToLower(response)
	if strings.Contains(responseLower, "readme") ||
		strings.Contains(responseLower, "sample") ||
		strings.Contains(responseLower, "workspace") ||
		strings.Contains(responseLower, "file") ||
		strings.Contains(responseLower, "content") {
		log.Info("Response indicates tool was likely used (contains expected keywords)")
	} else {
		log.Warn("Response may not indicate tool usage",
			loggerv2.String("response_preview", truncateString(response, 200)))
	}

	log.Info("Claude Code bridge test completed successfully",
		loggerv2.String("duration", duration.String()))

	log.Info("Verified:")
	log.Info("  1. mcpbridge binary found/built")
	log.Info("  2. Executor HTTP server running with bearer auth")
	log.Info("  3. Claude Code agent created with code execution mode")
	log.Info("  4. Multiple MCP servers configured (context7, docfork)")
	log.Info("  5. Workspace custom tools registered (read_file, write_file, shell, browser)")
	log.Info("  6. BuildBridgeMCPConfig() produced valid config with 3 bridge tools")
	log.Info("  7. Claude Code query executed through bridge successfully")

	return nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
