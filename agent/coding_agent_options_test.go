package mcpagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	claudecode "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/geminicli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
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
			name: "gemini structured contract gets working dir only",
			agent: &Agent{
				provider:                           llm.ProviderGeminiCLI,
				SessionID:                          "chat-session-3",
				GeminiPersistentInteractiveSession: true,
				CodingAgentWorkingDir:              "/tmp/gemini-chat",
			},
			wantWorkingKey: geminicli.MetadataKeyWorkingDir,
			wantWorkingDir: "/tmp/gemini-chat",
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
			name: "agy persistent chat",
			agent: &Agent{
				provider:                        llm.ProviderAgyCLI,
				SessionID:                       "chat-session-5",
				AgyPersistentInteractiveSession: true,
				CodingAgentWorkingDir:           "/tmp/agy-chat",
			},
			wantSessionKey:  agycli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  agycli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-5",
			wantPersistence: true,
			wantWorkingKey:  agycli.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/agy-chat",
		},
		{
			name: "codex workflow uses bounded interactive lifecycle",
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
			name: "pi persistent chat",
			agent: &Agent{
				provider:                       llm.ProviderPiCLI,
				SessionID:                      "chat-session-6",
				PiPersistentInteractiveSession: true,
				CodingAgentWorkingDir:          "/tmp/pi-chat",
			},
			wantSessionKey:  picli.MetadataKeyInteractiveSessionID,
			wantPersistKey:  picli.MetadataKeyPersistentInteractive,
			wantSessionID:   "chat-session-6",
			wantPersistence: true,
			wantWorkingKey:  picli.MetadataKeyWorkingDir,
			wantWorkingDir:  "/tmp/pi-chat",
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

func TestAppendCodingAgentWorkingDirOptionCleansInactiveGeneratedArtifacts(t *testing.T) {
	workDir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(workDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite(".claude/skills/system-tools/SKILL.md", "generated")
	mustWrite(".pi/skills/system-tools/SKILL.md", "generated")
	mustWrite(".pi/APPEND_SYSTEM.md", "<!-- mlp-session-instructions: orchestrator-generated -->\n")
	mustWrite(".pi/mcp.json", `{"mcpServers":{"api-bridge":{"command":"mcpbridge"}}}`)
	mustWrite(".agents/skills/system-tools/SKILL.md", "generated")
	mustWrite(".agents/worker/keep.txt", "background runtime")
	mustWrite("CLAUDE.md", "<!-- mlp-session-instructions: orchestrator-generated -->\n")
	mustWrite("AGENTS.md", "<!-- mlp-session-instructions: orchestrator-generated -->\n")

	agent := &Agent{
		provider:                           llm.ProviderCursorCLI,
		SessionID:                          "cursor-session",
		CursorPersistentInteractiveSession: true,
		CodingAgentWorkingDir:              workDir,
	}
	_ = metadataFromCallOptions(agent.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli"))

	for _, rel := range []string{".claude", ".pi", ".agents/skills", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(workDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed as inactive generated artifact, stat err=%v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, ".agents")); !os.IsNotExist(err) {
		t.Fatalf(".agents should be removed as an inactive provider artifact, stat err=%v", err)
	}
}

func TestAppendCodingAgentWorkingDirOptionRemovesInactiveProviderDirsInWorkflow(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "workspace-docs", "Workflow", "demo")
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(workDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite(".claude/skills/system-tools/SKILL.md", "generated")
	mustWrite(".pi/APPEND_SYSTEM.md", "generated")
	mustWrite(".gemini/settings.json", `{"generated":true}`)
	mustWrite(".agents/skills/system-tools/SKILL.md", "generated")
	mustWrite(".agents/save-0001/marker.txt", "stale runtime")
	mustWrite(".cursor/mcp.json", `{"mcpServers":{"api-bridge":{"command":"mcpbridge"}}}`)

	agent := &Agent{
		provider:              llm.ProviderCursorCLI,
		ModelID:               "cursor-cli",
		SessionID:             "cursor-session",
		CodingAgentWorkingDir: workDir,
	}
	_ = metadataFromCallOptions(agent.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli"))

	for _, rel := range []string{".claude", ".pi", ".gemini", ".agents"} {
		if _, err := os.Stat(filepath.Join(workDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed when Cursor starts in a workflow folder, stat err=%v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, ".cursor", "mcp.json")); err != nil {
		t.Fatalf("active cursor folder should be preserved before cursor adapter rewrites it: %v", err)
	}
}

func TestCleanupInactiveCodingAgentArtifactsRemovesInactiveCursorDir(t *testing.T) {
	workDir := t.TempDir()
	cursorDir := filepath.Join(workDir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	userMCP := filepath.Join(cursorDir, "mcp.json")
	body := `{"mcpServers":{"github":{"command":"docker","args":["run","github-mcp"]}}}`
	if err := os.WriteFile(userMCP, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cursorDir, "skills", "system-tools"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "skills", "system-tools", "SKILL.md"), []byte("generated"), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupInactiveCodingAgentProjectArtifacts(workDir, llm.ProviderClaudeCode)

	if _, err := os.Stat(cursorDir); !os.IsNotExist(err) {
		t.Fatalf("inactive cursor dir should be removed, stat err=%v", err)
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
		{name: "agy cli", provider: llm.ProviderAgyCLI, metadataKey: agycli.MetadataKeyWorkingDir},
		{name: "pi cli", provider: llm.ProviderPiCLI, metadataKey: picli.MetadataKeyWorkingDir},
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

func TestCodingAgentIntegrationAppenderCoverage(t *testing.T) {
	for _, contract := range llm.CodingAgentProviderContracts() {
		if !contract.UsesMCPBridge {
			continue
		}
		if _, ok := codingAgentIntegrationAppenders[contract.Provider]; !ok {
			t.Errorf("provider %s uses MCP bridge but has no executeLLM integration appender", contract.Provider)
		}
	}
	for provider := range codingAgentIntegrationAppenders {
		contract, ok := llm.GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Errorf("integration appender has %s but no coding-agent contract", provider)
			continue
		}
		if !contract.UsesMCPBridge {
			t.Errorf("integration appender has %s but contract does not use MCP bridge", provider)
		}
	}
}

func TestForceStructuredCodingAgentSuppressesGeminiInteractiveSessionMetadata(t *testing.T) {
	agent := &Agent{
		provider:                           llm.ProviderGeminiCLI,
		SessionID:                          "workflow-step-session",
		GeminiPersistentInteractiveSession: true,
		ForceStructuredCodingAgent:         true,
		CodingAgentWorkingDir:              "/tmp/workflow-step",
	}

	got := metadataFromCallOptions(agent.appendCodingAgentInteractiveOptions(nil))
	if _, ok := got[geminicli.MetadataKeyInteractiveSessionID]; ok {
		t.Fatalf("Gemini interactive session metadata present despite ForceStructuredCodingAgent: %#v", got)
	}
	if _, ok := got[geminicli.MetadataKeyPersistentInteractive]; ok {
		t.Fatalf("Gemini persistent interactive metadata present despite ForceStructuredCodingAgent: %#v", got)
	}
	if got[geminicli.MetadataKeyWorkingDir] != "/tmp/workflow-step" {
		t.Fatalf("Gemini working dir metadata = %#v, want /tmp/workflow-step", got[geminicli.MetadataKeyWorkingDir])
	}
}

func TestAppendCodingAgentInteractiveOptionsForActualFallbackProvider(t *testing.T) {
	agent := &Agent{
		provider:                           llm.ProviderOpenAI,
		SessionID:                          "fallback-session",
		CodingAgentWorkingDir:              "/tmp/fallback-workdir",
		GeminiPersistentInteractiveSession: true,
	}

	got := metadataFromCallOptions(agent.appendCodingAgentInteractiveOptionsForProvider(nil, llm.ProviderGeminiCLI, "gemini-3.1-flash-lite"))

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

// Cursor's --mode ask is a conversational stance that refuses natural-language
// writes with "Switch to Agent mode", which makes the chat unusable for any
// turn that requires writes. CursorBridgeToolsMode must NOT set --mode ask
// from the interactive-session option layer. Cursor runs in default agent mode;
// MCP bridge config, --approve-mcps, and deny-builtin hooks are mounted later
// by appendCursorCLIIntegrationOptions.
func TestCursorBridgeToolsModeDoesNotForceAskMode(t *testing.T) {
	agent := &Agent{
		provider:                           llm.ProviderCursorCLI,
		SessionID:                          "chat-session-bridge",
		CursorPersistentInteractiveSession: true,
		CursorBridgeToolsMode:              true,
	}

	got := metadataFromCallOptions(agent.appendCodingAgentInteractiveOptions(nil))

	if mode, ok := got[cursorcli.MetadataKeyMode]; ok {
		t.Fatalf("mode metadata should NOT be set under bridge tools mode (ask mode breaks natural-language writes), got %#v", mode)
	}
	if approve, ok := got[cursorcli.MetadataKeyApproveMCPs]; ok {
		t.Fatalf("approve-mcps metadata should be added by Cursor integration options, not interactive options; got %#v", approve)
	}
	if approveWeb, ok := got[cursorcli.MetadataKeyAutoApproveWebSearch].(bool); !ok || !approveWeb {
		t.Fatalf("auto web approval metadata = %#v, want true", got[cursorcli.MetadataKeyAutoApproveWebSearch])
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
