package mcpagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// structuredTransportProviderCase is one provider's real-CLI binary,
// mcpagent.Provider, WithXxxStructuredTransport option, and model ID — the
// three things that differ per provider in the tests below.
type structuredTransportProviderCase struct {
	name             string
	binary           string
	provider         llm.Provider
	modelID          string
	structuredOption AgentOption
}

var structuredTransportProviderCases = []structuredTransportProviderCase{
	{"Cursor", "cursor-agent", llm.ProviderCursorCLI, "cursor-cli", WithCursorStructuredTransport(true)},
	{"Codex", "codex", llm.ProviderCodexCLI, "codex-cli", WithCodexStructuredTransport(true)},
	{"Pi", "pi", llm.ProviderPiCLI, "pi-cli", WithPiStructuredTransport(true)},
}

// TestStructuredTransportSystemPromptSurvivesNewAgent is the mcpagent-layer
// regression guard for commit 57b4dd9 ("agent: don't clobber a custom system
// prompt with the connection default"): NewAgent used to unconditionally
// overwrite a caller-supplied WithSystemPrompt with a connection-derived
// default, silently discarding it. An adapter-layer test (in
// multi-llm-provider-go) cannot see this class of bug — it hand-constructs
// the message list itself and calls the adapter directly, never going
// through NewAgent/WithSystemPrompt at all. This test does the opposite: it
// builds a real Agent the way an actual mcpagent consumer would (NewAgent +
// WithSystemPrompt, on the real bridge — each provider's
// appendXxxCLIIntegrationOptions requires it unconditionally, so a
// bridgeless agent can't even reach the structured-transport option) and
// drives it through the real CLI under WithXxxStructuredTransport(true),
// asserting the custom system prompt's canary word survives all the way to
// the model's answer. Runs the same assertion across Cursor, Codex, and Pi —
// the class of bug is equally reachable through any of the three structured
// adapters, and until this test existed only Cursor had been checked.
//
// See docs/layer_test_coverage.html and docs/transport_tradeoffs_notes.html
// §test-layer-policy in the multi-llm-provider-go repo for the fuller
// adapter-vs-library test-ownership rule this test exists to satisfy.
func TestStructuredTransportSystemPromptSurvivesNewAgent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run this real-CLI e2e")
	}

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			canary := "PROMPT_SURVIVAL_" + realBridgeRandHex(6)
			customPrompt := "Your secret codeword is " + canary + ". If the user ever asks for your secret codeword, reply with ONLY that word."

			configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
			if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
			if err != nil {
				t.Fatalf("bootRealExecutor: %v", err)
			}
			defer stopExecutor()

			llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
			if err != nil {
				t.Fatalf("InitializeLLM: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			agent, err := NewAgent(ctx, llmModel, configPath,
				WithProvider(tc.provider),
				WithAPIConfig(apiURL, apiToken),
				WithSystemPrompt(customPrompt),
				tc.structuredOption,
				// Isolate the CLI process's cwd to an empty tmp dir. Without this
				// the process inherits the test binary's cwd (this repo checkout),
				// and a sufficiently agentic model can go read this very test file
				// off disk and answer from that instead of the injected system
				// prompt — a false negative unrelated to the 57b4dd9 class of bug
				// this test guards.
				WithIsolatedSessionWorkspace(true),
			)
			if err != nil {
				t.Fatalf("NewAgent: %v", err)
			}
			defer agent.Close()

			answer, err := agent.Ask(ctx, "What is your secret codeword?")
			if err != nil {
				t.Fatalf("agent.Ask: %v", err)
			}
			answer = strings.TrimSpace(answer)
			if !strings.Contains(answer, canary) {
				t.Fatalf("custom system prompt did not survive NewAgent -> %s structured transport -> real CLI: canary %q not found in answer %q (this is exactly the class of bug fixed in commit 57b4dd9 — regression if it recurs)", tc.name, canary, answer)
			}
			t.Logf("[%s] system prompt survived NewAgent through real structured-transport CLI call: %q", tc.name, answer)
		})
	}
}

// TestStructuredTransportSkillsSurviveNewAgent proves the other half of the
// same construction path: Agent.AttachSkill -> a.attachedSkills ->
// llmtypes.WithAttachedSkills (threaded centrally in executeLLMInner) ->
// each adapter's ProjectSkills. Layer 1 already P0-tests that ProjectSkills
// writes a skill to disk correctly for a hand-built CallOptions list; nothing
// before this test proved a skill attached via the real mcpagent consumer
// API (AttachSkill on a live Agent, not a raw CallOption) survives
// construction and is actually readable by the model. Now table-driven across
// all 3 structured providers (Codex/Cursor/Pi — Claude has no structured lane).
// Registers the bridge shell tool so a deny-builtin provider still has a way to
// open the projected SKILL.md (the tmux twin proved that without a file-read
// tool only Codex's unremovable native exec could read a projected skill).
func TestStructuredTransportSkillsSurviveNewAgent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run this real-CLI e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			canary := "SKILL_SURVIVAL_" + realBridgeRandHex(6)

			configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
			if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
			if err != nil {
				t.Fatalf("bootRealExecutor: %v", err)
			}
			defer stopExecutor()

			llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
			if err != nil {
				t.Fatalf("InitializeLLM: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			agent, err := NewAgent(ctx, llmModel, configPath,
				WithProvider(tc.provider),
				WithAPIConfig(apiURL, apiToken),
				tc.structuredOption,
				WithIsolatedSessionWorkspace(true),
			)
			if err != nil {
				t.Fatalf("NewAgent: %v", err)
			}
			defer agent.Close()

			shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
			if regErr := agent.RegisterCustomTool(
				"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
				func(ctx context.Context, args map[string]interface{}) (string, error) {
					return codeexec.ExecuteShellCommand(ctx, args, shellEnv)
				}, "workspace_advanced",
			); regErr != nil {
				t.Fatalf("RegisterCustomTool: %v", regErr)
			}

			agent.AttachSkill(&llmtypes.Skill{
				Name:        "canary-skill",
				Description: "A test skill that reveals a secret phrase when read.",
				Content:     "# Canary Skill\n\nWhen asked for the canary skill's secret phrase, reply with ONLY this exact word: " + canary,
			})

			answer, err := agent.Ask(ctx, "Read the canary-skill skill and tell me its secret phrase.")
			if err != nil {
				t.Fatalf("agent.Ask: %v", err)
			}
			answer = strings.TrimSpace(answer)
			if !strings.Contains(answer, canary) {
				t.Fatalf("[%s] skill did not survive NewAgent -> structured transport -> real CLI: canary %q not found in answer %q", tc.name, canary, answer)
			}
			t.Logf("[%s] skill survived NewAgent through real structured-transport CLI call: %q", tc.name, answer)
		})
	}
}
