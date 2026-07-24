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
	llm.ProviderClaudeCode: func(a *Agent, id string) { a.ClaudeCodeSessionID = id },
	llm.ProviderCodexCLI: func(a *Agent, id string) {
		if a.Logger != nil && a.CodexSessionID != id {
			a.Logger.Debug(fmt.Sprintf("CodexSessionID set via handle: session=%q old=%q new=%q isolated=%v", a.SessionID, a.CodexSessionID, id, a.IsolatedSessionWorkspace))
		}
		a.CodexSessionID = id
	},
	llm.ProviderCursorCLI: func(a *Agent, id string) { a.CursorSessionID = id },
	llm.ProviderPiCLI:     func(a *Agent, id string) { a.PiSessionID = id },
}

var codingAgentProjectDirIDSetters = map[llm.Provider]func(*Agent, string){
	llm.ProviderCodexCLI: func(a *Agent, dir string) { a.CodexProjectDirID = dir },
}

var codingAgentNativeSessionIDGetters = map[llm.Provider]func(*Agent) string{
	llm.ProviderClaudeCode: func(a *Agent) string { return a.ClaudeCodeSessionID },
	llm.ProviderCodexCLI:   func(a *Agent) string { return a.CodexSessionID },
	llm.ProviderCursorCLI:  func(a *Agent) string { return a.CursorSessionID },
	llm.ProviderPiCLI:      func(a *Agent) string { return a.PiSessionID },
}

var codingAgentProjectDirIDGetters = map[llm.Provider]func(*Agent) string{
	llm.ProviderCodexCLI: func(a *Agent) string { return a.CodexProjectDirID },
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
func (a *Agent) CurrentAgentSessionHandle() *AgentSessionHandle {
	if a == nil {
		return nil
	}
	providerHandle := a.CodingProviderSessionHandle
	if providerHandle.Empty() {
		providerHandle = a.legacyCodingProviderSessionHandle()
	}
	if providerHandle.Empty() && strings.TrimSpace(a.SessionID) == "" {
		return nil
	}
	handle := &AgentSessionHandle{
		SessionID:     strings.TrimSpace(a.SessionID),
		OwnerID:       strings.TrimSpace(a.SessionID),
		CorrelationID: string(a.TraceID),
		Provider:      providerHandle,
	}
	if llm.IsCodingAgentProvider(a.provider, a.ModelID) {
		handle.Scope = "coding_agent"
	}
	return handle
}

// ApplyAgentSessionHandle restores provider-native continuation state from a
// persisted handle. It intentionally does not restart providers itself; the next
// generation call uses the restored state to construct provider options.
func (a *Agent) ApplyAgentSessionHandle(handle *AgentSessionHandle) {
	if a == nil || handle == nil {
		return
	}
	if sessionID := strings.TrimSpace(handle.SessionID); sessionID != "" {
		a.SessionID = sessionID
	} else if ownerID := strings.TrimSpace(handle.OwnerID); ownerID != "" && strings.TrimSpace(a.SessionID) == "" {
		a.SessionID = ownerID
	}
	if handle.Provider.Empty() {
		return
	}
	if a.Logger != nil {
		a.Logger.Debug(fmt.Sprintf("Applying coding-agent session handle: session=%q provider=%q nativeSessionID=%q workingDir=%q isolated=%v", a.SessionID, handle.Provider.Provider, handle.Provider.NativeSessionID, handle.Provider.WorkingDir, a.IsolatedSessionWorkspace))
	}
	a.CodingProviderSessionHandle = handle.Provider
	a.applyCodingProviderSessionHandle(handle.Provider)
}

// ContinueAgentSession applies the handle and sends the latest message through
// the normal agent loop. For provider-native coding agents this means only the
// new user message is passed to the provider; the provider adapter uses the
// handle's native session state for history.
func (a *Agent) ContinueAgentSession(ctx context.Context, handle *AgentSessionHandle, message string) (string, []llmtypes.MessageContent, *AgentSessionHandle, error) {
	userMessage := llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: message}},
	}
	return a.ContinueAgentSessionWithHistory(ctx, handle, []llmtypes.MessageContent{userMessage})
}

// ContinueAgentSessionWithHistory applies the handle and runs the normal agent
// loop with caller-owned history. For provider-native coding agents the
// provider layer still receives only the latest user message; mcpagent keeps the
// full history for UI/persistence and API-backed providers.
func (a *Agent) ContinueAgentSessionWithHistory(ctx context.Context, handle *AgentSessionHandle, messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent, *AgentSessionHandle, error) {
	a.ApplyAgentSessionHandle(handle)
	answer, history, err := a.AskWithHistory(ctx, messages)
	if err != nil {
		return answer, history, a.CurrentAgentSessionHandle(), err
	}
	return answer, history, a.CurrentAgentSessionHandle(), nil
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
	handle := a.CodingProviderSessionHandle
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
	if strings.TrimSpace(handle.WorkingDir) == "" {
		handle.WorkingDir = strings.TrimSpace(a.CodingAgentWorkingDir)
	}
	if strings.TrimSpace(handle.Model) == "" {
		handle.Model = modelID
	}
	if a.Logger != nil {
		a.Logger.Debug(fmt.Sprintf("Resolved coding-agent continuation handle: session=%q provider=%q nativeSessionID=%q useLegacy=%v isolated=%v workingDir=%q", a.SessionID, provider, handle.NativeSessionID, useLegacy, a.IsolatedSessionWorkspace, handle.WorkingDir))
	}
	return handle, true
}

func (a *Agent) updateCodingProviderSessionHandleFromResponse(resp *llmtypes.ContentResponse) {
	if a == nil {
		return
	}
	if handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp); ok {
		a.CodingProviderSessionHandle = handle
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
		a.ModelID = model
	}
	if dir := strings.TrimSpace(handle.WorkingDir); dir != "" {
		a.CodingAgentWorkingDir = dir
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
		Model:    a.ModelID,
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
	if llm.IsTmuxCodingAgentProvider(a.provider, a.ModelID) && !a.usesStructuredTransport() {
		handle.Transport = llmtypes.CodingProviderTransportTmux
	} else if llm.IsCodingAgentProvider(a.provider, a.ModelID) {
		handle.Transport = llmtypes.CodingProviderTransportStructured
	}
	if getter, ok := codingAgentNativeSessionIDGetters[a.provider]; ok {
		handle.NativeSessionID = strings.TrimSpace(getter(a))
	}
	if getter, ok := codingAgentProjectDirIDGetters[a.provider]; ok {
		handle.ProjectDirID = strings.TrimSpace(getter(a))
	}
	handle.WorkingDir = strings.TrimSpace(a.CodingAgentWorkingDir)
	if handle.NativeSessionID == "" && handle.ProjectDirID == "" && handle.WorkingDir == "" {
		return llmtypes.CodingProviderSessionHandle{}
	}
	return handle
}
