package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecode "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/geminicli"
)

func TestSessionIDExtractionFromGenerationInfo(t *testing.T) {
	tests := []struct {
		name         string
		provider     llm.Provider
		additional   map[string]interface{}
		wantClaude   string
		wantGemini   string
		wantGeminiPD string
		wantCodex    string
	}{
		{
			name:     "claude code session ID extracted",
			provider: llm.ProviderClaudeCode,
			additional: map[string]interface{}{
				"claude_code_session_id": "claude-sess-abc123",
				"provider":              "claude-code",
			},
			wantClaude: "claude-sess-abc123",
		},
		{
			name:     "gemini session ID and project dir extracted",
			provider: llm.ProviderGeminiCLI,
			additional: map[string]interface{}{
				"gemini_session_id":     "gemini-sess-xyz789",
				"gemini_project_dir_id": "proj-dir-456",
				"provider":              "gemini-cli",
			},
			wantGemini:   "gemini-sess-xyz789",
			wantGeminiPD: "proj-dir-456",
		},
		{
			name:     "gemini session ID without project dir",
			provider: llm.ProviderGeminiCLI,
			additional: map[string]interface{}{
				"gemini_session_id": "gemini-sess-only",
			},
			wantGemini: "gemini-sess-only",
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
			name:     "empty session ID not stored",
			provider: llm.ProviderClaudeCode,
			additional: map[string]interface{}{
				"claude_code_session_id": "",
			},
			wantClaude: "",
		},
		{
			name:     "missing key not stored",
			provider: llm.ProviderGeminiCLI,
			additional: map[string]interface{}{
				"provider": "gemini-cli",
			},
			wantGemini: "",
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
			if agent.GeminiSessionID != tt.wantGemini {
				t.Errorf("GeminiSessionID = %q, want %q", agent.GeminiSessionID, tt.wantGemini)
			}
			if agent.GeminiProjectDirID != tt.wantGeminiPD {
				t.Errorf("GeminiProjectDirID = %q, want %q", agent.GeminiProjectDirID, tt.wantGeminiPD)
			}
			if agent.CodexSessionID != tt.wantCodex {
				t.Errorf("CodexSessionID = %q, want %q", agent.CodexSessionID, tt.wantCodex)
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
		geminiSessionID                   string
		geminiProjectDirID                string
		codexSessionID                    string
		sessionID                         string
		codexPersistentInteractiveSession bool
		wantResumeKey                     string
		wantResumeValue                   string
		wantProjectDirKey                 string
		wantProjectDirValue               string
		wantSkipResume                    bool
	}{
		{
			name:            "claude code passes resume session ID",
			provider:        llm.ProviderClaudeCode,
			claudeSessionID: "claude-resume-id",
			wantResumeKey:   claudecode.MetadataKeyResumeSessionID,
			wantResumeValue: "claude-resume-id",
		},
		{
			name:            "gemini passes resume session ID",
			provider:        llm.ProviderGeminiCLI,
			geminiSessionID: "gemini-resume-id",
			wantResumeKey:   geminicli.MetadataKeyResumeSessionID,
			wantResumeValue: "gemini-resume-id",
		},
		{
			name:               "gemini passes project dir ID when no working dir",
			provider:           llm.ProviderGeminiCLI,
			geminiSessionID:    "gemini-resume-id",
			geminiProjectDirID: "proj-dir-id",
			wantResumeKey:      geminicli.MetadataKeyResumeSessionID,
			wantResumeValue:    "gemini-resume-id",
			wantProjectDirKey:  geminicli.MetadataKeyProjectDirID,
			wantProjectDirValue: "proj-dir-id",
		},
		{
			name:           "codex passes resume thread ID when no persistent interactive",
			provider:       llm.ProviderCodexCLI,
			codexSessionID: "codex-thread-id",
			wantResumeKey:  codexcli.MetadataKeyResumeSessionID,
			wantResumeValue: "codex-thread-id",
		},
		{
			// Codex CLI uses the persistent tmux session for resume, so
			// when persistent interactive is fully wired up
			// (SessionID + tmux-capable model + explicit
			// CodexPersistentInteractiveSession flag),
			// buildStructuredResumeOptions must NOT emit a
			// MetadataKeyResumeSessionID — the tmux session itself
			// carries continuity.
			name:                              "codex skips resume when persistent interactive enabled",
			provider:                          llm.ProviderCodexCLI,
			modelID:                           "gpt-5.3-codex-spark",
			codexSessionID:                    "codex-thread-id",
			sessionID:                         "app-session-id",
			codexPersistentInteractiveSession: true,
			wantSkipResume:                    true,
		},
		{
			name:     "claude code no resume when session ID empty",
			provider: llm.ProviderClaudeCode,
		},
		{
			name:     "gemini no resume when session ID empty",
			provider: llm.ProviderGeminiCLI,
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
				GeminiSessionID:                   tt.geminiSessionID,
				GeminiProjectDirID:                tt.geminiProjectDirID,
				CodexSessionID:                    tt.codexSessionID,
				CodexPersistentInteractiveSession: tt.codexPersistentInteractiveSession,
			}

			opts := agent.buildStructuredResumeOptions()
			meta := metadataFromCallOptions(opts)

			if tt.wantSkipResume {
				if _, hasResume := meta[codexcli.MetadataKeyResumeSessionID]; hasResume {
					t.Fatalf("expected resume to be skipped for Codex persistent interactive, but found %v", meta[codexcli.MetadataKeyResumeSessionID])
				}
				return
			}

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
			name:       "gemini cli",
			provider:   llm.ProviderGeminiCLI,
			sessionKey: "gemini_session_id",
			resumeKey:  geminicli.MetadataKeyResumeSessionID,
		},
		{
			name:       "codex cli",
			provider:   llm.ProviderCodexCLI,
			sessionKey: "codex_thread_id",
			resumeKey:  codexcli.MetadataKeyResumeSessionID,
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
