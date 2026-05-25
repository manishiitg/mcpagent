package mcpagent

import (
	"os"
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
	if sessionID == "" || !codingAgentInteractiveEnabledForProvider(provider, modelID, sessionID) {
		return opts
	}
	// Per-step override: when ForceStructuredCodingAgent is set (from
	// the workflow step config's transport="structured" field), skip
	// the interactive-session-id option entirely. The CLI adapter's
	// dispatcher then falls through to the structured JSON path.
	if a.ForceStructuredCodingAgent {
		return opts
	}

	switch provider {
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
	case llm.ProviderCursorCLI:
		opts = append(opts, llm.WithCursorInteractiveSessionID(sessionID))
		if a.CursorPersistentInteractiveSession {
			opts = append(opts, llm.WithCursorPersistentInteractiveSession(true))
		}
		// CursorBridgeToolsMode intentionally does NOT set --mode ask. Cursor's
		// ask mode is a conversational stance that hard-refuses natural-language
		// write requests with "Switch to Agent mode", which makes the chat
		// unusable for any task that involves writes. Cursor runs in default
		// agent mode; MCP bridge config is still provided via .cursor/mcp.json
		// for tools the agent chooses to invoke through the bridge.
	case llm.ProviderAgyCLI:
		opts = append(opts, llm.WithAgyInteractiveSessionID(sessionID))
		if a.AgyPersistentInteractiveSession {
			opts = append(opts, llm.WithAgyPersistentInteractiveSession(true))
		}
	}

	return opts
}

func codingAgentInteractiveEnabledForProvider(provider llm.Provider, modelID, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	return llm.IsTmuxCodingAgentProvider(provider, modelID)
}

func (a *Agent) appendCodingAgentWorkingDirOptionForProvider(opts []llmtypes.CallOption, provider llm.Provider, modelID string) []llmtypes.CallOption {
	workingDir := strings.TrimSpace(a.CodingAgentWorkingDir)
	// IsolatedSessionWorkspace overrides the caller-supplied workingDir
	// with a fresh per-Agent tmp dir. The dir is created lazily on
	// first call and rm -rf'd by Agent.Close. Workflow steps opt into
	// this; chat code paths don't.
	if a.IsolatedSessionWorkspace {
		if tmpDir := a.ensureIsolatedWorkspaceDir(); tmpDir != "" {
			workingDir = tmpDir
		}
	}
	if workingDir == "" {
		return opts
	}
	option, ok := codingAgentWorkingDirOptionForProvider(provider, modelID)
	if !ok {
		return opts
	}
	return append(opts, option(workingDir))
}

// ensureIsolatedWorkspaceDir returns the per-Agent isolated tmp dir,
// creating it on first call via sync.Once. Returns "" only on
// os.MkdirTemp failure (in which case the caller falls back to
// CodingAgentWorkingDir to preserve session usability — isolation is
// belt-and-suspenders, not a hard contract). Agent.Close rm -rf's the
// dir if it was created.
func (a *Agent) ensureIsolatedWorkspaceDir() string {
	a.isolatedWorkspaceOnce.Do(func() {
		dir, err := os.MkdirTemp("", "mlp-cli-session-*")
		if err != nil {
			if a.Logger != nil {
				a.Logger.Warn("IsolatedSessionWorkspace: os.MkdirTemp failed; falling back to CodingAgentWorkingDir")
			}
			return
		}
		a.isolatedWorkspacePath = dir
		if a.Logger != nil {
			a.Logger.Info("IsolatedSessionWorkspace: created tmp dir " + dir)
		}
	})
	return a.isolatedWorkspacePath
}

func extractCodingAgentSessionIDs(a *Agent, resp *llmtypes.ContentResponse) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return
	}
	a.updateCodingProviderSessionHandleFromResponse(resp)
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
	if sid, ok := additional["cursor_session_id"].(string); ok && sid != "" {
		a.CursorSessionID = sid
	}
	if sid, ok := additional["agy_session_id"].(string); ok && sid != "" {
		a.AgySessionID = sid
	}
	if sid, ok := additional["opencode_session_id"].(string); ok && sid != "" {
		a.OpenCodeSessionID = sid
	}
	if a.CodingProviderSessionHandle.Empty() {
		a.CodingProviderSessionHandle = a.legacyCodingProviderSessionHandle()
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
		if a.CodexSessionID != "" {
			opts = append(opts, llm.WithCodexResumeSessionID(a.CodexSessionID))
		}
	case llm.ProviderCursorCLI:
		if a.CursorSessionID != "" {
			opts = append(opts, llm.WithCursorResumeSessionID(a.CursorSessionID))
		}
	case llm.ProviderAgyCLI:
		if a.AgySessionID != "" {
			opts = append(opts, llm.WithAgyResumeSessionID(a.AgySessionID))
		}
	case llm.ProviderOpenCodeCLI:
		if a.OpenCodeSessionID != "" {
			opts = append(opts, llm.WithOpenCodeResumeSessionID(a.OpenCodeSessionID))
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
	case llm.ProviderAgyCLI:
		return llm.WithAgyWorkingDir, true
	case llm.ProviderOpenCodeCLI:
		return llm.WithOpenCodeWorkingDir, true
	}
	return nil, false
}
