package mcpagent

import (
	"context"
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
	switch provider {
	case string(llm.ProviderClaudeCode):
		if id := strings.TrimSpace(handle.NativeSessionID); id != "" {
			a.ClaudeCodeSessionID = id
		}
	case string(llm.ProviderCodexCLI):
		if id := strings.TrimSpace(handle.NativeSessionID); id != "" {
			a.CodexSessionID = id
		}
		if dir := strings.TrimSpace(handle.ProjectDirID); dir != "" {
			a.CodexProjectDirID = dir
		}
	case string(llm.ProviderGeminiCLI):
		if id := strings.TrimSpace(handle.NativeSessionID); id != "" {
			a.GeminiSessionID = id
		}
		if dir := strings.TrimSpace(handle.ProjectDirID); dir != "" {
			a.GeminiProjectDirID = dir
		}
	case string(llm.ProviderCursorCLI):
		if id := strings.TrimSpace(handle.NativeSessionID); id != "" {
			a.CursorSessionID = id
		}
	case string(llm.ProviderAgyCLI):
		if id := strings.TrimSpace(handle.NativeSessionID); id != "" {
			a.AgySessionID = id
		}
	case string(llm.ProviderOpenCodeCLI):
		if id := strings.TrimSpace(handle.NativeSessionID); id != "" {
			a.OpenCodeSessionID = id
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
	if llm.IsTmuxCodingAgentProvider(a.provider, a.ModelID) && !a.ForceStructuredCodingAgent {
		handle.Transport = llmtypes.CodingProviderTransportTmux
	} else if llm.IsCodingAgentProvider(a.provider, a.ModelID) {
		handle.Transport = llmtypes.CodingProviderTransportStructured
	}
	switch a.provider {
	case llm.ProviderClaudeCode:
		handle.NativeSessionID = strings.TrimSpace(a.ClaudeCodeSessionID)
	case llm.ProviderCodexCLI:
		handle.NativeSessionID = strings.TrimSpace(a.CodexSessionID)
		handle.ProjectDirID = strings.TrimSpace(a.CodexProjectDirID)
	case llm.ProviderGeminiCLI:
		handle.NativeSessionID = strings.TrimSpace(a.GeminiSessionID)
		handle.ProjectDirID = strings.TrimSpace(a.GeminiProjectDirID)
	case llm.ProviderCursorCLI:
		handle.NativeSessionID = strings.TrimSpace(a.CursorSessionID)
	case llm.ProviderAgyCLI:
		handle.NativeSessionID = strings.TrimSpace(a.AgySessionID)
	case llm.ProviderOpenCodeCLI:
		handle.NativeSessionID = strings.TrimSpace(a.OpenCodeSessionID)
	}
	handle.WorkingDir = strings.TrimSpace(a.CodingAgentWorkingDir)
	if handle.NativeSessionID == "" && handle.ProjectDirID == "" && handle.WorkingDir == "" {
		return llmtypes.CodingProviderSessionHandle{}
	}
	return handle
}
