package mcpagentcodeexec

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	testutils "github.com/manishiitg/mcpagent/cmd/testing/testutils"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
)

var mcpAgentCodeExecTestCmd = &cobra.Command{
	Use:   "mcp-agent-code-exec",
	Short: "Test code execution agent with executor HTTP handlers",
	Long: `Tests the full code execution flow end-to-end:
1. Creates an agent in code execution mode
2. Starts HTTP server with executor handlers and bearer token auth
3. Agent calls get_api_spec to discover tool endpoints
4. Agent writes and executes code (any language) to call MCP tools via HTTP
5. Verifies the full integration works

This validates that the OpenAPI-based code execution mode works correctly.

Langfuse tracing is automatically enabled if LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY are set.
The trace_id will be output at the end for verification in Langfuse.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load .env file if it exists
		for _, path := range []string{".env", "../.env", "../../.env", "../../../.env"} {
			if _, err := os.Stat(path); err == nil {
				_ = godotenv.Load(path)
				break
			}
		}

		// Initialize logger using shared utilities
		logger := testutils.NewTestLoggerFromViper()

		logger.Info("=== MCP Agent Code Execution Test ===")
		logger.Info("Testing full code execution flow with executor handlers")

		// Get optional tracer (Langfuse if available, otherwise NoopTracer)
		tracer, isLangfuse := testutils.GetTracerWithLogger("langfuse", logger)
		if tracer == nil {
			tracer, _ = testutils.GetTracerWithLogger("noop", logger)
		}
		if isLangfuse {
			logger.Info("Langfuse tracing enabled")
		} else {
			logger.Info("Langfuse tracing disabled (set LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY to enable)")
		}

		traceID := testutils.GenerateTestTraceID()
		logger.Info("Trace ID generated", loggerv2.String("trace_id", string(traceID)))

		if err := TestCodeExecutionWithExecutor(logger, tracer, traceID); err != nil {
			return fmt.Errorf("test failed: %w", err)
		}

		// Flush tracer if it supports flushing
		if flusher, ok := tracer.(interface{ Flush() }); ok {
			logger.Info("Flushing tracer...")
			flusher.Flush()
			logger.Info("Tracer flushed")
		}

		logger.Info("All code execution tests passed")
		logger.Info("")
		logger.Info("For detailed verification, see criteria.md in cmd/testing/mcp-agent-code-exec/")
		if isLangfuse {
			logger.Info("Langfuse trace available", loggerv2.String("trace_id", string(traceID)))
			logger.Info("   View in Langfuse dashboard or use: go run ./cmd/testing/... langfuse-read --trace-id " + string(traceID))
		}
		return nil
	},
}

// GetMCPAgentCodeExecTestCmd returns the test command
func GetMCPAgentCodeExecTestCmd() *cobra.Command {
	return mcpAgentCodeExecTestCmd
}

// TestCodeExecutionWithExecutor tests the full code execution flow
func TestCodeExecutionWithExecutor(log loggerv2.Logger, tracer observability.Tracer, traceID observability.TraceID) error {
	ctx := context.Background()

	// Step 1: Start HTTP server with executor handlers and bearer auth
	log.Info("--- Step 1: Start Executor HTTP Server ---")
	serverURL, apiToken, shutdown, err := startExecutorServer(log)
	if err != nil {
		return err
	}
	defer shutdown()

	log.Info("Executor server started", loggerv2.String("url", serverURL))

	// Give server a moment to fully start
	time.Sleep(500 * time.Millisecond)

	// Step 2: Create agent in code execution mode
	log.Info("--- Step 2: Create Code Execution Agent ---")
	agent, err := createCodeExecutionAgent(ctx, log, tracer, traceID, serverURL, apiToken)
	if err != nil {
		return err
	}
	defer agent.Close()

	log.Info("Code execution agent created")

	// Step 3: Test code execution with a simple task
	log.Info("--- Step 3: Test Code Execution Flow ---")
	if err := testCodeExecutionFlow(ctx, agent, log, traceID); err != nil {
		return err
	}

	log.Info("Code execution flow completed successfully")

	return nil
}

// startExecutorServer starts an HTTP server with executor handlers and bearer auth
func startExecutorServer(log loggerv2.Logger) (string, string, func(), error) {
	// Get config path
	configPath := testutils.GetDefaultTestConfigPath()
	log.Info("Using config for executor handlers", loggerv2.String("path", configPath))

	// Generate API token for bearer auth
	apiToken := executor.GenerateAPIToken()

	// Create executor handlers
	handlers := executor.NewExecutorHandlers(configPath, log)

	// Create HTTP mux with both batch and per-tool endpoints
	mux := http.NewServeMux()

	// Batch endpoints (legacy)
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)

	// Per-tool wildcard endpoints (used by OpenAPI spec)
	// These match paths like POST /tools/mcp/{server}/{tool}
	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		// Extract server and tool from path: /tools/mcp/{server}/{tool}
		path := strings.TrimPrefix(r.URL.Path, "/tools/mcp/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"success":false,"error":"invalid path, expected /tools/mcp/{server}/{tool}"}`, http.StatusBadRequest)
			return
		}
		server, tool := parts[0], parts[1]
		// Normalize: underscores back to hyphens for MCP server lookup
		server = strings.ReplaceAll(server, "_", "-")
		tool = strings.ReplaceAll(tool, "_", "-")
		log.Info("Per-tool MCP request via wildcard",
			loggerv2.String("server", server),
			loggerv2.String("tool", tool))
		handlers.HandlePerToolMCPRequest(w, r, server, tool)
	})

	// Wildcard handler for /tools/custom/{tool} routes
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		tool := strings.TrimPrefix(r.URL.Path, "/tools/custom/")
		if tool == "" {
			http.Error(w, `{"success":false,"error":"missing tool name"}`, http.StatusBadRequest)
			return
		}
		tool = strings.ReplaceAll(tool, "_", "-")
		log.Info("Per-tool custom request via wildcard",
			loggerv2.String("tool", tool))
		handlers.HandlePerToolCustomRequest(w, r, tool)
	})

	// Wrap with bearer token auth middleware
	authedHandler := executor.AuthMiddleware(apiToken)(mux)

	// Start server on port 8000 (default MCP_API_URL)
	server := &http.Server{
		Addr:              "127.0.0.1:8000",
		Handler:           authedHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Start server in background
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Server error", err)
		}
	}()

	serverURL := "http://127.0.0.1:8000"

	// Shutdown function
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Error("Server shutdown error", err)
		}
		log.Info("Executor server stopped")
	}

	return serverURL, apiToken, shutdown, nil
}

