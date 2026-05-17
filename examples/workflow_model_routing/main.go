package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
)

type providerConfig struct {
	Name     string
	Provider llm.Provider
	ModelID  string
	APIKey   string
	KeyName  string
}

func main() {
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load .env file: %v\n", err)
		}
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := resolveProvider(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		printUsage()
		os.Exit(1)
	}

	if cfg.APIKey == "" {
		fmt.Fprintf(os.Stderr, "%s is required for %s\n", cfg.KeyName, cfg.Name)
		os.Exit(1)
	}

	configPath := "mcp_servers.json"
	if len(os.Args) > 2 {
		configPath = os.Args[2]
	}

	question := "Use the available MCP tools to get React documentation, then return a concise implementation checklist."
	if len(os.Args) > 3 {
		question = os.Args[3]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	apiKeys := &llm.ProviderAPIKeys{}
	apiKeys.SetKeyForProvider(cfg.Provider, &cfg.APIKey)

	llmModel, err := llm.InitializeLLM(llm.Config{
		Provider:    cfg.Provider,
		ModelID:     cfg.ModelID,
		Temperature: 0.2,
		APIKeys:     apiKeys,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize %s (%s): %v\n", cfg.Provider, cfg.ModelID, err)
		os.Exit(1)
	}

	agent, err := mcpagent.NewAgent(
		ctx,
		llmModel,
		configPath,
		mcpagent.WithMaxTurns(12),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}

	start := time.Now()
	answer, err := agent.Ask(ctx, question)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Agent run failed after %s: %v\n", elapsed.Round(time.Millisecond), err)
		printTokenUsage(agent)
		os.Exit(1)
	}

	fmt.Printf("\n=== Provider ===\n%s / %s\n", cfg.Provider, cfg.ModelID)
	fmt.Printf("\n=== Elapsed ===\n%s\n", elapsed.Round(time.Millisecond))
	fmt.Println("\n=== Agent Response ===")
	fmt.Println(answer)
	printTokenUsage(agent)
}

func resolveProvider(name string) (providerConfig, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "kimi", "kimi-k2.6":
		return providerConfig{
			Name:     "Kimi",
			Provider: llm.ProviderKimi,
			ModelID:  "kimi-k2.6",
			APIKey:   os.Getenv("KIMI_API_KEY"),
			KeyName:  "KIMI_API_KEY",
		}, nil
	case "minimax", "m2.7", "minimax-m2.7":
		return providerConfig{
			Name:     "MiniMax",
			Provider: llm.ProviderMiniMax,
			ModelID:  "MiniMax-M2.7",
			APIKey:   os.Getenv("MINIMAX_API_KEY"),
			KeyName:  "MINIMAX_API_KEY",
		}, nil
	case "glm", "zai", "z-ai", "glm-5.1":
		return providerConfig{
			Name:     "GLM / Z.AI",
			Provider: llm.ProviderZAI,
			ModelID:  "glm-5.1",
			APIKey:   os.Getenv("ZAI_API_KEY"),
			KeyName:  "ZAI_API_KEY",
		}, nil
	default:
		return providerConfig{}, fmt.Errorf("unsupported provider %q", name)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  go run main.go <kimi|minimax|glm> [mcp_servers.json] [prompt]")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  go run main.go kimi")
	fmt.Println("  go run main.go minimax")
	fmt.Println("  go run main.go glm mcp_servers.json \"Research React Server Components and return a checklist\"")
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
