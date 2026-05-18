package mcpagent

import (
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func (a *Agent) appendCodingAgentInteractiveOptions(opts []llmtypes.CallOption) []llmtypes.CallOption {
	return a.appendCodingAgentInteractiveOptionsForProvider(opts, a.provider, a.ModelID)
}

func (a *Agent) appendCodingAgentInteractiveOptionsForProvider(opts []llmtypes.CallOption, provider llm.Provider, modelID string) []llmtypes.CallOption {
	opts = a.appendCodingAgentWorkingDirOptionForProvider(opts, provider, modelID)

	sessionID := strings.TrimSpace(a.SessionID)
	if sessionID == "" || !codingAgentPersistentInteractiveEnabledForProvider(provider, modelID, sessionID) {
		return opts
	}

	switch provider {
	case llm.ProviderClaudeCode:
		opts = append(opts, llm.WithClaudeCodeInteractiveSessionID(sessionID))
		opts = append(opts, llm.WithClaudeCodePersistentInteractiveSession(true))
	case llm.ProviderCodexCLI:
		opts = append(opts, llm.WithCodexInteractiveSessionID(sessionID))
		if strings.TrimSpace(a.CodingAgentWorkingDir) == "" {
			if legacyDir := strings.TrimSpace(a.CodexProjectDirID); legacyDir != "" {
				opts = append(opts, llm.WithCodexProjectDirID(legacyDir))
			}
		}
		opts = append(opts, llm.WithCodexPersistentInteractiveSession(true))
	case llm.ProviderGeminiCLI:
		opts = append(opts, llm.WithGeminiInteractiveSessionID(sessionID))
		opts = append(opts, llm.WithGeminiPersistentInteractiveSession(true))
	case llm.ProviderCursorCLI:
		opts = append(opts, llm.WithCursorInteractiveSessionID(sessionID))
		if a.CursorPersistentInteractiveSession {
			opts = append(opts, llm.WithCursorPersistentInteractiveSession(true))
		}
		if a.CursorBridgeToolsMode {
			opts = append(opts, llm.WithCursorMode("ask"))
		}
	case llm.ProviderOpenCodeCLI:
		opts = append(opts, llm.WithOpenCodeInteractiveSessionID(sessionID))
		if a.OpenCodePersistentInteractiveSession {
			opts = append(opts, llm.WithOpenCodePersistentInteractiveSession(true))
		}
	}

	return opts
}

func (a *Agent) codingAgentPersistentInteractiveEnabled() bool {
	return codingAgentPersistentInteractiveEnabledForProvider(a.provider, a.ModelID, a.SessionID)
}

func codingAgentPersistentInteractiveEnabledForProvider(provider llm.Provider, modelID, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	return llm.IsTmuxCodingAgentProvider(provider, modelID)
}

func (a *Agent) appendCodingAgentWorkingDirOptionForProvider(opts []llmtypes.CallOption, provider llm.Provider, modelID string) []llmtypes.CallOption {
	workingDir := strings.TrimSpace(a.CodingAgentWorkingDir)
	if workingDir == "" {
		return opts
	}
	option, ok := codingAgentWorkingDirOptionForProvider(provider, modelID)
	if !ok {
		return opts
	}
	return append(opts, option(workingDir))
}

func codingAgentWorkingDirOptionForProvider(provider llm.Provider, modelID string) (func(string) llmtypes.CallOption, bool) {
	switch provider {
	case llm.ProviderClaudeCode:
		return llm.WithClaudeCodeWorkingDir, true
	case llm.ProviderCodexCLI:
		return llm.WithCodexProjectDirID, true
	case llm.ProviderGeminiCLI:
		return llm.WithGeminiWorkingDir, true
	case llm.ProviderCursorCLI:
		return llm.WithCursorWorkingDir, true
	case llm.ProviderOpenCodeCLI:
		return llm.WithOpenCodeWorkingDir, true
	}
	return nil, false
}
