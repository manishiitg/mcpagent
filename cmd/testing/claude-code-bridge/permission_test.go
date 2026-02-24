package claudecodebridge

import (
	"context"
	"fmt"
	"log"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func RunPermissionTest() {
	fmt.Println("Starting permission test...")
	llmInstance, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderClaudeCode,
		ModelID:  "claude-code",
	})
	if err != nil {
		log.Fatalf("Failed to init LLM: %v", err)
	}

	opts := []llmtypes.CallOption{
		llm.WithAllowedTools("mcp__api-bridge__*,WebSearch"),
		// Intentionally NOT including WithDangerouslySkipPermissions()
	}

	messages := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "Run 'ls -la' using the Bash tool."),
	}

	ctx := context.Background()
	resp, err := llmInstance.GenerateContent(ctx, messages, opts...)
	if err != nil {
		log.Fatalf("GenerateContent error: %v", err)
	}

	fmt.Println("Response choices count:", len(resp.Choices))
	if len(resp.Choices) > 0 {
		fmt.Println("Content:", resp.Choices[0].Content)
		if resp.Choices[0].GenerationInfo != nil {
			if denials, ok := resp.Choices[0].GenerationInfo.Additional["permission_denials"]; ok {
				fmt.Printf("🎯 DEFINITIVE PROOF: Bash tool was blocked! Denials: %+v\n", denials)
			} else {
				fmt.Println("⚠️ No permission denials found.")
			}
		}
	}
}
