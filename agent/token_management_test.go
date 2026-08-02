package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

func TestCodingAgentsSkipWrapperTokenCounting(t *testing.T) {
	agent := &Agent{
		provider:          llm.ProviderClaudeCode,
		modelID:           "claude-sonnet-4-6",
		toolOutputHandler: NewToolOutputHandler(),
	}

	if agent.shouldUseWrapperTokenCounting() {
		t.Fatal("coding agents should rely on provider-native context management, not wrapper token counting")
	}
}

func TestNonCodingAgentsUseWrapperTokenCountingWhenConfigured(t *testing.T) {
	agent := &Agent{
		provider:          llm.ProviderOpenAI,
		modelID:           "gpt-5",
		toolOutputHandler: NewToolOutputHandler(),
	}

	if !agent.shouldUseWrapperTokenCounting() {
		t.Fatal("non-coding agents should use wrapper token counting when a tool output handler is configured")
	}
}

func TestWrapperTokenCountingRequiresHandler(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderOpenAI,
		modelID:  "gpt-5",
	}

	if agent.shouldUseWrapperTokenCounting() {
		t.Fatal("wrapper token counting should be disabled without a tool output handler")
	}
}
