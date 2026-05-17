package mcpagent

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecode "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/geminicli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/opencodecli"
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
			name: "cursor persistent chat",
			agent: &Agent{
				provider:                           llm.ProviderCursorCLI,
				SessionID:                          "chat-session-4",
				CursorPersistentInteractiveSession: true,
				CodingAgentWorkingDir:              "/tmp/cursor-chat",
			},
			wantSessionKey:  cursorcli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  cursorcli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-4",
			wantPersistence: true,
			wantWorkingKey:  cursorcli.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/cursor-chat",
		},
		{
			name: "codex workflow uses persistent interactive lifecycle",
			agent: &Agent{
				provider:                          llm.ProviderCodexCLI,
				SessionID:                         "workflow-session",
				CodexPersistentInteractiveSession: false,
			},
			wantSessionKey:  codexcli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  codexcli.MetadataKeyPersistentInteractive,
			wantSessionID:   "workflow-session",
			wantPersistence: true,
		},
		{
			name: "opencode persistent chat",
			agent: &Agent{
				provider:              llm.ProviderOpenCodeCLI,
				SessionID:             "chat-session-5",
				CodingAgentWorkingDir: "/tmp/opencode-chat",
			},
			wantSessionKey:  opencodecli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  opencodecli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-5",
			wantPersistence: true,
			wantWorkingKey:  opencodecli.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/opencode-chat",
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
		{name: "cursor cli", provider: llm.ProviderCursorCLI, metadataKey: cursorcli.MetadataKeyWorkingDir},
		{name: "opencode cli", provider: llm.ProviderOpenCodeCLI, metadataKey: opencodecli.MetadataKeyWorkingDir},
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

func TestAppendCodingAgentInteractiveOptionsForActualFallbackProvider(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderOpenAI,
		SessionID:             "fallback-session",
		CodingAgentWorkingDir: "/tmp/fallback-workdir",
	}

	got := metadataFromCallOptions(agent.appendCodingAgentInteractiveOptionsForProvider(nil, llm.ProviderGeminiCLI, "gemini-3.1-flash-lite"))

	if got[geminicli.MetadataKeyInteractiveSessionID] != "fallback-session" {
		t.Fatalf("Gemini fallback session metadata = %#v, want fallback-session", got[geminicli.MetadataKeyInteractiveSessionID])
	}
	if got[geminicli.MetadataKeyPersistentInteractive] != true {
		t.Fatalf("Gemini fallback persistent metadata = %#v, want true", got[geminicli.MetadataKeyPersistentInteractive])
	}
	if got[geminicli.MetadataKeyWorkingDir] != "/tmp/fallback-workdir" {
		t.Fatalf("Gemini fallback working dir = %#v, want /tmp/fallback-workdir", got[geminicli.MetadataKeyWorkingDir])
	}
}

func TestAnnotateUnifiedCompletionEventMarksCodingAgentTerminalFormat(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderCodexCLI,
		ModelID:  "gpt-5.3-codex-spark",
	}
	event := events.NewUnifiedCompletionEvent("simple", "simple", "question", "answer", "completed", time.Second, 1)

	agent.annotateUnifiedCompletionEvent(event)

	if event.Metadata["provider"] != string(llm.ProviderCodexCLI) {
		t.Fatalf("provider metadata = %#v, want %q", event.Metadata["provider"], llm.ProviderCodexCLI)
	}
	if event.Metadata["model_id"] != "gpt-5.3-codex-spark" {
		t.Fatalf("model_id metadata = %#v, want gpt-5.3-codex-spark", event.Metadata["model_id"])
	}
	if event.Metadata["coding_agent_terminal_format"] != true {
		t.Fatalf("coding_agent_terminal_format metadata = %#v, want true", event.Metadata["coding_agent_terminal_format"])
	}
}

func TestEnsureGeminiProjectDirIDStableFromSession(t *testing.T) {
	first := &Agent{SessionID: " chat/session 123 "}
	second := &Agent{SessionID: "chat/session 123"}

	firstID := first.ensureGeminiProjectDirID()
	secondID := second.ensureGeminiProjectDirID()

	if firstID == "" {
		t.Fatal("Gemini project dir ID should not be empty")
	}
	if firstID != secondID {
		t.Fatalf("Gemini project dir ID should be deterministic for the same session, got %q and %q", firstID, secondID)
	}
	if strings.ContainsAny(firstID, "/ ") {
		t.Fatalf("Gemini project dir ID %q should be filesystem-safe", firstID)
	}

	existing := &Agent{SessionID: "different-session", GeminiProjectDirID: "existing-project-dir"}
	if got := existing.ensureGeminiProjectDirID(); got != "existing-project-dir" {
		t.Fatalf("existing Gemini project dir ID changed to %q", got)
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
