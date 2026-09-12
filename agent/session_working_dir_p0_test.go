package mcpagent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodingAgentWorkingDirPersistenceAllProvidersP0(t *testing.T) {
	for _, provider := range []llm.Provider{llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderPiCLI, llm.ProviderMuseCLI} {
		for _, isolated := range []bool{false, true} {
			t.Run(string(provider)+map[bool]string{true: "/isolated", false: "/builder"}[isolated], func(t *testing.T) {
				expected := filepath.Join(t.TempDir(), "private-runtime")
				agent := &Agent{sessionID: "chat-a", provider: provider, codingAgentWorkingDir: expected, isolatedSessionWorkspace: isolated}
				if isolated {
					agent.codingAgentWorkingDir = "/workflow/shared"
					agent.isolatedWorkspaceOnce.Do(func() { agent.isolatedWorkspacePath = expected; agent.isolatedWorkspaceStable = true })
				}
				partial := llmtypes.CodingProviderSessionHandle{Provider: string(provider), NativeSessionID: "native-a", Transport: llmtypes.CodingProviderTransportTmux}
				response := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: &llmtypes.GenerationInfo{CodingProviderSessionHandle: &partial}}}}
				agent.updateCodingProviderSessionHandleFromResponse(response)
				if agent.codingProviderSessionHandle.WorkingDir != expected {
					t.Fatal("provider response erased launch directory")
				}
				// Simulate a legacy partial typed handle already resident in memory.
				agent.codingProviderSessionHandle.WorkingDir = ""
				raw, err := json.Marshal(SnapshotAgentSession(agent))
				if err != nil {
					t.Fatal(err)
				}
				var saved AgentSessionHandle
				if err := json.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Provider.WorkingDir != expected || saved.Provider.NativeSessionID != "native-a" {
					t.Fatalf("incomplete durable handle: %+v", saved.Provider)
				}
				restored := &Agent{provider: provider, sessionID: "chat-a"}
				restored.applyAgentSessionHandle(&saved)
				got := SnapshotAgentSession(restored)
				if got.Provider.WorkingDir != expected || got.Provider.NativeSessionID != "native-a" {
					t.Fatalf("restart lost identity: %+v", got.Provider)
				}
			})
		}
	}
}

func TestCodingAgentWorkingDirDoesNotOverrideProviderIdentityP0(t *testing.T) {
	agent := &Agent{provider: llm.ProviderMuseCLI, codingAgentWorkingDir: "/current"}
	explicit := llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", WorkingDir: "/provider-reported", NativeSessionID: "native"}
	if got := agent.withContinuationWorkingDir(explicit); got.WorkingDir != "/provider-reported" {
		t.Fatal("overwrote provider's explicit directory")
	}
	different := llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", NativeSessionID: "native"}
	if got := agent.withContinuationWorkingDir(different); got.WorkingDir != "" {
		t.Fatal("assigned another provider's directory")
	}
}
