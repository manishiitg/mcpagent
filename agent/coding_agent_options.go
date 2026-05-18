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

func extractCodingAgentSessionIDs(a *Agent, resp *llmtypes.ContentResponse) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return
	}
	additional := resp.Choices[0].GenerationInfo.Additional
	if additional == nil {
		return
	}
	if sid, ok := additional["claude_code_session_id"].(string); ok && sid != "" {
		a.ClaudeCodeSessionID = sid
	}
	if sid, ok := additional["gemini_session_id"].(string); ok && sid != "" {
		a.GeminiSessionID = sid
	}
	if dirID, ok := additional["gemini_project_dir_id"].(string); ok && dirID != "" {
		a.GeminiProjectDirID = dirID
	}
	if sid, ok := additional["codex_thread_id"].(string); ok && sid != "" {
		a.CodexSessionID = sid
	}
}

func (a *Agent) buildStructuredResumeOptions() []llmtypes.CallOption {
	var opts []llmtypes.CallOption
	switch a.provider {
	case llm.ProviderClaudeCode:
		if a.ClaudeCodeSessionID != "" {
			opts = append(opts, llm.WithResumeSessionID(a.ClaudeCodeSessionID))
		}
	case llm.ProviderGeminiCLI:
		if a.GeminiSessionID != "" {
			opts = append(opts, llm.WithGeminiResumeSessionID(a.GeminiSessionID))
		}
		if strings.TrimSpace(a.CodingAgentWorkingDir) == "" && a.GeminiProjectDirID != "" {
			opts = append(opts, llm.WithGeminiProjectDirID(a.GeminiProjectDirID))
		}
	case llm.ProviderCodexCLI:
		if a.CodexSessionID != "" && !a.codingAgentPersistentInteractiveEnabled() {
			opts = append(opts, llm.WithCodexResumeSessionID(a.CodexSessionID))
		}
	}
	return opts
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
