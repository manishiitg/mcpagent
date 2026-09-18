package mcpagent

import (
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
)

func TestProviderAccountContinuationCannotCrossAccounts(t *testing.T) {
	a := &Agent{provider: llm.ProviderCodexCLI, modelID: "gpt-test", sessionID: "owner-B", llmConfig: AgentLLMConfiguration{Primary: LLMModel{Provider: "codex-cli", ModelID: "gpt-test", ConnectionID: "account-B"}}}
	foreign := &AgentSessionHandle{SessionID: "owner-A", ConnectionID: "account-A", Provider: llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", NativeSessionID: "thread-A"}}
	a.applyAgentSessionHandle(foreign)
	if a.codexSessionID != "" || a.sessionID != "owner-B" {
		t.Fatal("foreign account's continuation was applied")
	}
	if a.currentAgentSessionHandle().ConnectionID != "account-B" {
		t.Fatal("continuation lost account binding")
	}
}
