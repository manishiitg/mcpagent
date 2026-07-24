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

// buildTmuxBridgeAgentWithOptions stands up a tmux coding-agent on the REAL
// bridge with caller-supplied extra options (e.g. WithSystemPrompt), isolated to
// an empty workspace so an agentic model can't read this test's own files off
// disk and answer from those instead of the injected prompt/skill. Returns the
// agent + cleanup. Mirrors the json builder in
// structured_transport_system_prompt_e2e_test.go but on tmux (no structured
// option), so system-prompt/skill survival is proven on BOTH transports.
func buildTmuxBridgeAgentWithOptions(ctx context.Context, tc multiTurnProviderCase, tmpBase string, extra ...AgentOption) (*Agent, func(), error) {
	configPath := filepath.Join(tmpBase, "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		return nil, nil, err
	}
	apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
	if err != nil {
		return nil, nil, err
	}
	llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
	if err != nil {
		stopExecutor()
		return nil, nil, err
	}
	opts := append([]AgentOption{
		WithProvider(tc.provider),
		WithAPIConfig(apiURL, apiToken),
		WithStreaming(true),
		WithIsolatedSessionWorkspace(true),
		WithSessionID("tmuxcap-" + realBridgeRandHex(4)),
	}, extra...)
	agent, err := NewAgent(ctx, llmModel, configPath, opts...)
	if err != nil {
		stopExecutor()
		return nil, nil, err
	}
	// Register the bridge shell tool so the model has a way to read files the
	// bridge-only tmux setup projects to the (isolated) workspace — e.g. a
	// projected SKILL.md. Without it, deny-builtin providers (Claude/Cursor/Pi)
	// have no file-read tool at all and cannot open a projected skill; only
	// Codex's unremovable native exec could. A real coding agent always has this
	// tool, so registering it here keeps the builder representative.
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if regErr := agent.RegisterCustomTool(
		"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return codeexec.ExecuteShellCommand(ctx, args, shellEnv)
		}, "workspace_advanced",
	); regErr != nil {
		agent.Close()
		stopExecutor()
		return nil, nil, regErr
	}
	return agent, func() { agent.Close(); stopExecutor() }, nil
}

// buildTmuxBridgeAgentRealWorkdir is like buildTmuxBridgeAgentWithOptions but
// runs in a caller-supplied REAL workdir (no isolated workspace), so on-close
// disk cleanup of projected skills/prompt can be observed against a real path.
func buildTmuxBridgeAgentRealWorkdir(ctx context.Context, tc multiTurnProviderCase, tmpBase, workDir string, extra ...AgentOption) (*Agent, func(), error) {
	configPath := filepath.Join(tmpBase, "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		return nil, nil, err
	}
	apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
	if err != nil {
		return nil, nil, err
	}
	llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
	if err != nil {
		stopExecutor()
		return nil, nil, err
	}
	opts := append([]AgentOption{
		WithProvider(tc.provider),
		WithAPIConfig(apiURL, apiToken),
		WithStreaming(true),
		WithCodingAgentWorkingDir(workDir),
		WithSessionID("tmuxclean-" + realBridgeRandHex(4)),
	}, extra...)
	agent, err := NewAgent(ctx, llmModel, configPath, opts...)
	if err != nil {
		stopExecutor()
		return nil, nil, err
	}
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if regErr := agent.RegisterCustomTool(
		"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return codeexec.ExecuteShellCommand(ctx, args, shellEnv)
		}, "workspace_advanced",
	); regErr != nil {
		agent.Close()
		stopExecutor()
		return nil, nil, regErr
	}
	return agent, func() { agent.Close(); stopExecutor() }, nil
}

