package retainedturn

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestFinalResponseUsesLastAssistantText(t *testing.T) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "progress"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeTool, "tool result"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, " final answer "),
	}
	if got := finalResponse(messages); got != "final answer" {
		t.Fatalf("finalResponse()=%q, want final answer", got)
	}
}