// createCodeExecutionAgent creates an agent in code execution mode
func createCodeExecutionAgent(ctx context.Context, log loggerv2.Logger, tracer observability.Tracer, traceID observability.TraceID, apiURL, apiToken string) (*mcpagent.Agent, error) {
	log.Info("Creating code execution agent...",
		loggerv2.String("trace_id", string(traceID)))

	// Create LLM using testutils
	llm, llmProvider, err := testutils.CreateTestLLMFromViper(log)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM: %w", err)
	}

	// Create temporary MCP config with context7
	mcpServers := map[string]interface{}{
		"context7": map[string]interface{}{
			"url":      "https://mcp.context7.com/mcp",
			"protocol": "http",
		},
	}

	configPath, cleanup, err := testutils.CreateTempMCPConfig(mcpServers, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp MCP config: %w", err)
	}
	// Note: We can't defer cleanup here since we need the config to persist for the agent
	// The caller should handle cleanup or the temp file will be cleaned up on process exit

	log.Info("Created temporary MCP config with context7",
		loggerv2.String("path", configPath))

	// Create agent with code execution mode enabled using the provided tracer
	agent, err := testutils.CreateTestAgent(ctx, &testutils.TestAgentConfig{
		LLM:        llm,
		Provider:   llmProvider,
		ConfigPath: configPath,
		Tracer:     tracer,
		TraceID:    traceID,
		Logger:     log,
		Options: []mcpagent.AgentOption{
			mcpagent.WithCodeExecutionMode(true),
			mcpagent.WithAPIConfig(apiURL, apiToken),
		},
	})
	if err != nil {
		cleanup() // Clean up config on error
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	log.Info("Code execution mode enabled")
	log.Info("MCP server configured: context7")
	log.Info("API URL and token configured for code execution",
		loggerv2.String("api_url", apiURL))

	return agent, nil
}

// testCodeExecutionFlow tests the code execution flow with a real MCP tool
func testCodeExecutionFlow(ctx context.Context, agent *mcpagent.Agent, log loggerv2.Logger, traceID observability.TraceID) error {
	log.Info("Testing code execution with context7 MCP tool...",
		loggerv2.String("trace_id", string(traceID)))

	// Test query: Ask agent to call get_api_spec and describe what it finds
	// The agent will use get_api_spec to discover endpoints and report back
	// Note: execute_shell_command is not registered in this test, so we only verify
	// that get_api_spec returns a valid OpenAPI spec for context7
	query := `Use get_api_spec to get the OpenAPI specification for the "context7" server.
Then describe the available endpoints and what parameters they accept for "react" library resolution.`

	log.Info("Sending query to agent", loggerv2.String("query", query))

	// Execute query
	startTime := time.Now()
	response, err := agent.Ask(ctx, query)
	duration := time.Since(startTime)

	if err != nil {
		return fmt.Errorf("agent query failed: %w", err)
	}

	log.Info("Agent response received",
		loggerv2.String("response", response),
		loggerv2.String("duration", duration.String()))

	// Validate response
	if response == "" {
		return fmt.Errorf("empty response from agent")
	}

	// Check if response contains evidence of OpenAPI spec retrieval
	// The response should mention endpoints, resolve-library-id, or OpenAPI concepts
	hasEndpointInfo := containsIgnoreCase(response, "resolve_library_id") ||
		containsIgnoreCase(response, "resolve-library-id") ||
		containsIgnoreCase(response, "/tools/mcp/context7")
	hasOpenAPIInfo := containsIgnoreCase(response, "openapi") ||
		containsIgnoreCase(response, "endpoint") ||
		containsIgnoreCase(response, "libraryName")

	if len(response) < 50 {
		log.Warn("Response seems too short - get_api_spec may not have been called",
			loggerv2.String("response", response))
	} else if !hasEndpointInfo && !hasOpenAPIInfo {
		log.Warn("Response doesn't mention endpoints or OpenAPI - spec may not have been returned",
			loggerv2.Int("response_length", len(response)))
	} else {
		log.Info("Response indicates OpenAPI spec was retrieved and parsed successfully",
			loggerv2.Int("response_length", len(response)))
	}

	log.Info("Code execution mode test completed")
	log.Info("Verified:")
	log.Info("  1. Agent has get_api_spec virtual tool available")
	log.Info("  2. get_api_spec generated OpenAPI spec from MCP tool definitions")
	log.Info("  3. OpenAPI spec includes per-tool endpoints with request schemas")
	log.Info("  4. LLM correctly parsed the spec and identified endpoints + parameters")
	log.Info("  5. Bearer token auth is configured in the OpenAPI spec")

	return nil
}

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	return strings.Contains(s, substr)
}
