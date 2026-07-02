package mcpagent

import (
	"fmt"
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
		opts = append(opts, llm.WithCursorAutoApproveWebSearch())
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
	case llm.ProviderPiCLI:
		opts = append(opts, llm.WithPiInteractiveSessionID(sessionID))
		if a.PiPersistentInteractiveSession {
			opts = append(opts, llm.WithPiPersistentInteractiveSession(true))
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
	opts = append(opts, option(workingDir))

	// CLI providers that ALSO project the per-session system prompt into a
	// workspace instruction file (claude→CLAUDE.md, codex→AGENTS.md,
	// gemini→GEMINI.md) would otherwise inject the prompt
	// twice — once via the CLI flag/env/in-band channel and once via the
	// projected file — doubling the (often large) builder prompt. Carry it
	// through the projected file only; each adapter falls back to its normal
	// channel if the projection is disabled or its write fails. cursor and agy
	// carry the prompt through their rules file alone (single channel), so they
	// need no opt-in here.
	switch provider {
	case llm.ProviderClaudeCode:
		opts = append(opts, llm.WithClaudeCodeProjectInstructionOnly(true))
	case llm.ProviderCodexCLI:
		opts = append(opts, llm.WithCodexProjectInstructionOnly(true))
	case llm.ProviderGeminiCLI:
		opts = append(opts, llm.WithGeminiProjectInstructionOnly(true))
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
	if sid, ok := additional["gemini_session_id"].(string); ok && sid != "" {
		a.GeminiSessionID = sid
	}
	if dirID, ok := additional["gemini_project_dir_id"].(string); ok && dirID != "" {
		a.GeminiProjectDirID = dirID
	}
	if sid, ok := additional["codex_thread_id"].(string); ok && sid != "" {
		if a.Logger != nil && a.CodexSessionID != sid {
			a.Logger.Info(fmt.Sprintf("🔎 [CODEX_SESSION_DEBUG] CodexSessionID SET from response codex_thread_id: session=%q old=%q new=%q isolated=%v", a.SessionID, a.CodexSessionID, sid, a.IsolatedSessionWorkspace))
		}
		a.CodexSessionID = sid
	}
	if sid, ok := additional["cursor_session_id"].(string); ok && sid != "" {
		a.CursorSessionID = sid
	}
	if sid, ok := additional["agy_session_id"].(string); ok && sid != "" {
		a.AgySessionID = sid
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
		// In bridge-tools mode, Cursor must launch a fresh native conversation
		// so it discovers the just-written .cursor/mcp.json before the deny
		// hooks block built-in Shell/Read/Write. Resuming an older Cursor
		// conversation can preserve a stale native tool catalog with no
		// api-bridge tools, leaving the agent with no usable tools at all.
		if a.CursorSessionID != "" && !a.CursorBridgeToolsMode {
			opts = append(opts, llm.WithCursorResumeSessionID(a.CursorSessionID))
		}
	case llm.ProviderAgyCLI:
		if a.AgySessionID != "" {
			opts = append(opts, llm.WithAgyResumeSessionID(a.AgySessionID))
		}
	case llm.ProviderPiCLI:
		if a.PiSessionID != "" {
			opts = append(opts, llm.WithPiResumeSessionID(a.PiSessionID))
		}
	}
	return opts
}

func (a *Agent) appendCursorCLIIntegrationOptions(opts []llmtypes.CallOption) []llmtypes.CallOption {
	bridgeConfig, bridgeErr := a.BuildBridgeMCPConfig()
	if bridgeErr == nil {
		opts = append(opts, llm.WithCursorMCPConfig(bridgeConfig))
		// --approve-mcps auto-accepts cursor's "approve this MCP server?"
		// TUI dialog so the FIRST bridge tool call does not stall waiting
		// for a human to click through. Required whenever WithCursorMCPConfig
		// is set in a headless context.
		opts = append(opts, llm.WithCursorApproveMCPs())
		// WithCursorDenyBuiltinTools installs a per-session .cursor/hooks.json
		// that denies cursor's built-in Shell/Read/Edit/Write/etc. tools at the
		// hook layer, forcing the agent to route tool calls through the MCP
		// bridge we just configured. Only enable it alongside the bridge config
		// so we never deny built-ins without a working MCP fallback.
		opts = append(opts, llm.WithCursorDenyBuiltinTools(true))
		if a.Logger != nil {
			a.Logger.Info("🌉 [CURSOR_CLI] Configured MCP bridge through .cursor/mcp.json with deny-builtin hooks")
			a.Logger.Info("🌉 Using Cursor CLI in tmux mode with MCP bridge and deny-builtin hooks (no --force; hooks gate built-ins)")
		}
		return opts
	}

	if a.Logger != nil {
		a.Logger.Warn(fmt.Sprintf("Could not build bridge MCP config for Cursor CLI (tools may be limited): %v", bridgeErr))
	}
	// --force (= --yolo) puts cursor in auto-approve-everything mode, which
	// bypasses .cursor/hooks.json deny verdicts. Only pass --force when no
	// bridge config was available, because there are no hooks to enforce.
	opts = append(opts, llm.WithCursorForce())
	if a.Logger != nil {
		a.Logger.Info("🌉 Using Cursor CLI in tmux mode with live input support (--force yolo: bridge unavailable)")
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
	case llm.ProviderPiCLI:
		return llm.WithPiWorkingDir, true
	}
	return nil, false
}
