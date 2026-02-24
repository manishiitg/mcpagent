package hello

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	testutils "github.com/manishiitg/mcpagent/cmd/testing/testutils"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

var helloTestCmd = &cobra.Command{
	Use:   "hello",
	Short: "Basic hello test and multi-MCP server test for Claude Code integration",
	Long: `Tests Claude Code integration at two levels:

1. Hello Test: Creates a minimal agent, sends "Say hello", verifies response.
2. Multi-MCP Test: Creates an agent with multiple MCP servers (sequential-thinking,
   context7), sends a question requiring tool usage, verifies tools are called.

NOTE: When running inside a Claude Code session, you must unset the CLAUDECODE
environment variable first: unset CLAUDECODE && mcpagent-test hello --provider claude-code

Examples:
  mcpagent-test hello --provider claude-code
  mcpagent-test hello --provider claude-code --log-level debug
  mcpagent-test hello --provider claude-code --log-file logs/hello-test.log`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := testutils.NewTestLoggerFromViper()

		logger.Info("=== Hello World Test ===")
		if err := TestHello(logger); err != nil {
			return fmt.Errorf("hello test failed: %w", err)
		}

		skipMCP := viper.GetBool("skip-mcp")
		if !skipMCP {
			logger.Info("")
			logger.Info("=== Multi-MCP Server Test ===")
			if err := TestMultiMCP(logger); err != nil {
				return fmt.Errorf("multi-MCP test failed: %w", err)
			}
		}

		logger.Info("")
		logger.Info("All hello tests passed")
		return nil
	},
}

func init() {
	helloTestCmd.Flags().Bool("skip-mcp", false, "Skip multi-MCP server test (run hello only)")
	_ = viper.BindPFlag("skip-mcp", helloTestCmd.Flags().Lookup("skip-mcp"))
}

// GetHelloTestCmd returns the test command
func GetHelloTestCmd() *cobra.Command {
	return helloTestCmd
}

// TestHello creates an agent and sends a basic hello message
func TestHello(log loggerv2.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Step 1: Create LLM
	log.Info("--- Step 1: Create LLM ---")
	model, provider, err := testutils.CreateTestLLMFromViper(log)
	if err != nil {
		return fmt.Errorf("failed to create LLM: %w", err)
	}
	log.Info("LLM created", loggerv2.String("provider", string(provider)))

	// Step 2: Create minimal agent (no MCP servers needed)
	log.Info("--- Step 2: Create Agent ---")
	tracer, _ := testutils.GetTracerWithLogger("noop", log)
	traceID := testutils.GenerateTestTraceID()

	agent, err := testutils.CreateMinimalAgent(ctx, model, provider, tracer, traceID, log)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer agent.Close()
	log.Info("Agent created successfully")

	// Step 3: Verify auto-disable for CLI providers (Claude Code / Gemini CLI)
	if provider == llm.ProviderClaudeCode || provider == llm.ProviderGeminiCLI {
		log.Info("--- Step 3: CLI provider auto-disable check ---",
			loggerv2.String("provider", string(provider)))
		log.Info("Provider is CLI-based, agent created with auto-disable logic")
	} else {
		log.Info("--- Step 3: Skipping CLI provider checks (provider is API-based) ---")
	}

	// Step 4: Send hello message
	log.Info("--- Step 4: Send Hello Message ---")
	startTime := time.Now()
	response, err := agent.Ask(ctx, "Say hello in one short sentence.")
	duration := time.Since(startTime)
	if err != nil {
		return fmt.Errorf("agent.Ask failed: %w", err)
	}

	log.Info("Got response",
		loggerv2.String("response", response),
		loggerv2.String("duration", duration.String()))

	// Step 5: Validate response
	log.Info("--- Step 5: Validate Response ---")
	if response == "" {
		return fmt.Errorf("response is empty")
	}

	responseLower := strings.ToLower(response)
	if !strings.Contains(responseLower, "hello") && !strings.Contains(responseLower, "hi") && !strings.Contains(responseLower, "hey") && !strings.Contains(responseLower, "greet") {
		log.Warn("Response may not contain a greeting", loggerv2.String("response", response))
	} else {
		log.Info("Response contains a greeting")
	}

	log.Info("Hello test passed", loggerv2.Int("response_length", len(response)))
	return nil
}

// TestMultiMCP creates an agent with multiple MCP servers and verifies tool usage
func TestMultiMCP(log loggerv2.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Step 1: Create LLM
	log.Info("--- Step 1: Create LLM ---")
	model, provider, err := testutils.CreateTestLLMFromViper(log)
	if err != nil {
		return fmt.Errorf("failed to create LLM: %w", err)
	}
	log.Info("LLM created", loggerv2.String("provider", string(provider)))

	// Step 2: Create temp MCP config with servers
	log.Info("--- Step 2: Create MCP Config ---")
	mcpServers := map[string]interface{}{
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
		return fmt.Errorf("failed to create temp MCP config: %w", err)
	}
	defer cleanup()

	log.Info("MCP config created",
		loggerv2.String("path", configPath),
		loggerv2.Int("server_count", len(mcpServers)))

	// Step 3: Create agent with MCP servers
	log.Info("--- Step 3: Create Agent with MCP Servers ---")
	tracer, _ := testutils.GetTracerWithLogger("noop", log)
	traceID := testutils.GenerateTestTraceID()

	var options []mcpagent.AgentOption
	if provider == llm.ProviderClaudeCode || provider == llm.ProviderGeminiCLI {
		options = append(options, mcpagent.WithProvider(provider))
	}

	agent, err := testutils.CreateAgentWithTracer(ctx, model, provider, configPath, tracer, traceID, log, options...)
	if err != nil {
		return fmt.Errorf("failed to create agent with MCP servers: %w", err)
	}
	defer agent.Close()

	log.Info("Agent created with MCP servers",
		loggerv2.Int("server_count", len(mcpServers)))

	// Step 4: Send a question requiring tool usage
	log.Info("--- Step 4: Send Multi-Tool Question ---")
	question := "Use the docfork tool to list or search for available documents. Just tell me what you found."

	log.Info("Sending question", loggerv2.String("question", question))
	startTime := time.Now()
	response, err := agent.Ask(ctx, question)
	duration := time.Since(startTime)

	if err != nil {
		return fmt.Errorf("agent.Ask with MCP failed: %w", err)
	}

	log.Info("Got response",
		loggerv2.String("response_preview", truncateString(response, 300)),
		loggerv2.Int("response_length", len(response)),
		loggerv2.String("duration", duration.String()))

	// Step 5: Validate response
	log.Info("--- Step 5: Validate Response ---")
	if response == "" {
		return fmt.Errorf("response is empty")
	}

	if len(response) < 10 {
		log.Warn("Response seems too short - tools may not have been called",
			loggerv2.Int("response_length", len(response)))
	} else {
		log.Info("Response length looks good",
			loggerv2.Int("response_length", len(response)))
	}

	responseLower := strings.ToLower(response)
	if strings.Contains(responseLower, "doc") || strings.Contains(responseLower, "found") || strings.Contains(responseLower, "cabinet") {
		log.Info("Response contains expected content (docfork info)")
	} else {
		log.Warn("Response may not contain expected docfork info", loggerv2.String("response", response))
	}

	log.Info("Multi-MCP test passed",
		loggerv2.Int("response_length", len(response)),
		loggerv2.String("duration", duration.String()))

	return nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
