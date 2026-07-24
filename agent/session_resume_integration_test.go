package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecode "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

func TestSessionIDExtractionFromGenerationInfo(t *testing.T) {
	tests := []struct {
		name       string
		provider   llm.Provider
		additional map[string]interface{}
		wantClaude string
		wantCodex  string
		wantPi     string
	}{
		{
			name:     "claude code session ID extracted",
			provider: llm.ProviderClaudeCode,
			additional: map[string]interface{}{
				"claude_code_session_id": "claude-sess-abc123",
				"provider":               "claude-code",
			},
			wantClaude: "claude-sess-abc123",
		},
		{
			name:     "codex thread ID extracted",
			provider: llm.ProviderCodexCLI,
			additional: map[string]interface{}{
				"codex_thread_id": "019e-codex-thread-id",
				"provider":        "codex-cli",
			},
			wantCodex: "019e-codex-thread-id",
		},
		{
			name:     "pi session ID extracted",
			provider: llm.ProviderPiCLI,
			additional: map[string]interface{}{
				"pi_session_id": "mlp-pi-session-id",
				"provider":      "pi-cli",
			},
			wantPi: "mlp-pi-session-id",
		},
		{
			name:     "empty session ID not stored",
			provider: llm.ProviderClaudeCode,
			additional: map[string]interface{}{
				"claude_code_session_id": "",
			},
			wantClaude: "",
		},
		{
			name:     "wrong type not stored",
			provider: llm.ProviderCodexCLI,
			additional: map[string]interface{}{
				"codex_thread_id": 12345,
			},
			wantCodex: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{provider: tt.provider}

			resp := &llmtypes.ContentResponse{
				Choices: []*llmtypes.ContentChoice{
					{
						Content: "test response",
						GenerationInfo: &llmtypes.GenerationInfo{
							Additional: tt.additional,
						},
					},
				},
			}

			extractCodingAgentSessionIDs(agent, resp)

			if agent.ClaudeCodeSessionID != tt.wantClaude {
				t.Errorf("ClaudeCodeSessionID = %q, want %q", agent.ClaudeCodeSessionID, tt.wantClaude)
			}
			if agent.CodexSessionID != tt.wantCodex {
				t.Errorf("CodexSessionID = %q, want %q", agent.CodexSessionID, tt.wantCodex)
			}
			if agent.PiSessionID != tt.wantPi {
				t.Errorf("PiSessionID = %q, want %q", agent.PiSessionID, tt.wantPi)
			}
		})
	}
}

func TestSessionIDResumeOptionsInjected(t *testing.T) {
	tests := []struct {
		name                              string
		provider                          llm.Provider
		modelID                           string
		claudeSessionID                   string
		codexSessionID                    string
		cursorSessionID                   string
		cursorBridgeToolsMode             bool
		piSessionID                       string
		sessionID                         string
		codexPersistentInteractiveSession bool
		wantResumeKey                     string
		wantResumeValue                   string
		wantProjectDirKey                 string
		wantProjectDirValue               string
	}{
		{
			name:            "claude code passes resume session ID",
			provider:        llm.ProviderClaudeCode,
			claudeSessionID: "claude-resume-id",
			wantResumeKey:   claudecode.MetadataKeyResumeSessionID,
			wantResumeValue: "claude-resume-id",
		},
		{
			name:            "codex passes resume thread ID when no persistent interactive",
			provider:        llm.ProviderCodexCLI,
			codexSessionID:  "codex-thread-id",
			wantResumeKey:   codexcli.MetadataKeyResumeSessionID,
			wantResumeValue: "codex-thread-id",
		},
		{
			name:                              "codex passes resume thread ID when persistent interactive enabled",
			provider:                          llm.ProviderCodexCLI,
			modelID:                           "gpt-5.3-codex-spark",
			codexSessionID:                    "codex-thread-id",
			sessionID:                         "app-session-id",
			codexPersistentInteractiveSession: true,
			wantResumeKey:                     codexcli.MetadataKeyResumeSessionID,
			wantResumeValue:                   "codex-thread-id",
		},
		{
			name:            "cursor passes resume session ID outside bridge mode",
			provider:        llm.ProviderCursorCLI,
			cursorSessionID: "cursor-native-id",
			wantResumeKey:   cursorcli.MetadataKeyResumeSessionID,
			wantResumeValue: "cursor-native-id",
		},
		{
			name:                  "cursor bridge mode still passes resume session ID",
			provider:              llm.ProviderCursorCLI,
			cursorSessionID:       "cursor-native-id",
			cursorBridgeToolsMode: true,
			wantResumeKey:         cursorcli.MetadataKeyResumeSessionID,
			wantResumeValue:       "cursor-native-id",
		},
		{
			name:            "pi passes native session ID",
			provider:        llm.ProviderPiCLI,
			piSessionID:     "mlp-pi-resume-id",
			wantResumeKey:   picli.MetadataKeyResumeSessionID,
			wantResumeValue: "mlp-pi-resume-id",
		},
		{
			name:     "claude code no resume when session ID empty",
			provider: llm.ProviderClaudeCode,
		},
		{
			name:     "codex no resume when session ID empty",
			provider: llm.ProviderCodexCLI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{
				provider:                          tt.provider,
				ModelID:                           tt.modelID,
				SessionID:                         tt.sessionID,
				ClaudeCodeSessionID:               tt.claudeSessionID,
				CodexSessionID:                    tt.codexSessionID,
				CursorSessionID:                   tt.cursorSessionID,
				CursorBridgeToolsMode:             tt.cursorBridgeToolsMode,
				PiSessionID:                       tt.piSessionID,
				CodexPersistentInteractiveSession: tt.codexPersistentInteractiveSession,
			}

			opts := agent.buildStructuredResumeOptions()
			meta := metadataFromCallOptions(opts)

			if tt.wantResumeKey == "" {
				if len(meta) > 0 {
					t.Fatalf("expected no resume options, got metadata: %v", meta)
				}
				return
			}

			if meta[tt.wantResumeKey] != tt.wantResumeValue {
				t.Fatalf("resume metadata %q = %v, want %q", tt.wantResumeKey, meta[tt.wantResumeKey], tt.wantResumeValue)
			}

			if tt.wantProjectDirKey != "" {
				if meta[tt.wantProjectDirKey] != tt.wantProjectDirValue {
					t.Fatalf("project dir metadata %q = %v, want %q", tt.wantProjectDirKey, meta[tt.wantProjectDirKey], tt.wantProjectDirValue)
				}
			}
		})
	}
}

