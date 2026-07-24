package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

// multiTurnProviderCase is one provider's real-CLI binary, mcpagent.Provider,
// model ID, and persistent-session option — generalized from the
// Claude-only helper this file originally had, so multi-turn and
// concurrency isolation get proven against all 4 real coding-agent CLIs, not
// just Claude (see docs/layer_test_coverage.html §matrix — this was the
// single largest "Claude only" gap in mcpagent's real e2e coverage).
type multiTurnProviderCase struct {
	name          string
	binary        string
	provider      llm.Provider
	modelID       string
	streamEnv     string
	persistentOpt func(bool) AgentOption
	// strictBridgeOnly mirrors realBridgeProviderCase's field of the same name
	// (real_bridge_streaming_e2e_test.go): false ONLY for Codex, whose native
	// functions.exec tool cannot be disabled by any flag — a documented,
	// pre-existing limitation, not something a test failure here should be
	// read as a new regression in. Found live extending
	// TestRealBridgeStreamingToolFailureRecovery/GiveUp to all 4 providers:
	// without this, assertBridgeOrWebsearchOnly correctly but confusingly
	// fails for Codex on every run.
	strictBridgeOnly bool
}

var multiTurnProviderCases = []multiTurnProviderCase{
	{"Claude", "claude", llm.ProviderClaudeCode, "claude-haiku-4-5", "CLAUDE_CODE_STREAM_TRANSCRIPT", WithClaudeCodePersistentInteractiveSession, true},
	{"Codex", "codex", llm.ProviderCodexCLI, "gpt-5.6-luna", "CODEX_CLI_STREAM_TRANSCRIPT", WithCodexPersistentInteractiveSession, false},
	{"Cursor", "cursor-agent", llm.ProviderCursorCLI, "cursor-cli", "CURSOR_CLI_STREAM_TRANSCRIPT", WithCursorPersistentInteractiveSession, true},
	{"Pi", "pi", llm.ProviderPiCLI, "google/gemini-3.5-flash", "", WithPiPersistentInteractiveSession, true},
}

// closePersistentInteractiveSession tears down the provider's persistent tmux
// session for this owner (sessionID), if one exists. Agent.Close()
// deliberately does NOT do this in production: a persistent session must
// survive its owning Agent being closed so a later turn's fresh Agent
// (same session ID) can reuse it — that's the whole mechanism these
// multi-turn tests rely on. But a test process never comes back for a later
// turn once it exits, so without an explicit close here every
// persistent-session test leaks its tmux session forever. Found live: 134
// orphaned mlp-* tmux sessions had accumulated from this file's testing
// across several days before this was added.
func closePersistentInteractiveSession(tc multiTurnProviderCase, sessionID string) {
	switch tc.provider {
	case llm.ProviderClaudeCode:
		claudecode.CloseClaudeCodeInteractiveSessionForOwner(sessionID, "test cleanup")
	case llm.ProviderCodexCLI:
		codexcli.CloseCodexCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	case llm.ProviderCursorCLI:
		cursorcli.CloseCursorCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	case llm.ProviderPiCLI:
		picli.ClosePiCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	}
}

// buildRealBridgeAgent stands up an Agent for the given provider, wired to
// the REAL bridge: its own executor HTTP server, the real mcpbridge, and a
// registered real execute_shell_command. t-less (usable from concurrency
// goroutines): callers pass explicit temp/work dirs. Returns the agent + a
// cleanup func.
func buildRealBridgeAgent(ctx context.Context, tc multiTurnProviderCase, tmpBase, workDir, sessionID string, persistent bool) (*Agent, func(), error) {
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
	opts := []AgentOption{
		WithProvider(tc.provider),
		WithAPIConfig(apiURL, apiToken),
		WithStreaming(true),
		WithCodingAgentWorkingDir(workDir),
		WithSessionID(sessionID),
	}
	if persistent {
		opts = append(opts, tc.persistentOpt(true))
	}
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
	return agent, func() {
		agent.Close()
		stopExecutor()
		if persistent {
			closePersistentInteractiveSession(tc, sessionID)
		}
	}, nil
}

