package mcpagent

import (
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func (a *Agent) appendCodingAgentInteractiveOptions(opts []llmtypes.CallOption) []llmtypes.CallOption {
	opts = a.appendCodingAgentWorkingDirOption(opts)

	sessionID := strings.TrimSpace(a.SessionID)
	if sessionID == "" {
		return opts
	}

	switch a.provider {
	case llm.ProviderClaudeCode:
		opts = append(opts, llm.WithClaudeCodeInteractiveSessionID(sessionID))
		if a.ClaudeCodePersistentInteractiveSession {
			opts = append(opts, llm.WithClaudeCodePersistentInteractiveSession(true))
		}
	case llm.ProviderCodexCLI:
		opts = append(opts, llm.WithCodexInteractiveSessionID(sessionID))
		if strings.TrimSpace(a.CodingAgentWorkingDir) == "" {
			if legacyDir := strings.TrimSpace(a.CodexProjectDirID); legacyDir != "" {
				opts = append(opts, llm.WithCodexProjectDirID(legacyDir))
			}
		}
		if a.CodexPersistentInteractiveSession {
			opts = append(opts, llm.WithCodexPersistentInteractiveSession(true))
		}
	case llm.ProviderGeminiCLI:
		opts = append(opts, llm.WithGeminiInteractiveSessionID(sessionID))
		if a.GeminiPersistentInteractiveSession {
			opts = append(opts, llm.WithGeminiPersistentInteractiveSession(true))
		}
	}

	return opts
}

func (a *Agent) appendCodingAgentWorkingDirOption(opts []llmtypes.CallOption) []llmtypes.CallOption {
	workingDir := strings.TrimSpace(a.CodingAgentWorkingDir)
	if workingDir == "" {
		return opts
	}
	option, ok := codingAgentWorkingDirOptionForProvider(a.provider, a.ModelID)
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
	case llm.ProviderKimi:
		if kimiCodeCLITransportEnabled(modelID) {
			return llm.WithKimiWorkingDir, true
		}
	}
	return nil, false
}
