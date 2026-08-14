// Package retainedturn recovers structured output for messages delivered
// directly to an already-running coding CLI.
package retainedturn

import (
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// FinalResponse returns the authoritative final assistant text for a retained
// coding-CLI turn. It reads only the provider's structured sidecar transcript
// and never starts, resumes, configures, or sends input to an agent session.
func FinalResponse(provider llmproviders.Provider, ownerSessionID string, turnStart time.Time) string {
	messages := llmproviders.ReadCodingAgentRetainedTurnMessages(provider, ownerSessionID, turnStart)
	return finalResponse(messages)
}

func finalResponse(messages []llmtypes.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		parts := make([]string, 0, len(messages[i].Parts))
		for _, part := range messages[i].Parts {
			switch text := part.(type) {
			case llmtypes.TextContent:
				if value := strings.TrimSpace(text.Text); value != "" {
					parts = append(parts, value)
				}
			case *llmtypes.TextContent:
				if text != nil {
					if value := strings.TrimSpace(text.Text); value != "" {
						parts = append(parts, value)
					}
				}
			}
		}
		if result := strings.TrimSpace(strings.Join(parts, "\n")); result != "" {
			return result
		}
	}
	return ""
}
