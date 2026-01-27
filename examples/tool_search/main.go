package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
)

func main() {
	// Load .env file if it exists (check current dir and parent dirs)
	envPaths := []string{".env", "../.env", "../../.env"}
	for _, path := range envPaths {
		if _, err := os.Stat(path); err == nil {
			if err := godotenv.Load(path); err == nil {
				fmt.Printf("Loaded environment from %s\n", path)
				break
			}
		}
	}

	// Step 1: Get Vertex API key from environment
	vertexKey := os.Getenv("VERTEX_API_KEY")
	if vertexKey == "" {
		vertexKey = os.Getenv("GOOGLE_API_KEY")
	}
	if vertexKey == "" {
		fmt.Fprintf(os.Stderr, "Please set VERTEX_API_KEY or GOOGLE_API_KEY environment variable\n")
		os.Exit(1)
	}

	// Step 2: Initialize Vertex AI LLM with Gemini 3 Flash
	llmModel, err := llm.InitializeLLM(llm.Config{
		Provider:    llm.ProviderVertex,
		ModelID:     vertex.ModelGemini3FlashPreview,
		Temperature: 0.7,
		Logger:      nil,
		APIKeys: &llm.ProviderAPIKeys{
			Vertex: &vertexKey,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize LLM: %v\n", err)
		os.Exit(1)
	}

	// Step 3: Set up MCP server configuration path
	configPath := "mcp_servers.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// Step 4: Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Step 5: Create the agent with TOOL SEARCH MODE enabled
	// In tool search mode:
	// - LLM starts with only 'search_tools' virtual tool
	// - LLM must search for tools using regex patterns before using them
	// - Discovered tools become available dynamically
	//
	// Optional: Use WithPreDiscoveredTools() to make certain tools always available
	agent, err := mcpagent.NewAgent(
		ctx,
		llmModel,
		configPath,
		mcpagent.WithToolSearchMode(true), // Enable tool search mode
		// mcpagent.WithPreDiscoveredTools([]string{"get_weather"}), // Optional: pre-discover specific tools
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}
	defer agent.Close()

	// Print initial state
	fmt.Printf("\n=== Tool Search Mode Example ===\n")
	fmt.Printf("Initial tools available: %d (should be 1 - just search_tools)\n", len(agent.Tools))
	fmt.Printf("Deferred tools: %d (hidden until searched)\n", agent.GetDeferredToolCount())
	fmt.Printf("================================\n\n")

	// Step 6: Ask the agent a question
	// The LLM will need to:
	// 1. Call search_tools to find relevant tools
	// 2. Use the discovered tools to answer the question
	question := "Search for documentation tools and use them to get information about the React library"
	if len(os.Args) > 2 {
		question = os.Args[2]
	}

	fmt.Printf("Question: %s\n\n", question)

	answer, err := agent.Ask(ctx, question)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get answer: %v\n", err)
		os.Exit(1)
	}

	// Step 7: Print results
	fmt.Println("\n=== Agent Response ===")
	fmt.Println(answer)
	fmt.Println("======================")

	// Show discovered tools
	fmt.Printf("\n=== Tool Discovery Stats ===\n")
	fmt.Printf("Discovered tools: %d\n", agent.GetDiscoveredToolCount())
	fmt.Printf("Total tools now available: %d\n", len(agent.Tools))
	fmt.Printf("============================\n")
}
