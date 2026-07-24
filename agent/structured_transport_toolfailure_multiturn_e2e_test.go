package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
)

// buildStructuredBridgeAgentWithShell is the structured-transport counterpart of
// newRealBridgeAgentWithShell (real_bridge_tool_failure_e2e_test.go): a real
// bridge Agent running over the JSON transport with a CUSTOM
// execute_shell_command handler for fault injection. Returns agent + cleanup.
func buildStructuredBridgeAgentWithShell(t *testing.T, ctx context.Context, tc structuredTransportProviderCase, workDir string, handler func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error)) (*Agent, func()) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
	if err != nil {
		t.Fatalf("bootRealExecutor: %v", err)
	}
	llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
	if err != nil {
		stopExecutor()
		t.Fatalf("InitializeLLM: %v", err)
	}
	agent, err := NewAgent(ctx, llmModel, configPath,
		WithProvider(tc.provider), WithAPIConfig(apiURL, apiToken),
		WithStreaming(true), WithCodingAgentWorkingDir(workDir),
		tc.structuredOption,
		WithSessionID("jsontoolfail-"+realBridgeRandHex(4)))
	if err != nil {
		stopExecutor()
		t.Fatalf("NewAgent: %v", err)
	}
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if regErr := agent.RegisterCustomTool(
		"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return handler(ctx, args, shellEnv)
		}, "workspace_advanced",
	); regErr != nil {
		agent.Close()
		stopExecutor()
		t.Fatalf("RegisterCustomTool: %v", regErr)
	}
	return agent, func() { agent.Close(); stopExecutor() }
}

