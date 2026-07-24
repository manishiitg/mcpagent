package mcpagent

import (
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var codingAgentPersistentInteractiveEnabledByProvider = map[llm.Provider]func(*Agent) bool{
	llm.ProviderClaudeCode: func(a *Agent) bool { return a.ClaudeCodePersistentInteractiveSession },
	llm.ProviderCodexCLI:   func(a *Agent) bool { return a.CodexPersistentInteractiveSession },
	llm.ProviderCursorCLI:  func(a *Agent) bool { return a.CursorPersistentInteractiveSession },
	llm.ProviderPiCLI:      func(a *Agent) bool { return a.PiPersistentInteractiveSession },
}

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

	if option := llm.CodingAgentInteractiveSessionOption(provider, sessionID); option != nil {
		opts = append(opts, option)
	} else {
		return opts
	}

	if provider == llm.ProviderCodexCLI {
		if strings.TrimSpace(a.CodingAgentWorkingDir) == "" {
			if legacyDir := strings.TrimSpace(a.CodexProjectDirID); legacyDir != "" {
				opts = append(opts, llm.WithCodexProjectDirID(legacyDir))
			}
		}
	}

	if provider == llm.ProviderCursorCLI {
		opts = append(opts, llm.WithCursorAutoApproveWebSearch())
		// CursorBridgeToolsMode intentionally does NOT set --mode ask. Cursor's
		// ask mode is a conversational stance that hard-refuses natural-language
		// write requests with "Switch to Agent mode", which makes the chat
		// unusable for any task that involves writes. Cursor runs in default
		// agent mode; MCP bridge config is still provided via .cursor/mcp.json
		// for tools the agent chooses to invoke through the bridge.
	}

	if a.codingAgentPersistentInteractiveEnabled(provider) {
		if option := llm.CodingAgentPersistentInteractiveOption(provider, true); option != nil {
			opts = append(opts, option)
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
	cleanupInactiveCodingAgentProjectArtifacts(workingDir, provider)
	option := llm.CodingAgentWorkingDirOption(provider, workingDir)
	if option == nil {
		return opts
	}
	opts = append(opts, option)

	// CLI providers that ALSO project the per-session system prompt into a
	// workspace instruction file (claude→CLAUDE.md, codex→AGENTS.md) would
	// otherwise inject the prompt
	// twice — once via the CLI flag/env/in-band channel and once via the
	// projected file — doubling the (often large) builder prompt. Carry it
	// through the projected file only; each adapter falls back to its normal
	// channel if the projection is disabled or its write fails. Cursor carries
	// the prompt through its rules file alone, and Pi uses explicit append-
	// system-prompt, so they need no opt-in here.
	if option := llm.CodingAgentProjectInstructionOnlyOption(provider, true); option != nil {
		opts = append(opts, option)
	}

	return opts
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
	if sid, ok := additional["codex_thread_id"].(string); ok && sid != "" {
		if a.Logger != nil && a.CodexSessionID != sid {
			a.Logger.Debug(fmt.Sprintf("CodexSessionID set from response: session=%q old=%q new=%q isolated=%v", a.SessionID, a.CodexSessionID, sid, a.IsolatedSessionWorkspace))
		}
		a.CodexSessionID = sid
	}
	if sid, ok := additional["cursor_session_id"].(string); ok && sid != "" {
		a.CursorSessionID = sid
	}
	if sid, ok := additional["pi_session_id"].(string); ok && sid != "" {
		a.PiSessionID = sid
	}
	if a.CodingProviderSessionHandle.Empty() {
		a.CodingProviderSessionHandle = a.legacyCodingProviderSessionHandle()
	}
}

func (a *Agent) buildStructuredResumeOptions() []llmtypes.CallOption {
	var opts []llmtypes.CallOption
	handle := a.legacyCodingProviderSessionHandle()
	if sessionID := strings.TrimSpace(handle.NativeSessionID); sessionID != "" {
		if option := llm.NativeResumeOption(a.provider, sessionID); option != nil {
			opts = append(opts, option)
		}
	}
	return opts
}

func (a *Agent) appendCursorCLIIntegrationOptions(opts []llmtypes.CallOption) ([]llmtypes.CallOption, error) {
	bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
	if bridgeErr != nil {
		return nil, fmt.Errorf("Cursor CLI requires the MCP bridge: %w", bridgeErr)
	}

	opts = append(opts, llm.WithCursorMCPConfig(bridgeConfig))
	// --approve-mcps auto-accepts cursor's "approve this MCP server?"
	// TUI dialog so the FIRST bridge tool call does not stall waiting
	// for a human to click through. Required whenever WithCursorMCPConfig
	// is set in a headless context.
	opts = append(opts, llm.WithCursorApproveMCPs())
	if a.bridgeReadyFile != "" {
		// Hold a cold cursor session's first prompt until the bridge reports the
		// tools connected (tools/list answered) — closes the cold-turn race where
		// cursor's first bridge call fails and it falls back to its Shell tool.
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
	// WithCursorDenyBuiltinTools installs a per-session .cursor/hooks.json
	// that denies cursor's built-in Shell/Read/Edit/Write/etc. tools at the
	// hook layer, forcing the agent to route tool calls through the MCP
	// bridge we just configured.
	opts = append(opts, llm.WithCursorDenyBuiltinTools(true))
	if a.Logger != nil {
		a.Logger.Info("🌉 [CURSOR_CLI] Configured MCP bridge through .cursor/mcp.json with deny-builtin hooks")
		a.Logger.Info("⏱️ [CURSOR_CLI] No supported MCP-client timeout control; request cancellation and the mcpbridge HTTP backstop remain authoritative")
		a.Logger.Info("🌉 Using Cursor CLI in tmux mode with MCP bridge and deny-builtin hooks (no --force; hooks gate built-ins)")
	}
	if a.CursorStructuredTransport {
		opts = append(opts, llm.WithCursorStructuredTransport(true))
	}
	return opts, nil
}

func codingAgentWorkingDirOptionForProvider(provider llm.Provider, modelID string) (func(string) llmtypes.CallOption, bool) {
	if !llm.IsCodingAgentProvider(provider, modelID) {
		return nil, false
	}
	return func(dir string) llmtypes.CallOption {
		return llm.CodingAgentWorkingDirOption(provider, dir)
	}, true
}

func (a *Agent) codingAgentPersistentInteractiveEnabled(provider llm.Provider) bool {
	if a == nil {
		return false
	}
	if fn, ok := codingAgentPersistentInteractiveEnabledByProvider[provider]; ok {
		return fn(a)
	}
	return false
}
