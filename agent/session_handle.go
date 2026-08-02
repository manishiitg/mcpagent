package mcpagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// AgentSessionHandle is the mcpagent-owned continuation handle. Product layers
// may persist it as opaque JSON; provider-native fields remain nested inside the
// provider handle.
type AgentSessionHandle struct {
	AgentID       string                               `json:"agent_id,omitempty"`
	SessionID     string                               `json:"session_id,omitempty"`
	OwnerID       string                               `json:"owner_id,omitempty"`
	Scope         string                               `json:"scope,omitempty"`
	CorrelationID string                               `json:"correlation_id,omitempty"`
	Provider      llmtypes.CodingProviderSessionHandle `json:"provider,omitempty"`
}

var codingAgentNativeSessionIDSetters = map[llm.Provider]func(*Agent, string){
	llm.ProviderClaudeCode: func(a *Agent, id string) { a.claudeCodeSessionID = id },
	llm.ProviderCodexCLI: func(a *Agent, id string) {
		if a.logger != nil && a.codexSessionID != id {
			a.logger.Debug(fmt.Sprintf("CodexSessionID set via handle: session=%q old=%q new=%q isolated=%v", a.sessionID, a.codexSessionID, id, a.isolatedSessionWorkspace))
		}
		a.codexSessionID = id
	},
	llm.ProviderCursorCLI: func(a *Agent, id string) { a.cursorSessionID = id },
	llm.ProviderPiCLI:     func(a *Agent, id string) { a.piSessionID = id },
}

var codingAgentProjectDirIDSetters = map[llm.Provider]func(*Agent, string){
	llm.ProviderCodexCLI: func(a *Agent, dir string) { a.codexProjectDirID = dir },
}

var codingAgentNativeSessionIDGetters = map[llm.Provider]func(*Agent) string{
	llm.ProviderClaudeCode: func(a *Agent) string { return a.claudeCodeSessionID },
	llm.ProviderCodexCLI:   func(a *Agent) string { return a.codexSessionID },
	llm.ProviderCursorCLI:  func(a *Agent) string { return a.cursorSessionID },
	llm.ProviderPiCLI:      func(a *Agent) string { return a.piSessionID },
}

var codingAgentProjectDirIDGetters = map[llm.Provider]func(*Agent) string{
	llm.ProviderCodexCLI: func(a *Agent) string { return a.codexProjectDirID },
}

func (h *AgentSessionHandle) Empty() bool {
	if h == nil {
		return true
	}
	return strings.TrimSpace(h.SessionID) == "" &&
		strings.TrimSpace(h.OwnerID) == "" &&
		h.Provider.Empty()
}

// CurrentAgentSessionHandle returns the latest known continuation handle for
// the agent. It synthesizes a provider handle from legacy fields when the
// provider has not yet returned the typed handle.
func (a *Agent) currentAgentSessionHandle() *AgentSessionHandle {
	if a == nil {
		return nil
	}
	providerHandle := a.codingProviderSessionHandle
	if providerHandle.Empty() {
		providerHandle = a.legacyCodingProviderSessionHandle()
	}
	if providerHandle.Empty() && strings.TrimSpace(a.sessionID) == "" {
		return nil
	}
	handle := &AgentSessionHandle{
		SessionID:     strings.TrimSpace(a.sessionID),
		OwnerID:       strings.TrimSpace(a.sessionID),
		CorrelationID: string(a.traceID),
		Provider:      providerHandle,
	}
	if llm.IsCodingAgentProvider(a.provider, a.modelID) {
		handle.Scope = "coding_agent"
	}
	return handle
}