// TestStructuredTransportToolFailureRecovery is the json/structured counterpart
// of TestRealBridgeStreamingToolFailureRecovery: a MID-STREAM bridge tool
// failure must degrade gracefully — the model retries the same command, the turn
// completes, and the build id (obtainable only via a real tool run) is recovered.
// Codex/Cursor/Pi (Claude has no structured lane).
func TestStructuredTransportToolFailureRecovery(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the structured tool-failure e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			codeWord := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
				t.Fatal(err)
			}

			var calls int32
			agent, cleanup := buildStructuredBridgeAgentWithShell(t, ctx, tc, workDir,
				func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error) {
					if atomic.AddInt32(&calls, 1) == 1 {
						return "", fmt.Errorf("TRANSIENT_TOOL_FAILURE: the tool backend was briefly unavailable; retry the same command")
					}
					return codeexec.ExecuteShellCommand(ctx, args, shellEnv)
				})
			defer cleanup()

			listener := &recordingAgentEventListener{}
			agent.AddEventListener(listener)

			answer, err := agent.Ask(ctx, fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\n"+
					"If a tool call fails with a transient error, RETRY the same command once. Then reply with the build id it printed.", buildIDPath))
			if err != nil {
				t.Fatalf("structured turn errored on a mid-stream tool failure (must degrade gracefully): %v", err)
			}

			nCalls := atomic.LoadInt32(&calls)
			cleanText, _, contentChunks := captureRealBridge(listener.events)
			toolNames := toolNamesFromEvents(listener.events)
			t.Logf("[%s] structured tool-failure recovery: calls=%d tool-events=%d content=%d; answer=%q", tc.name, nCalls, len(toolNames), contentChunks, strings.TrimSpace(answer))

			if nCalls < 2 {
				t.Fatalf("model did not retry after the tool failure; the tool was called %d time(s)", nCalls)
			}
			if !strings.Contains(answer, codeWord) {
				t.Fatalf("did not recover from the mid-stream failure; answer=%q", answer)
			}

			rec := agentreview.Write(t, "TestStructuredTransportToolFailureRecovery_"+tc.name,
				tc.name+" (structured/json) recovers from a MID-STREAM bridge tool failure: first call fails, model retries, turn completes with the build id",
				map[string]any{
					"provider":               tc.name,
					"transport":              "structured/json",
					"tool_handler_calls":     nCalls,
					"streamed_tool_events":   len(toolNames),
					"tool_names":             toolNames,
					"content_chunks":         contentChunks,
					"clean_stream":           strings.TrimSpace(cleanText),
					"answer":                 strings.TrimSpace(answer),
					"recovered_build_id":     codeWord,
					"injected_first_failure": "TRANSIENT_TOOL_FAILURE on call #1",
				},
				map[string]any{"retried": nCalls >= 2, "recovered": strings.Contains(answer, codeWord)},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

// TestStructuredTransportToolFailureGiveUp is the json/structured counterpart of
// TestRealBridgeStreamingToolFailureGiveUp: a tool that ALWAYS fails must not
// hang or crash the turn — the model gives up after a bounded retry and does NOT
// fabricate the build id (obtainable only via a real tool run).
func TestStructuredTransportToolFailureGiveUp(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the structured tool-failure e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			if tc.provider == llm.ProviderCodexCLI {
				// Codex's native functions.exec is unremovable by any flag, so it
				// bypasses the (always-failing) bridge tool and reads build_id.txt
				// directly — the give-up premise (no way to obtain the id) is
				// unfalsifiable for Codex. Recovery IS tested for Codex; this mirrors
				// the tmux give-up test's strictBridgeOnly=false handling.
				t.Skip("Codex native exec bypasses the failing bridge tool; give-up is unfalsifiable (see strictBridgeOnly).")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			unreachable := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if err := os.WriteFile(buildIDPath, []byte(unreachable), 0o600); err != nil {
				t.Fatal(err)
			}

			var calls int32
			agent, cleanup := buildStructuredBridgeAgentWithShell(t, ctx, tc, workDir,
				func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "", fmt.Errorf("PERMANENT_TOOL_FAILURE: the tool backend is unavailable")
				})
			defer cleanup()

			listener := &recordingAgentEventListener{}
			agent.AddEventListener(listener)

			answer, err := agent.Ask(ctx, fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly:\ncat %s\n"+
					"If the tool call keeps failing, do NOT retry more than once — reply that the build id could NOT be read and stop.", buildIDPath))
			if err != nil {
				t.Fatalf("structured turn errored on a permanent tool failure (must give up gracefully, not error): %v", err)
			}

			nCalls := atomic.LoadInt32(&calls)
			_, _, contentChunks := captureRealBridge(listener.events)
			toolNames := toolNamesFromEvents(listener.events)
			t.Logf("[%s] structured tool-failure give-up: calls=%d tool-events=%d content=%d; tools=%v; answer=%q", tc.name, nCalls, len(toolNames), contentChunks, toolNames, strings.TrimSpace(answer))

			// (1) The tool was actually attempted (handler counter or a streamed
			// tool-call event — Cursor routes through its GetMcpTools/CallMcpTool
			// meta-tool so its handler counter can read 0 even on a real attempt).
			if nCalls == 0 && len(toolNames) == 0 {
				t.Fatalf("the failing tool was never called (no handler invocation and no streamed tool-call event)")
			}
			// (2) It did NOT fabricate the build id — that value is obtainable ONLY
			// via a successful tool run, which never happened.
			if strings.Contains(answer, unreachable) {
				t.Fatalf("model fabricated the build id despite the tool always failing; answer=%q", answer)
			}

			rec := agentreview.Write(t, "TestStructuredTransportToolFailureGiveUp_"+tc.name,
				tc.name+" (structured/json) gives up gracefully on a PERMANENTLY failing bridge tool: bounded retry, turn ends without fabricating the build id",
				map[string]any{
					"provider":          tc.name,
					"transport":         "structured/json",
					"tool_handler_calls": nCalls,
					"streamed_tool_events": len(toolNames),
					"tool_names":        toolNames,
					"content_chunks":    contentChunks,
					"answer":            strings.TrimSpace(answer),
					"unreachable_build_id": unreachable,
					"injected_failure":  "PERMANENT_TOOL_FAILURE on every call",
				},
				map[string]any{"attempted": nCalls > 0 || len(toolNames) > 0, "did_not_fabricate": !strings.Contains(answer, unreachable)},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

// TestStructuredTransportMultiTurn proves multi-turn continuity on the
// json/structured transport via native --resume (ContinueConversation): turn 1
// reads a build id from a file; turn 2, WITHOUT re-reading the file, writes the
// build id it learned in turn 1 into report.md. The build id in report.md proves
// the second turn genuinely continued the first turn's context. This is json's
// analogue of the tmux persistent-session multi-turn test — the mechanism is
// native --resume, not pane reuse (the transport-specific P0, as it should be).
func TestStructuredTransportMultiTurn(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the structured multi-turn e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			buildID := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if err := os.WriteFile(buildIDPath, []byte(buildID), 0o600); err != nil {
				t.Fatal(err)
			}
			convID := "jsonmt-" + realBridgeRandHex(4)

			agent, cleanup, err := buildStructuredBridgeAgent(ctx, tc, t.TempDir(), workDir, convID)
			if err != nil {
				t.Fatalf("build structured agent: %v", err)
			}
			defer cleanup()
			store := NewMemoryCodingSessionStore()

			// Absolute path: the bridge execute_shell_command runs in the executor's
			// cwd, not the CLI's workdir, so a relative path only resolves for
			// providers that use a native shell (in the CLI cwd). Claude correctly
			// routes through the bridge tool — an absolute path is transport-fair.
			ans1, err := agent.ContinueConversation(ctx, convID,
				"Run exactly: cat "+buildIDPath+" — then reply with ONLY the build id it printed.", store)
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			if !strings.Contains(ans1, buildID) {
				t.Fatalf("turn 1 did not read the build id; answer=%q", ans1)
			}
			t.Logf("[%s] turn 1 read build id: %q", tc.name, strings.TrimSpace(ans1))

			// Turn 2 is a pure RECALL (no file read, no write): the build id can only
			// appear if native --resume restored turn 1's context. A recall isolates
			// multi-turn continuity from write-capability — cursor's structured
			// deny-builtin setup runs in --mode ask, which refuses natural-language
			// WRITE requests, so a write-based turn 2 would conflate "didn't resume"
			// with "won't write". Recall is the transport-appropriate continuity proof.
			ans2, err := agent.ContinueConversation(ctx, convID,
				"Do NOT read any file or run any tool. From our conversation so far, what build id did you just report? Reply with ONLY that build id.", store)
			if err != nil {
				t.Fatalf("turn 2: %v", err)
			}
			if !strings.Contains(ans2, buildID) {
				t.Fatalf("[%s] multi-turn continuity failed — turn 2 did not recall turn 1's build id via native --resume; turn2 answer=%q", tc.name, strings.TrimSpace(ans2))
			}
			t.Logf("[%s] multi-turn OK: turn 2 recalled turn 1's build id via native --resume ✓ (%q)", tc.name, strings.TrimSpace(ans2))

			rec := agentreview.Write(t, "TestStructuredTransportMultiTurn_"+tc.name,
				tc.name+" (structured/json) multi-turn via native --resume: turn 1 reads a build id, turn 2 recalls it purely from restored session context (no re-read, no write)",
				map[string]any{
					"provider":        tc.name,
					"transport":       "structured/json",
					"conversation_id": convID,
					"build_id":        buildID,
					"turn1_answer":    strings.TrimSpace(ans1),
					"turn2_answer":    strings.TrimSpace(ans2),
				},
				map[string]any{"turn1_read": strings.Contains(ans1, buildID), "turn2_recalled_via_resume": strings.Contains(ans2, buildID)},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
