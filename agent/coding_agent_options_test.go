package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecode "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/geminicli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/kimi"
)

func TestAppendCodingAgentInteractiveOptions(t *testing.T) {
	tests := []struct {
		name            string
		agent           *Agent
		wantSessionKey  string
		wantPersistKey  string
		wantSessionID   string
		wantPersistence bool
		wantWorkingKey  string
		wantWorkingDir  string
	}{
		{
			name: "claude code persistent chat",
			agent: &Agent{
				provider:                               llm.ProviderClaudeCode,
				SessionID:                              " chat-session-1 ",
				ClaudeCodePersistentInteractiveSession: true,
				CodingAgentWorkingDir:                  " /tmp/user-chat ",
			},
			wantSessionKey:  claudecode.MetadataKeyInteractiveSessionID,
			wantPersistKey:  claudecode.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-1",
			wantPersistence: true,
			wantWorkingKey:  claudecode.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/user-chat",
		},
		{
			name: "codex persistent chat",
			agent: &Agent{
				provider:                          llm.ProviderCodexCLI,
				SessionID:                         "chat-session-2",
				CodexPersistentInteractiveSession: true,
				CodingAgentWorkingDir:             "/tmp/codex-chat",
			},
			wantSessionKey:  codexcli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  codexcli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-2",
			wantPersistence: true,
			wantWorkingKey:  codexcli.MetadataKeyProjectDirID,
			wantWorkingDir:  "/tmp/codex-chat",
		},
		{
			name: "gemini persistent chat",
			agent: &Agent{
				provider:                           llm.ProviderGeminiCLI,
				SessionID:                          "chat-session-3",
				GeminiPersistentInteractiveSession: true,
				CodingAgentWorkingDir:              "/tmp/gemini-chat",
			},
			wantSessionKey:  geminicli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  geminicli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-3",
			wantPersistence: true,
			wantWorkingKey:  geminicli.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/gemini-chat",
		},
		{
			name: "kimi code cli working dir without interactive session",
			agent: &Agent{
				provider:              llm.ProviderKimi,
				ModelID:               "kimi-code",
				CodingAgentWorkingDir: "/tmp/kimi-chat",
			},
			wantWorkingKey: kimi.MetadataKeyWorkingDir,
			wantWorkingDir: "/tmp/kimi-chat",
		},
		{
			name: "codex workflow keeps non-persistent lifecycle",
			agent: &Agent{
				provider:                          llm.ProviderCodexCLI,
				SessionID:                         "workflow-session",
				CodexPersistentInteractiveSession: false,
			},
			wantSessionKey:  codexcli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  codexcli.MetadataKeyPersistentInteractive,
			wantSessionID:   "workflow-session",
			wantPersistence: false,
		},
		{
			name: "missing owner session produces no coding-agent metadata",
			agent: &Agent{
				provider:                          llm.ProviderCodexCLI,
				CodexPersistentInteractiveSession: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := metadataFromCallOptions(tt.agent.appendCodingAgentInteractiveOptions(nil))

			if tt.wantSessionKey == "" && tt.wantWorkingKey == "" {
				if len(got) != 0 {
					t.Fatalf("metadata = %#v, want empty", got)
				}
				return
			}

			if tt.wantSessionKey != "" && got[tt.wantSessionKey] != tt.wantSessionID {
				t.Fatalf("session metadata %q = %#v, want %q", tt.wantSessionKey, got[tt.wantSessionKey], tt.wantSessionID)
			}
			if tt.wantPersistKey != "" {
				persistentValue, hasPersistentValue := got[tt.wantPersistKey]
				if tt.wantPersistence {
					if persistentValue != true {
						t.Fatalf("persistent metadata %q = %#v, want true", tt.wantPersistKey, persistentValue)
					}
				} else if hasPersistentValue && persistentValue != false {
					t.Fatalf("persistent metadata %q = %#v, want absent or false", tt.wantPersistKey, persistentValue)
				}
			}
			if tt.wantWorkingKey != "" && got[tt.wantWorkingKey] != tt.wantWorkingDir {
				t.Fatalf("working dir metadata %q = %#v, want %q", tt.wantWorkingKey, got[tt.wantWorkingKey], tt.wantWorkingDir)
			}
		})
	}
}

func TestCodingCLIWorkingDirOptionCoverage(t *testing.T) {
	cases := []struct {
		name        string
		provider    llm.Provider
		modelID     string
		metadataKey string
	}{
		{name: "claude code", provider: llm.ProviderClaudeCode, metadataKey: claudecode.MetadataKeyWorkingDir},
		{name: "codex cli", provider: llm.ProviderCodexCLI, metadataKey: codexcli.MetadataKeyProjectDirID},
		{name: "gemini cli", provider: llm.ProviderGeminiCLI, metadataKey: geminicli.MetadataKeyWorkingDir},
		{name: "kimi code cli", provider: llm.ProviderKimi, modelID: "kimi-code", metadataKey: kimi.MetadataKeyWorkingDir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isCodingCLIProvider(tc.provider, tc.modelID) {
				t.Fatalf("%s/%s should be recognized as a coding CLI provider", tc.provider, tc.modelID)
			}
			option, ok := codingAgentWorkingDirOptionForProvider(tc.provider, tc.modelID)
			if !ok {
				t.Fatalf("missing working-directory option for coding CLI provider=%s model=%s", tc.provider, tc.modelID)
			}
			got := metadataFromCallOptions([]llmtypes.CallOption{option("/tmp/workdir")})
			if got[tc.metadataKey] != "/tmp/workdir" {
				t.Fatalf("metadata %q = %#v, want /tmp/workdir", tc.metadataKey, got[tc.metadataKey])
			}
		})
	}
}

func TestWithClaudeCodeTransport(t *testing.T) {
	agent := &Agent{}
	WithClaudeCodeTransport(llm.ClaudeCodeTransportPrint)(agent)
	if agent.ClaudeCodeTransport != llm.ClaudeCodeTransportPrint {
		t.Fatalf("ClaudeCodeTransport = %q, want %q", agent.ClaudeCodeTransport, llm.ClaudeCodeTransportPrint)
	}
}

func metadataFromCallOptions(options []llmtypes.CallOption) map[string]interface{} {
	opts := &llmtypes.CallOptions{}
	for _, option := range options {
		option(opts)
	}
	if opts.Metadata == nil || opts.Metadata.Custom == nil {
		return map[string]interface{}{}
	}
	return opts.Metadata.Custom
}