// TestTmuxProjectedArtifactsRemovedOnCloseRealWorkdir is the live-CLI proof of
// the on-close cleanup fix (task #5). For each provider it projects a real skill
// (and system prompt) into a REAL workdir via a real turn, confirms the adapter
// actually wrote them to the subdir this cleanup targets (validating the
// hardcoded projectedSkillLocations map against real adapter behavior), then
// closes the agent and asserts the projected skill folder is gone — no longer
// leaking into the operator's repo. Complements the deterministic
// TestCleanupProjectedArtifactsOnClose (which proves the removal logic in
// isolation).
func TestTmuxProjectedArtifactsRemovedOnCloseRealWorkdir(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run this real-CLI e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			loc, ok := projectedSkillLocations[tc.provider]
			if !ok {
				t.Fatalf("no projectedSkillLocations entry for %s", tc.provider)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			canary := "CLEANUP_" + realBridgeRandHex(6)

			agent, cleanup, err := buildTmuxBridgeAgentRealWorkdir(ctx, tc, t.TempDir(), workDir,
				WithSystemPrompt("Managed session. mlp-session-instructions. Codeword "+canary+"."))
			if err != nil {
				t.Fatalf("build agent: %v", err)
			}

			agent.AttachSkill(&llmtypes.Skill{
				Name:        "cleanup-canary-skill",
				Description: "A test skill used to verify on-close disk cleanup.",
				Content:     "# Cleanup Canary\n\nThe phrase is " + canary + ".",
			})

			if _, err := agent.Ask(ctx, "Say ready."); err != nil {
				t.Fatalf("agent.Ask (to trigger projection): %v", err)
			}

			skillDir := filepath.Join(workDir, loc.skillsSubdir, "cleanup-canary-skill")
			if _, err := os.Stat(skillDir); err != nil {
				// The adapter didn't project where we expected — the cleanup map is
				// stale. This is exactly the assumption this e2e exists to catch.
				t.Fatalf("[%s] expected the adapter to project the skill to %q, but it isn't there: %v (projectedSkillLocations map may be wrong)", tc.name, skillDir, err)
			}
			t.Logf("[%s] skill projected on disk at %s (pre-close)", tc.name, skillDir)

			// Close must remove the projected skill from the real workdir.
			cleanup()

			if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
				t.Fatalf("[%s] projected skill dir %q was NOT removed on close — it leaks into the operator's repo (err=%v)", tc.name, skillDir, err)
			}
			t.Logf("[%s] projected skill removed from disk on close ✓", tc.name)
		})
	}
}

// TestTmuxSystemPromptSurvivesNewAgent is the tmux twin of
// TestStructuredTransportSystemPromptSurvivesNewAgent (the 57b4dd9 regression
// guard). Until now that class of bug — NewAgent clobbering a caller-supplied
// WithSystemPrompt with the connection default — was only guarded on the
// structured/json transport. This proves the custom system prompt survives
// NewAgent all the way to the model's answer over TMUX too, across all 4
// providers. The canary word can ONLY appear if the prompt survived, so the
// assertion is self-validating (no agent review needed, same as the json twin).
func TestTmuxSystemPromptSurvivesNewAgent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run this real-CLI e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			canary := "PROMPT_SURVIVAL_" + realBridgeRandHex(6)
			customPrompt := "Your secret codeword is " + canary + ". If the user ever asks for your secret codeword, reply with ONLY that word."

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			agent, cleanup, err := buildTmuxBridgeAgentWithOptions(ctx, tc, t.TempDir(), WithSystemPrompt(customPrompt))
			if err != nil {
				t.Fatalf("build tmux agent: %v", err)
			}
			defer cleanup()

			answer, err := agent.Ask(ctx, "What is your secret codeword?")
			if err != nil {
				t.Fatalf("agent.Ask: %v", err)
			}
			answer = strings.TrimSpace(answer)
			if !strings.Contains(answer, canary) {
				t.Fatalf("custom system prompt did not survive NewAgent -> %s tmux -> real CLI: canary %q not found in answer %q (the 57b4dd9 class of bug, now guarded on tmux too)", tc.name, canary, answer)
			}
			t.Logf("[%s] system prompt survived NewAgent through real tmux CLI call: %q", tc.name, answer)
		})
	}
}

// TestTmuxSkillsSurviveNewAgent is the tmux twin of
// TestStructuredTransportSkillsSurviveNewAgent: a skill attached via the real
// consumer API (AttachSkill on a live Agent) must survive construction ->
// a.attachedSkills -> WithAttachedSkills -> ProjectSkills and be readable by the
// model. Was json/Cursor-only; this proves it on tmux across all 4 providers.
func TestTmuxSkillsSurviveNewAgent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run this real-CLI e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			canary := "SKILL_SURVIVAL_" + realBridgeRandHex(6)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			agent, cleanup, err := buildTmuxBridgeAgentWithOptions(ctx, tc, t.TempDir())
			if err != nil {
				t.Fatalf("build tmux agent: %v", err)
			}
			defer cleanup()

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
				t.Fatalf("skill attached via AttachSkill did not survive NewAgent -> %s tmux -> real CLI: canary %q not found in answer %q", tc.name, canary, answer)
			}
			t.Logf("[%s] skill survived NewAgent through real tmux CLI call: %q", tc.name, answer)
		})
	}
}