// ApplyAgentSessionHandle restores provider-native continuation state from a
// persisted handle. It intentionally does not restart providers itself; the next
// generation call uses the restored state to construct provider options.
func (a *Agent) applyAgentSessionHandle(handle *AgentSessionHandle) {
	if a == nil || handle == nil {
		return
	}
	configuredProvider := a.provider
	configuredModel := strings.TrimSpace(a.modelID)
	if sessionID := strings.TrimSpace(handle.SessionID); sessionID != "" {
		a.sessionID = sessionID
	} else if ownerID := strings.TrimSpace(handle.OwnerID); ownerID != "" && strings.TrimSpace(a.sessionID) == "" {
		a.sessionID = ownerID
	}
	if handle.Provider.Empty() {
		return
	}
	if a.logger != nil {
		a.logger.Debug(fmt.Sprintf("Applying coding-agent session handle: session=%q provider=%q nativeSessionID=%q workingDir=%q isolated=%v", a.sessionID, handle.Provider.Provider, handle.Provider.NativeSessionID, handle.Provider.WorkingDir, a.isolatedSessionWorkspace))
	}
	a.codingProviderSessionHandle = handle.Provider
	a.applyCodingProviderSessionHandle(handle.Provider)
	// A persisted handle identifies the provider-native conversation, but its
	// model describes the previous turn. Preserve an explicitly configured
	// model for this agent so role changes (for example Builder -> Pulse) can
	// resume the same conversation with the model selected for the new turn.
	if configuredProvider != "" && configuredModel != "" {
		a.provider = configuredProvider
		a.modelID = configuredModel
		a.codingProviderSessionHandle.Provider = string(configuredProvider)
		a.codingProviderSessionHandle.Model = configuredModel
	}
}

// ContinueAgentSessionWithHistory applies the handle and runs the normal agent
// loop with caller-owned history. For provider-native coding agents the
// provider layer still receives only the latest user message; mcpagent keeps the
// full history for UI/persistence and API-backed providers.
func (a *Agent) continueAgentSessionWithHistory(ctx context.Context, handle *AgentSessionHandle, messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent, *AgentSessionHandle, error) {
	a.applyAgentSessionHandle(handle)
	answer, history, err := a.askWithHistory(ctx, messages)
	if err != nil {
		return answer, history, a.currentAgentSessionHandle(), err
	}
	return answer, history, a.currentAgentSessionHandle(), nil
}

func (a *Agent) codingProviderContinuationHandleForModel(provider llm.Provider, modelID string) (llmtypes.CodingProviderSessionHandle, bool) {
	if a == nil || !llm.IsCodingAgentProvider(provider, modelID) {
		return llmtypes.CodingProviderSessionHandle{}, false
	}
	contract, ok := llm.GetCodingAgentProviderContract(provider, modelID)
	if !ok || !contract.SupportsNativeResume {
		return llmtypes.CodingProviderSessionHandle{}, false
	}
	// Resolve a usable handle. The primary field (a.CodingProviderSessionHandle)
	// is set on restore via ApplyAgentSessionHandle from the persisted chat
	// history. For chats saved BEFORE cursor adapter started attaching a
	// SessionHandle (mlp ccf010e), the persisted handle may exist but be
	// missing NativeSessionID — the seed populates a.CursorSessionID
	// independently, but the primary handle stays "valid but native-less".
	// In that case the legacy lookup (which derives NativeSessionID from the
	// per-provider *SessionID fields seeded above) recovers the missing
	// piece. Fall back to legacy whenever the primary handle would reject;
	// the legacy handle's stricter Empty() check guards correctness.
	handle := a.codingProviderSessionHandle
	useLegacy := handle.Empty() ||
		!strings.EqualFold(strings.TrimSpace(handle.Provider), strings.TrimSpace(string(provider))) ||
		strings.TrimSpace(handle.NativeSessionID) == ""
	if useLegacy {
		handle = a.legacyCodingProviderSessionHandle()
	}
	if handle.Empty() {
		return llmtypes.CodingProviderSessionHandle{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(handle.Provider), strings.TrimSpace(string(provider))) {
		return llmtypes.CodingProviderSessionHandle{}, false
	}
	if strings.TrimSpace(handle.NativeSessionID) == "" {
		return llmtypes.CodingProviderSessionHandle{}, false
	}
	handle.WorkingDir = a.resolveContinuationWorkingDir(handle.WorkingDir)
	// The continuation identity is the native session ID. The caller's model is
	// authoritative for the new turn; the handle's model belongs to the turn
	// that originally created or last persisted the conversation.
	handle.Model = modelID
	if a.logger != nil {
		a.logger.Debug(fmt.Sprintf("Resolved coding-agent continuation handle: session=%q provider=%q nativeSessionID=%q useLegacy=%v isolated=%v workingDir=%q", a.sessionID, provider, handle.NativeSessionID, useLegacy, a.isolatedSessionWorkspace, handle.WorkingDir))
	}
	return handle, true
}

// resolveContinuationWorkingDir decides which directory a resumed coding-CLI
// turn runs in. A recorded handle dir always wins; otherwise an isolated
// session returns to ITS OWN workspace, and only a non-isolated session falls
// back to the user's real workspace.
//
// The isolated case is the one that bites: the conversation was created with
// the CLI's cwd set to the isolated dir, and coding CLIs key their resumable
// conversation by cwd. Falling back to CodingAgentWorkingDir therefore sent the
// CLI hunting for the conversation under the wrong project key, which surfaced
// as "No conversation found with session ID" and silently restarted the turn as
// a fresh conversation.
func (a *Agent) resolveContinuationWorkingDir(handleWorkingDir string) string {
	if dir := strings.TrimSpace(handleWorkingDir); dir != "" {
		return dir
	}
	if a.isolatedSessionWorkspace {
		if dir := strings.TrimSpace(a.ensureIsolatedWorkspaceDir()); dir != "" {
			return dir
		}
	}
	return strings.TrimSpace(a.codingAgentWorkingDir)
}

func (a *Agent) updateCodingProviderSessionHandleFromResponse(resp *llmtypes.ContentResponse) {
	if a == nil {
		return
	}
	if handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp); ok {
		a.codingProviderSessionHandle = handle
		a.applyCodingProviderSessionHandle(handle)
	}
}