// captureRealBridge collapses a listener's events into the CLEAN streamed text
// (Source != terminal, tools dropped), the tool-call count, and the clean content
// chunk count.
func captureRealBridge(evs []*events.AgentEvent) (cleanText string, toolCalls, contentChunks int) {
	// Delta-aware joining: a provider streaming fine-grained deltas (e.g. Pi,
	// mid-token) must have its pieces concatenated VERBATIM, with no
	// separator — inserting "\n" between every chunk unconditionally, as this
	// function used to, splits words/ids mid-string whenever a delta boundary
	// falls there (observed live: "BUILD_ID_4aa\n94683070c"). Only join
	// complete, non-delta blocks with "\n". Mirrors buildMessageModes's
	// already-correct logic (streaming_message_modes_e2e_test.go) — that
	// function got this fix, this one didn't, found extending
	// TestRealBridgeStreamingToolFailureRecovery to Pi.
	var sb strings.Builder
	inDelta := false
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case *events.StreamingChunkEvent:
			if d.IsToolCall || strings.TrimSpace(d.Content) == "" || d.Source == events.StreamingChunkSourceTerminal {
				continue
			}
			if d.IsDelta {
				sb.WriteString(d.Content) // verbatim — never split a token
				inDelta = true
			} else {
				if inDelta {
					sb.WriteString("\n")
					inDelta = false
				}
				sb.WriteString(d.Content)
				sb.WriteString("\n")
			}
			contentChunks++
		case *events.ToolCallStartEvent:
			toolCalls++
		}
	}
	return sb.String(), toolCalls, contentChunks
}