func TestSessionIDRoundTrip(t *testing.T) {
	providers := []struct {
		name       string
		provider   llm.Provider
		sessionKey string
		resumeKey  string
	}{
		{
			name:       "claude code",
			provider:   llm.ProviderClaudeCode,
			sessionKey: "claude_code_session_id",
			resumeKey:  claudecode.MetadataKeyResumeSessionID,
		},
		{
			name:       "codex cli",
			provider:   llm.ProviderCodexCLI,
			sessionKey: "codex_thread_id",
			resumeKey:  codexcli.MetadataKeyResumeSessionID,
		},
		{
			name:       "cursor cli",
			provider:   llm.ProviderCursorCLI,
			sessionKey: "cursor_session_id",
			resumeKey:  cursorcli.MetadataKeyResumeSessionID,
		},
		{
			name:       "pi cli",
			provider:   llm.ProviderPiCLI,
			sessionKey: "pi_session_id",
			resumeKey:  picli.MetadataKeyResumeSessionID,
		},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			agent := &Agent{provider: p.provider}
			sessionID := "roundtrip-sess-" + p.name

			resp := &llmtypes.ContentResponse{
				Choices: []*llmtypes.ContentChoice{
					{
						Content: "turn 1 response",
						GenerationInfo: &llmtypes.GenerationInfo{
							Additional: map[string]interface{}{
								p.sessionKey: sessionID,
							},
						},
					},
				},
			}

			extractCodingAgentSessionIDs(agent, resp)

			opts := agent.buildStructuredResumeOptions()
			meta := metadataFromCallOptions(opts)

			if meta[p.resumeKey] != sessionID {
				t.Fatalf("round-trip failed: extracted %q but resume option has %v (key=%s)", sessionID, meta[p.resumeKey], p.resumeKey)
			}
		})
	}
}

func TestTypedCodingProviderSessionHandleUpdatesAgent(t *testing.T) {
	agent := &Agent{provider: llm.ProviderClaudeCode, ModelID: "old-model"}
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content: "turn 1 response",
				GenerationInfo: &llmtypes.GenerationInfo{
					CodingProviderSessionHandle: &llmtypes.CodingProviderSessionHandle{
						Provider:        "claude-code",
						Transport:       llmtypes.CodingProviderTransportTmux,
						NativeSessionID: "claude-native-1",
						TmuxSession:     "tmux-1",
						WorkingDir:      "/workspace",
						Model:           "claude-sonnet-4-6",
						Status:          llmtypes.CodingProviderSessionStatusIdle,
					},
				},
			},
		},
	}

	extractCodingAgentSessionIDs(agent, resp)

	if agent.ClaudeCodeSessionID != "claude-native-1" {
		t.Fatalf("ClaudeCodeSessionID = %q, want claude-native-1", agent.ClaudeCodeSessionID)
	}
	if agent.CodingAgentWorkingDir != "/workspace" {
		t.Fatalf("CodingAgentWorkingDir = %q, want /workspace", agent.CodingAgentWorkingDir)
	}
	if agent.CodingProviderSessionHandle.TmuxSession != "tmux-1" {
		t.Fatalf("typed handle not stored: %#v", agent.CodingProviderSessionHandle)
	}
}