func (a *Agent) applyCodingProviderSessionHandle(handle llmtypes.CodingProviderSessionHandle) {
	if a == nil || handle.Empty() {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(handle.Provider))
	if provider != "" {
		a.provider = llm.Provider(provider)
	}
	if model := strings.TrimSpace(handle.Model); model != "" {
		a.modelID = model
	}
	if dir := strings.TrimSpace(handle.WorkingDir); dir != "" {
		a.codingAgentWorkingDir = dir
	}
	providerID := llm.Provider(provider)
	if id := strings.TrimSpace(handle.NativeSessionID); id != "" {
		if setter, ok := codingAgentNativeSessionIDSetters[providerID]; ok {
			setter(a, id)
		}
	}
	if dir := strings.TrimSpace(handle.ProjectDirID); dir != "" {
		if setter, ok := codingAgentProjectDirIDSetters[providerID]; ok {
			setter(a, dir)
		}
	}
}

func (a *Agent) legacyCodingProviderSessionHandle() llmtypes.CodingProviderSessionHandle {
	if a == nil {
		return llmtypes.CodingProviderSessionHandle{}
	}
	handle := llmtypes.CodingProviderSessionHandle{
		Provider: string(a.provider),
		Model:    a.modelID,
		Status:   llmtypes.CodingProviderSessionStatusIdle,
	}
	// Transport-aware, not just Force-aware: a per-provider structured flag
	// (CodexStructuredTransport/CursorStructuredTransport/PiStructuredTransport)
	// runs the turn on the JSON transport just as ForceStructuredCodingAgent
	// does. usesStructuredTransport() is the union of both, so the continuation
	// handle records the transport the turn ACTUALLY ran on. Without this, a
	// structured turn recorded Transport=tmux and the NEXT turn resumed the
	// native session through the tmux path (`Continuing … with native session`)
	// instead of `--json --resume`, silently losing all turn-1 context on
	// Codex/Cursor (found live: multi-turn wrote a hallucinated build id).
	if llm.IsTmuxCodingAgentProvider(a.provider, a.modelID) && !a.usesStructuredTransport() {
		handle.Transport = llmtypes.CodingProviderTransportTmux
	} else if llm.IsCodingAgentProvider(a.provider, a.modelID) {
		handle.Transport = llmtypes.CodingProviderTransportStructured
	}
	if getter, ok := codingAgentNativeSessionIDGetters[a.provider]; ok {
		handle.NativeSessionID = strings.TrimSpace(getter(a))
	}
	if getter, ok := codingAgentProjectDirIDGetters[a.provider]; ok {
		handle.ProjectDirID = strings.TrimSpace(getter(a))
	}
	// Must be the dir the conversation was actually created in, which for an
	// isolated session is the session workspace — NOT CodingAgentWorkingDir.
	// This is the legacy handle used whenever the adapter did not attach one of
	// its own (every structured adapter today), so hardcoding
	// CodingAgentWorkingDir here silently pinned the handle to the user's real
	// workspace and made native resume look for the conversation under the
	// wrong project key: "No conversation found with session ID", turn restarts
	// fresh, conversation lost. Reuses the same precedence as resume.
	handle.WorkingDir = a.resolveContinuationWorkingDir("")
	if handle.NativeSessionID == "" && handle.ProjectDirID == "" && handle.WorkingDir == "" {
		return llmtypes.CodingProviderSessionHandle{}
	}
	return handle
}
