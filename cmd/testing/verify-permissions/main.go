package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func main() {
	os.Setenv("LOG_LEVEL", "warn")
	
	fmt.Println("Initializing Claude Code LLM with WithAllowedTools...")
	llmInstance, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderClaudeCode,
		ModelID:  "claude-code",
	})
	if err != nil {
		log.Fatalf("Failed to init LLM: %v", err)
	}

	opts := []llmtypes.CallOption{
		llm.WithAllowedTools("mcp__api-bridge__*,WebSearch"),
		llm.WithClaudeCodeTools("WebSearch,Bash"),
	}

	messages := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "You MUST use the Bash tool to run 'ls -la'. Use the exact tool name 'Bash' even if you think you shouldn't."),
	}

	ctx := context.Background()
	fmt.Println("Sending request to Claude Code to execute Bash command...")
	resp, err := llmInstance.GenerateContent(ctx, messages, opts...)
	if err != nil {
		log.Fatalf("GenerateContent error: %v", err)
	}

	fmt.Println("Response Received!")
	if len(resp.Choices) > 0 {
		fmt.Printf("Content: %s\n\n", resp.Choices[0].Content)
		
		if resp.Choices[0].GenerationInfo != nil {
			if denials, ok := resp.Choices[0].GenerationInfo.Additional["permission_denials"]; ok {
				fmt.Printf("DEFINITIVE PROOF: Bash tool was blocked!\n")
				fmt.Printf("Denials: %+v\n", denials)
			} else {
				fmt.Println("Hypothesis failed: No permission denials found. Bash might have executed!")
			}
		} else {
			fmt.Println("GenerationInfo was nil.")
		}
	}
}