func TestAgentSessionHandleApplyRestoresProviderState(t *testing.T) {
	agent := &Agent{}
	handle := &AgentSessionHandle{
		SessionID: "app-session-1",
		Provider: llmtypes.CodingProviderSessionHandle{
			Provider:        "codex-cli",
			Transport:       llmtypes.CodingProviderTransportStructured,
			NativeSessionID: "codex-thread-1",
			WorkingDir:      "/workspace",
			ProjectDirID:    "codex-project-1",
			Model:           "gpt-5.4",
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		},
	}

	agent.ApplyAgentSessionHandle(handle)

	if agent.SessionID != "app-session-1" {
		t.Fatalf("SessionID = %q, want app-session-1", agent.SessionID)
	}
	if agent.CodexSessionID != "codex-thread-1" {
		t.Fatalf("CodexSessionID = %q, want codex-thread-1", agent.CodexSessionID)
	}
	if agent.CodexProjectDirID != "codex-project-1" {
		t.Fatalf("CodexProjectDirID = %q, want codex-project-1", agent.CodexProjectDirID)
	}
	if got := agent.CurrentAgentSessionHandle(); got == nil || got.Provider.NativeSessionID != "codex-thread-1" {
		t.Fatalf("CurrentAgentSessionHandle = %#v", got)
	}
}

func TestAgentSessionHandleApplyPreservesConfiguredModel(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderClaudeCode,
		ModelID:  "claude-sonnet-5",
	}
	handle := &AgentSessionHandle{
		SessionID: "pulse-session",
		Provider: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "builder-conversation",
			Model:           "claude-opus-4-8",
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		},
	}

	agent.ApplyAgentSessionHandle(handle)

	if agent.ModelID != "claude-sonnet-5" {
		t.Fatalf("ModelID = %q, want configured Pulse model", agent.ModelID)
	}
	if agent.CodingProviderSessionHandle.Model != "claude-sonnet-5" {
		t.Fatalf("stored handle model = %q, want configured Pulse model", agent.CodingProviderSessionHandle.Model)
	}
	if agent.ClaudeCodeSessionID != "builder-conversation" {
		t.Fatalf("ClaudeCodeSessionID = %q, want restored conversation", agent.ClaudeCodeSessionID)
	}
}

func TestCodingProviderContinuationHandleUsesRequestedModel(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderClaudeCode,
		ModelID:               "claude-sonnet-5",
		CodingAgentWorkingDir: "/tmp/work",
		CodingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "builder-conversation",
			Model:           "claude-opus-4-8",
		},
	}

	handle, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, "claude-sonnet-5")
	if !ok {
		t.Fatal("expected continuation handle")
	}
	if handle.Model != "claude-sonnet-5" {
		t.Fatalf("continuation model = %q, want requested Pulse model", handle.Model)
	}
}

func TestCodingProviderContinuationHandleForModelRequiresMatchingNativeHandle(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderClaudeCode,
		ModelID:               "claude-sonnet-4-6",
		CodingAgentWorkingDir: "/tmp/work",
		CodingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "claude-native",
		},
	}

	handle, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	if !ok {
		t.Fatal("expected continuation handle")
	}
	if handle.WorkingDir != "/tmp/work" {
		t.Fatalf("WorkingDir = %q, want /tmp/work", handle.WorkingDir)
	}
	if handle.Model != "claude-sonnet-4-6" {
		t.Fatalf("Model = %q, want claude-sonnet-4-6", handle.Model)
	}

	if _, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderCodexCLI, "gpt-5.4"); ok {
		t.Fatal("expected provider mismatch to be rejected")
	}

	agent.CodingProviderSessionHandle.NativeSessionID = ""
	if _, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, "claude-sonnet-4-6"); ok {
		t.Fatal("expected missing native session id to be rejected")
	}
}

func TestCodingProviderContinuationHandleAcceptsPiNativeResume(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderPiCLI,
		ModelID:               "google/gemini-3.5-flash",
		CodingAgentWorkingDir: "/tmp/pi-work",
		CodingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderPiCLI),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "owner-session",
			TmuxSession:     "mlp-pi-cli-int-owner-session",
			WorkingDir:      "/tmp/pi-work",
		},
	}

	handle, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderPiCLI, "google/gemini-3.5-flash")
	if !ok {
		t.Fatal("expected pi-cli provider-native continuation")
	}
	if handle.NativeSessionID != "owner-session" || handle.WorkingDir != "/tmp/pi-work" {
		t.Fatalf("Pi continuation handle = %#v", handle)
	}
}

func TestLatestHumanMessageTextForProviderContinuation(t *testing.T) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "system"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "old message"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "old response"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "new", "message"),
	}
	got, ok := latestHumanMessageTextForProviderContinuation(messages)
	if !ok {
		t.Fatal("expected latest human message")
	}
	if got != "new\nmessage" {
		t.Fatalf("latest message = %q, want new\\nmessage", got)
	}
}