// TestRealBridgeStreamingMultiTurn proves streaming works across MULTIPLE
// turns on a PERSISTENT coding-agent session through the real bridge, on
// every provider: turn 2 reuses the same tmux session (so the cold-turn
// readiness gate is skipped), streams, and the model carries turn-1 context
// (the build id) into a real file write on turn 2. Table-driven across all 4
// providers — was Claude-only (see docs/layer_test_coverage.html §matrix).
func TestRealBridgeStreamingMultiTurn(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge multi-turn e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			if tc.streamEnv != "" {
				t.Setenv(tc.streamEnv, "1")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			codeWord := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			reportPath := filepath.Join(workDir, "report.md")
			if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
				t.Fatal(err)
			}

			agent, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, "mt-"+realBridgeRandHex(4), true)
			if err != nil {
				t.Fatalf("build agent: %v", err)
			}
			defer cleanup()
			listener := &recordingAgentEventListener{}
			agent.AddEventListener(listener)

			// Turn 1 — read the build id through the real shell tool.
			ans1, err := agent.Ask(ctx, fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\nReply with the build id it printed (you do not otherwise know it).", buildIDPath))
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			turn1End := len(listener.events)
			tmux1 := strings.TrimSpace(agent.CodingProviderSessionHandle.TmuxSession)
			_, t1tools, t1content := captureRealBridge(listener.events[:turn1End])
			if !strings.Contains(ans1, codeWord) {
				t.Fatalf("turn 1 answer missing the build id (tool did not run): %q", ans1)
			}
			if t1tools == 0 || t1content == 0 {
				t.Fatalf("turn 1 did not stream: tools=%d content=%d", t1tools, t1content)
			}

			// Turn 2 — SAME session; carry the turn-1 build id into a real file write.
			turn2Start := time.Now()
			ans2, err := agent.Ask(ctx, fmt.Sprintf(
				"Using the build id from the previous step, use execute_shell_command to write a GitHub-flavored markdown table to %s with a header row and the rows '| build_id | <that build id> |' and '| status | ok |'. Then run: cat %s and reply with its contents.", reportPath, reportPath))
			if err != nil {
				t.Fatalf("turn 2: %v", err)
			}
			turn2Elapsed := time.Since(turn2Start)
			tmux2 := strings.TrimSpace(agent.CodingProviderSessionHandle.TmuxSession)
			_, t2tools, t2content := captureRealBridge(listener.events[turn1End:])

			// Session reused across turns → the cold-turn readiness wait is skipped on
			// turn 2 (it only runs on a freshly created session).
			if tmux1 == "" || tmux1 != tmux2 {
				t.Fatalf("persistent session was NOT reused across turns: tmux1=%q tmux2=%q", tmux1, tmux2)
			}
			if t2tools == 0 || t2content == 0 {
				t.Fatalf("turn 2 did not stream: tools=%d content=%d", t2tools, t2content)
			}
			// Context continuity + real write: report.md on disk carries the turn-1 build id.
			//nolint:gosec // G304: reportPath is a test-controlled temp path.
			report, rerr := os.ReadFile(reportPath)
			if rerr != nil {
				t.Fatalf("report.md not written on turn 2: %v", rerr)
			}
			if !strings.Contains(string(report), codeWord) || !strings.Contains(string(report), "|") {
				t.Fatalf("turn 2 did not carry the turn-1 build id into report.md (context not preserved): %q", string(report))
			}

			t.Logf("[%s] multi-turn OK: reused tmux=%s; turn1(tools=%d content=%d) turn2(tools=%d content=%d elapsed=%s)",
				tc.name, tmux1, t1tools, t1content, t2tools, t2content, turn2Elapsed.Round(time.Second))

			rec := agentreview.Write(t, "TestRealBridgeStreamingMultiTurn_"+tc.name,
				tc.name+" persistent multi-turn through the REAL bridge: turn 1 reads a build id, turn 2 reuses the session and writes it into report.md",
				map[string]any{
					"provider":              tc.name,
					"reused_tmux_session":   tmux1 == tmux2,
					"turn1_answer":          strings.TrimSpace(ans1),
					"turn1_tool_events":     t1tools,
					"turn1_content_chunks":  t1content,
					"turn2_answer":          strings.TrimSpace(ans2),
					"turn2_tool_events":     t2tools,
					"turn2_content_chunks":  t2content,
					"report_md_on_disk":     string(report),
					"build_id_only_in_file": codeWord,
				},
				map[string]any{"reused": tmux1 == tmux2, "turn1_streamed": t1tools > 0 && t1content > 0, "turn2_streamed": t2tools > 0 && t2content > 0},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

// TestRealBridgeStreamingConcurrent proves parallel coding-agent sessions
// through the real bridge stay ISOLATED, on every provider: each session
// reads its OWN build id and neither its answer nor its stream leaks the
// other session's build id. Table-driven across all 4 providers — was
// Claude-only (see docs/layer_test_coverage.html §matrix).
func TestRealBridgeStreamingConcurrent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge concurrency e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			if tc.streamEnv != "" {
				t.Setenv(tc.streamEnv, "1")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			const n = 2
			type worker struct{ codeWord, workDir, tmpBase, sessionID, buildIDPath string }
			workers := make([]worker, n)
			for i := range workers {
				wd := t.TempDir()
				w := worker{
					codeWord:    fmt.Sprintf("BUILD_ID_%d_%s", i, realBridgeRandHex(5)),
					workDir:     wd,
					tmpBase:     t.TempDir(),
					sessionID:   fmt.Sprintf("conc-%d-%s", i, realBridgeRandHex(4)),
					buildIDPath: filepath.Join(wd, "build_id.txt"),
				}
				if err := os.WriteFile(w.buildIDPath, []byte(w.codeWord), 0o600); err != nil {
					t.Fatal(err)
				}
				workers[i] = w
			}

			type result struct {
				answer, cleanText string
				tools             int
				err               error
			}
			results := make([]result, n)
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					w := workers[i]
					agent, cleanup, err := buildRealBridgeAgent(ctx, tc, w.tmpBase, w.workDir, w.sessionID, false)
					if err != nil {
						results[i] = result{err: err}
						return
					}
					defer cleanup()
					listener := &recordingAgentEventListener{}
					agent.AddEventListener(listener)
					ans, askErr := agent.Ask(ctx, fmt.Sprintf(
						"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\nReply with the build id it printed.", w.buildIDPath))
					if askErr != nil {
						results[i] = result{err: askErr}
						return
					}
					text, tools, _ := captureRealBridge(listener.events)
					results[i] = result{answer: ans, cleanText: text, tools: tools}
				}(i)
			}
			wg.Wait()

			for i, r := range results {
				if r.err != nil {
					t.Fatalf("worker %d failed: %v", i, r.err)
				}
				if !strings.Contains(r.answer, workers[i].codeWord) {
					t.Fatalf("worker %d answer missing its OWN build id %q: %q", i, workers[i].codeWord, r.answer)
				}
				if r.tools == 0 {
					t.Fatalf("worker %d did not stream a tool call", i)
				}
				for j := range workers {
					if j == i {
						continue
					}
					if strings.Contains(r.answer, workers[j].codeWord) || strings.Contains(r.cleanText, workers[j].codeWord) {
						t.Fatalf("ISOLATION BREACH: worker %d leaked worker %d's build id %q (answer=%q)", i, j, workers[j].codeWord, r.answer)
					}
				}
				t.Logf("[%s] worker %d isolated: own=%s tools=%d", tc.name, i, workers[i].codeWord, r.tools)
			}

			rec := agentreview.Write(t, "TestRealBridgeStreamingConcurrent_"+tc.name,
				fmt.Sprintf("%d parallel %s sessions through the REAL bridge, each reading its own build id — stream isolation", n, tc.name),
				map[string]any{
					"provider":         tc.name,
					"worker0_answer":   strings.TrimSpace(results[0].answer),
					"worker0_build_id": workers[0].codeWord,
					"worker1_answer":   strings.TrimSpace(results[1].answer),
					"worker1_build_id": workers[1].codeWord,
					"no_cross_leak":    true,
				},
				map[string]any{"workers": n, "isolated": true},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
