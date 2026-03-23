package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/llm"
)

func main() {
	// Load .env file if it exists
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not load .env file: %v\n", err)
		}
	}

	// Step 1: Verify Claude Code CLI is available
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintf(os.Stderr, "Claude Code CLI not found in PATH. Install it first and authenticate before running this example.\n")
		fmt.Fprintf(os.Stderr, "Install: npm install -g @anthropic-ai/claude-code\n")
		os.Exit(1)
	}

	// Step 2: Resolve or build the mcpbridge binary used by coding-agent providers
	bridgePath, err := ensureBridgeBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to prepare mcpbridge binary: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("MCP_BRIDGE_BINARY", bridgePath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set MCP_BRIDGE_BINARY: %v\n", err)
		os.Exit(1)
	}

	// Step 3: Initialize Claude Code LLM
	llmModel, err := llm.InitializeLLM(llm.Config{
		Provider:    llm.ProviderClaudeCode,
		ModelID:     "claude-haiku-4-5",
		Temperature: 0.7,
		Logger:      nil,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize Claude Code LLM: %v\n", err)
		os.Exit(1)
	}

	// Step 4: Set up MCP server configuration path
	configPath := "mcp_servers.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// Step 5: Start the executor API used by the MCP bridge
	apiBaseURL, apiToken, shutdownServer, err := startExecutorServer(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start executor API: %v\n", err)
		os.Exit(1)
	}
	defer shutdownServer()

	fmt.Println("=== Basic Claude Code Example ===")
	fmt.Println("This example uses Claude Code as the agent provider and MCP tools through the mcpbridge layer.")
	fmt.Printf("mcpbridge: %s\n", bridgePath)
	fmt.Printf("executor API: %s\n", apiBaseURL)
	fmt.Println()

	// Step 6: Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Step 7: Create the agent
	agent, err := mcpagent.NewAgent(
		ctx,
		llmModel,
		configPath,
		mcpagent.WithProvider(llm.ProviderClaudeCode),
		mcpagent.WithAPIConfig(apiBaseURL, apiToken),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}
	defer agent.Close()

	// Register execute_shell_command so the bridge can expose it to Claude Code.
	shellEnv := append(mcpagent.BuildSafeEnvironment(),
		fmt.Sprintf("MCP_API_URL=%s", apiBaseURL),
		fmt.Sprintf("MCP_API_TOKEN=%s", apiToken),
	)
	err = agent.RegisterCustomTool(
		"execute_shell_command",
		codeexec.ShellCommandDescription,
		codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return codeexec.ExecuteShellCommand(ctx, args, shellEnv)
		},
		"workspace_advanced",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to register execute_shell_command tool: %v\n", err)
		os.Exit(1)
	}

	// Step 8: Ask the agent a question
	question := "Get me the documentation for React library"
	if len(os.Args) > 2 {
		question = os.Args[2]
	}

	answer, err := agent.Ask(ctx, question)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get answer: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n=== Agent Response ===")
	fmt.Println(answer)
	fmt.Println("=====================")
	printTokenUsage(agent)
}

func printTokenUsage(agent *mcpagent.Agent) {
	promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount := agent.GetTokenUsage()

	fmt.Println("\n=== Token Usage ===")
	fmt.Printf("Prompt tokens: %d\n", promptTokens)
	fmt.Printf("Completion tokens: %d\n", completionTokens)
	fmt.Printf("Total tokens: %d\n", totalTokens)
	fmt.Printf("Cache tokens: %d\n", cacheTokens)
	fmt.Printf("Reasoning tokens: %d\n", reasoningTokens)
	fmt.Printf("LLM calls: %d\n", llmCallCount)
	fmt.Printf("Cache-enabled calls: %d\n", cacheEnabledCallCount)
	fmt.Println("===================")
}

func startExecutorServer(configPath string) (string, string, func(), error) {
	apiToken := executor.GenerateAPIToken()
	handlers := executor.NewExecutorHandlers(configPath, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)
	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path[len("/tools/mcp/"):]
		slash := indexSlash(path)
		if slash <= 0 || slash >= len(path)-1 {
			http.Error(w, "invalid tool path", http.StatusBadRequest)
			return
		}
		server := path[:slash]
		tool := path[slash+1:]
		handlers.HandlePerToolMCPRequest(w, r, server, tool)
	})
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Path[len("/tools/custom/"):]
		if tool == "" {
			http.Error(w, "missing custom tool name", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolCustomRequest(w, r, tool)
	})
	mux.HandleFunc("/tools/virtual/", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Path[len("/tools/virtual/"):]
		if tool == "" {
			http.Error(w, "missing virtual tool name", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolVirtualRequest(w, r, tool)
	})

	authedHandler := executor.AuthMiddleware(apiToken)(mux)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("failed to listen on localhost: %w", err)
	}

	server := &http.Server{
		Handler: authedHandler,
	}

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Executor server error: %v\n", serveErr)
		}
	}()

	apiBaseURL := "http://" + listener.Addr().String()
	if err := os.Setenv("MCP_API_URL", apiBaseURL); err != nil {
		return "", "", func() {}, fmt.Errorf("failed to set MCP_API_URL: %w", err)
	}
	if err := os.Setenv("MCP_API_TOKEN", apiToken); err != nil {
		return "", "", func() {}, fmt.Errorf("failed to set MCP_API_TOKEN: %w", err)
	}

	time.Sleep(500 * time.Millisecond)

	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}

	return apiBaseURL, apiToken, shutdown, nil
}

func ensureBridgeBinary() (string, error) {
	if envPath := os.Getenv("MCP_BRIDGE_BINARY"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	if path, err := exec.LookPath("mcpbridge"); err == nil {
		return path, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	outputPath := filepath.Join(cwd, "generated", "mcpbridge")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create generated directory: %w", err)
	}

	cmd := exec.Command("go", "build", "-o", outputPath, "../../cmd/mcpbridge")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build ../../cmd/mcpbridge: %w", err)
	}

	return outputPath, nil
}

func indexSlash(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
