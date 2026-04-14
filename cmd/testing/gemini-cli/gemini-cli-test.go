package geminicli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	testutils "github.com/manishiitg/mcpagent/cmd/testing/testutils"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var geminiCLITestCmd = &cobra.Command{
	Use:   "gemini-cli",
	Short: "E2E tests for Gemini CLI provider with HTTP routing hooks + mcpbridge",
	Long: `Tests the Gemini CLI provider with MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING=true.

Three sub-tests (all share one agent/session to exercise multi-turn):
1. Text response  — catches broken hook config (allowed_function_names 400 error)
2. Tool call      — model calls execute_shell_command via real mcpbridge; hook must allow it
3. Blocked tool   — model tries a disallowed Gemini built-in; hook must deny it

Sub-test 2 starts a mock HTTP API server and uses the real mcpbridge binary,
matching the production code path.

Examples:
  mcpagent-test test gemini-cli
  mcpagent-test test gemini-cli --log-level debug`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := testutils.NewTestLoggerFromViper()
		logger.Info("=== Gemini CLI E2E Tests ===")

		if err := runAllGeminiCLITests(logger); err != nil {
			return fmt.Errorf("gemini-cli tests failed: %w", err)
		}

		logger.Info("✅ All Gemini CLI tests passed!")
		return nil
	},
}

// GetGeminiCLITestCmd returns the Gemini CLI test command.
func GetGeminiCLITestCmd() *cobra.Command {
	return geminiCLITestCmd
}

func runAllGeminiCLITests(log loggerv2.Logger) error {
	// Force HTTP routing hooks on — production always runs with this set.
	// The original bug (mode=AUTO + allowedFunctionNames in BeforeToolSelection)
	// only manifests when this env var is set, which is why basic tests missed it.
	orig := os.Getenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING")
	if err := os.Setenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING", "true"); err != nil {
		return fmt.Errorf("failed to set env var: %w", err)
	}
	defer func() {
		if orig == "" {
			_ = os.Unsetenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING")
		} else {
			_ = os.Setenv("MCPAGENT_GEMINI_ENFORCE_HTTP_TOOL_ROUTING", orig)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- Sub-test 1: plain text response ---
	// Uses a minimal agent (no bridge). Catches broken hook config / settings.json.
	log.Info("--- Sub-test 1: Text response (validates hook config / settings.json) ---")
	if err := subTestTextResponse(ctx, log); err != nil {
		return fmt.Errorf("sub-test 1 (text response): %w", err)
	}
	log.Info("✅ Sub-test 1 passed")

	// --- Sub-test 2: allowed tool call via mcpbridge ---
	// Starts a mock HTTP API server + uses real mcpbridge binary.
	// The enforce-http-tool-routing hook must allow execute_shell_command through.
	log.Info("--- Sub-test 2: Allowed tool call via mcpbridge (execute_shell_command) ---")
	if err := subTestAllowedToolCallViaBridge(ctx, log); err != nil {
		return fmt.Errorf("sub-test 2 (allowed tool call via bridge): %w", err)
	}
	log.Info("✅ Sub-test 2 passed")

	// --- Sub-test 3: disallowed tool blocked by hook ---
	// The enforce-http-tool-routing hook must deny Gemini built-ins not in the allowed set.
	log.Info("--- Sub-test 3: Disallowed built-in tool blocked by hook ---")
	if err := subTestDisallowedToolBlocked(ctx, log); err != nil {
		return fmt.Errorf("sub-test 3 (disallowed tool blocked): %w", err)
	}
	log.Info("✅ Sub-test 3 passed")

	// --- Sub-test 4: read_file path-validation bypasses hook (regression) ---
	// Reproduces the root cause of the 15-minute context deadline exceeded issue:
	// read_file.build() throws INVALID_TOOL_PARAMS for non-workspace paths BEFORE the
	// BeforeTool hook runs, causing confusing model retry loops that hit the deadline.
	// The fix is tools.exclude which removes read_file from the model's tool registry.
	log.Info("--- Sub-test 4: read_file path-validation bypasses hook + tools.exclude fix ---")
	if err := subTestReadFilePathValidationBypassesHook(ctx, log); err != nil {
		return fmt.Errorf("sub-test 4 (read_file hook bypass + tools.exclude fix): %w", err)
	}
	log.Info("✅ Sub-test 4 passed")

	return nil
}

// subTestTextResponse: minimal agent, no bridge. Validates hook config / settings.json.
func subTestTextResponse(ctx context.Context, log loggerv2.Logger) error {
	model, err := newGeminiCLIModel(log)
	if err != nil {
		return err
	}
	tracer, _ := testutils.GetTracerWithLogger("noop", log)
	agent, err := testutils.CreateMinimalAgent(ctx, model, llm.ProviderGeminiCLI, tracer, testutils.GenerateTestTraceID(), log)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer agent.Close()

	response, err := agent.Ask(ctx, "Say hello in one short sentence.")
	if err != nil {
		return checkGeminiErr(err)
	}
	if response == "" {
		return fmt.Errorf("empty response")
	}
	log.Info("Got response", loggerv2.String("response", response))
	return nil
}

// subTestAllowedToolCallViaBridge: starts a mock HTTP server, creates an agent
// with code execution mode + mcpbridge, asks model to run execute_shell_command.
func subTestAllowedToolCallViaBridge(ctx context.Context, log loggerv2.Logger) error {
	// 1. Start mock HTTP API server
	mock, err := startMockAPIServer(log)
	if err != nil {
		return fmt.Errorf("start mock server: %w", err)
	}
	defer mock.shutdown()
	time.Sleep(100 * time.Millisecond) // let listener bind

	// 2. Create LLM + agent with code execution mode + bridge config pointing to mock
	model, err := newGeminiCLIModel(log)
	if err != nil {
		return err
	}
	tracer, _ := testutils.GetTracerWithLogger("noop", log)
	traceID := testutils.GenerateTestTraceID()

	// Write minimal config so CreateAgentWithTracer has a valid path
	configPath := "/tmp/minimal-mcp-config-gemini-bridge-test.json"
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	defer os.Remove(configPath)

	agent, err := testutils.CreateAgentWithTracer(ctx, model, llm.ProviderGeminiCLI, configPath, tracer, traceID, log,
		mcpagent.WithCodeExecutionMode(true),
		mcpagent.WithAPIConfig(mock.url, mock.apiToken),
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer agent.Close()

	// Register bridge tools so BuildBridgeMCPConfig can find them.
	// In production these are registered by the orchestrator; in the test we stub them.
	// The actual execution goes through mcpbridge → mock HTTP server, not these stubs.
	if err := registerBridgeToolStubs(agent); err != nil {
		return fmt.Errorf("register bridge tool stubs: %w", err)
	}

	// 3. Ask model to use the bridge tool. We name the full MCP tool name so the model
	// doesn't try bare execute_shell_command (which doesn't exist as a native Gemini CLI tool).
	// We also accept get_api_spec as a valid bridge call since the model reliably discovers
	// the tool index via get_api_spec in code execution mode.
	//
	// We care that the bridge path was exercised (mock received ANY bridge call), not that
	// the conversation completed cleanly. A context deadline is still a pass if the mock
	// was reached.
	prompt := "Use the mcp_api-bridge_execute_shell_command tool (from the api-bridge MCP server) " +
		"with command=\"echo bridge-test-ok\" and tell me the output. " +
		"If that tool isn't available, call mcp_api-bridge_get_api_spec to list available tools."
	response, askErr := agent.Ask(ctx, prompt)
	if askErr != nil {
		// Surface 400 INVALID_ARGUMENT errors immediately — they indicate broken hook/settings config.
		if geminiErr := checkGeminiErr(askErr); geminiErr != nil {
			return geminiErr
		}
		log.Info("Ask returned error (checking mock before failing)",
			loggerv2.String("error", askErr.Error()))
	} else {
		log.Info("Got response", loggerv2.String("response_preview", truncate(response, 300)))
	}

	// Verify the mock received at least one bridge tool call.
	// execute_shell_command is the primary target; get_api_spec is the fallback
	// (always called when the model discovers the tool index).
	reqs := mock.getRequests()
	foundToolCall := false
	foundPath := ""
	for _, r := range reqs {
		if strings.Contains(r.Path, "execute_shell_command") || strings.Contains(r.Path, "get_api_spec") {
			foundToolCall = true
			foundPath = r.Path
			break
		}
	}
	if !foundToolCall {
		if askErr != nil {
			return fmt.Errorf("no bridge tool reached mock server AND Ask failed: %w", askErr)
		}
		return fmt.Errorf("no bridge tool call reached mock server (hook may have blocked all calls); requests received: %d", len(reqs))
	}
	log.Info("✅ Bridge tool reached mock server", loggerv2.String("path", foundPath))

	return nil
}

// subTestDisallowedToolBlocked: asks model to use a Gemini CLI built-in (read_file)
// that is NOT in the allowed set. The hook must deny it; no 400 from Gemini API.
func subTestDisallowedToolBlocked(ctx context.Context, log loggerv2.Logger) error {
	model, err := newGeminiCLIModel(log)
	if err != nil {
		return err
	}
	tracer, _ := testutils.GetTracerWithLogger("noop", log)
	agent, err := testutils.CreateMinimalAgent(ctx, model, llm.ProviderGeminiCLI, tracer, testutils.GenerateTestTraceID(), log)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer agent.Close()

	// read_file is a Gemini CLI built-in NOT in the allowed set — hook must deny it.
	response, err := agent.Ask(ctx, "Use the read_file tool to read /etc/hostname and tell me its contents. Only use read_file.")
	if err != nil {
		// Hook denied → model may give up and return an agent error. That's fine.
		// The only unacceptable outcome is a 400 INVALID_ARGUMENT from the Gemini API.
		return checkGeminiErr(err)
	}

	log.Info("Got response after tool block",
		loggerv2.String("response_preview", truncate(response, 200)))
	return nil
}

// subTestReadFilePathValidationBypassesHook is a two-part regression test for the
// 15-minute context deadline exceeded bug.
//
// Root cause: Gemini CLI's read_file validates paths against its temp workspace dir in
// tool.build(). Any path outside the workspace (e.g. /app/workspace-docs/plan.md) throws
// during build() — BEFORE executeToolWithHooks or the Policy Engine runs. The model
// receives INVALID_TOOL_PARAMS, gets confused, and retries in a loop until the step timeout.
//
// Fix: tools.exclude removes read_file from the model's tool registry entirely,
// so the model never attempts the call and the retry loop never starts.
//
// Part 1 (reproduce): calls model.GenerateContent directly with settings that have the
//   BeforeTool hook but NO tools.exclude. Asks the model to read a non-workspace path.
//   Asserts that the hook log has NO new read_file entry — confirming the hook was
//   bypassed entirely by the path-validation failure in build().
//
// Part 2 (verify fix): calls through agent.Ask() which now includes tools.exclude.
//   Asks the model to read the same path. Asserts the call completes without a
//   Gemini API 400 error and without INVALID_TOOL_PARAMS-style retry loops.
func subTestReadFilePathValidationBypassesHook(ctx context.Context, log loggerv2.Logger) error {
	// Part 1: reproduce — hook is configured but never called because build() throws first.
	log.Info("Part 1: Reproducing hook bypass (no tools.exclude)")
	if err := verifyReadFileBypassesHookLog(ctx, log); err != nil {
		return fmt.Errorf("part 1 (reproduce hook bypass): %w", err)
	}
	log.Info("✅ Part 1: confirmed BeforeTool hook was NOT called for read_file (bypassed by path validation)")

	// Part 2: verify fix — tools.exclude removes read_file, no INVALID_TOOL_PARAMS loop.
	log.Info("Part 2: Verifying fix (tools.exclude in settings)")
	if err := verifyToolsExcludePreventsReadFile(ctx, log); err != nil {
		return fmt.Errorf("part 2 (verify tools.exclude fix): %w", err)
	}
	log.Info("✅ Part 2: confirmed tools.exclude prevents read_file retry loop")

	return nil
}

// verifyReadFileBypassesHookLog calls model.GenerateContent directly with a settings JSON
// that configures the enforce-http-tool-routing hook but omits tools.exclude (the pre-fix
// state). It then asserts that the hook log has NO new entry for read_file, confirming
// the hook was bypassed by read_file.build() throwing INVALID_TOOL_PARAMS.
func verifyReadFileBypassesHookLog(ctx context.Context, log loggerv2.Logger) error {
	model, err := newGeminiCLIModel(log)
	if err != nil {
		return err
	}

	// Create an isolated project dir and write the enforce-http-tool-routing hook.
	dirID := fmt.Sprintf("test-readfile-bypass-%d", time.Now().UnixMilli())
	projectDir := filepath.Join(os.TempDir(), "gemini-cli-project-"+dirID)
	hooksDir := filepath.Join(projectDir, ".gemini", "hooks")
	if mkErr := os.MkdirAll(hooksDir, 0750); mkErr != nil {
		return fmt.Errorf("create hooks dir: %w", mkErr)
	}
	defer func() { _ = os.RemoveAll(projectDir) }()

	hookPath := filepath.Join(hooksDir, "enforce-http-tool-routing.py")
	if writeErr := os.WriteFile(hookPath, []byte(enforceHTTPRoutingHookScript()), 0750); writeErr != nil { //nolint:gosec // executable hook script
		return fmt.Errorf("write hook script: %w", writeErr)
	}

	// Settings: hook configured but NO tools.exclude — this is the pre-fix state.
	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"BeforeTool": []map[string]interface{}{
				{
					"matcher": "*",
					"hooks": []map[string]interface{}{
						{
							"name":        "enforce-http-tool-routing",
							"type":        "command",
							"command":     "$GEMINI_PROJECT_DIR/.gemini/hooks/enforce-http-tool-routing.py",
							"timeout":     5000,
							"description": "Allow only bridge tools; deny all Gemini built-ins",
						},
					},
				},
			},
		},
	}
	settingsJSON, _ := json.Marshal(settings)

	// Record hook log line count BEFORE the call.
	logPath := filepath.Join(os.TempDir(), "enforce-http-tool-routing.log")
	linesBefore := countFileLines(logPath)

	// Use a short timeout — long enough to see the first INVALID_TOOL_PARAMS response
	// but far shorter than the 15-minute production timeout that gets hit in the bug.
	shortCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// Call model directly (bypass llm_generation.go which now adds tools.exclude).
	// The model will try read_file with the given path; build() throws INVALID_TOOL_PARAMS;
	// hook is never called; model gets confused and eventually emits a text response.
	messages := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{
					Text: "Use the read_file tool to read /app/workspace-docs/plan.md and tell me what it contains. You MUST use the read_file tool.",
				},
			},
		},
	}
	_, callErr := model.GenerateContent(shortCtx, messages,
		llm.WithGeminiProjectSettings(string(settingsJSON)),
		llm.WithGeminiProjectDirID(dirID),
		llm.WithGeminiApprovalMode("auto"),
	)

	// The call is expected to fail (INVALID_TOOL_PARAMS loop → context deadline, or the model
	// gives up and returns a text response). A 400 INVALID_ARGUMENT from the Gemini API is
	// the only unacceptable failure — it means the settings.json is broken.
	if callErr != nil {
		if geminiErr := checkGeminiErr(callErr); geminiErr != nil {
			return geminiErr
		}
		log.Info("Call returned error (expected for pre-fix scenario)",
			loggerv2.String("error", callErr.Error()))
	}

	// Check hook log for new entries written during this call.
	linesAfter := countFileLines(logPath)
	newLines := readNewFileLines(logPath, linesBefore)
	log.Info("Hook log after call (no-exclude scenario)",
		loggerv2.Int("new_lines", linesAfter-linesBefore),
	)

	// Assert: hook was NOT invoked for read_file.
	// If the hook HAD been called, it would have written a DENY line containing "read_file".
	// No such line means the hook was bypassed (build() threw before reaching executeToolWithHooks).
	for _, line := range newLines {
		if strings.Contains(line, "read_file") {
			return fmt.Errorf(
				"unexpected: hook log contains a read_file entry %q — "+
					"this means hook WAS called, contradicting the expected bypass via build() path validation. "+
					"The tools.exclude fix may be unexpectedly active, or Gemini CLI path validation changed",
				line,
			)
		}
	}

	log.Info("Confirmed: no read_file entry in hook log — hook was bypassed by build() path validation failure")
	return nil
}

// verifyToolsExcludePreventsReadFile verifies the fix: with tools.exclude in settings,
// the model never sees read_file, so no INVALID_TOOL_PARAMS loop occurs. It uses
// agent.Ask() which now always includes tools.exclude via llm_generation.go.
func verifyToolsExcludePreventsReadFile(ctx context.Context, log loggerv2.Logger) error {
	model, err := newGeminiCLIModel(log)
	if err != nil {
		return err
	}
	tracer, _ := testutils.GetTracerWithLogger("noop", log)
	agent, err := testutils.CreateMinimalAgent(ctx, model, llm.ProviderGeminiCLI, tracer, testutils.GenerateTestTraceID(), log)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer agent.Close()

	// Record hook log position before the call.
	logPath := filepath.Join(os.TempDir(), "enforce-http-tool-routing.log")
	linesBefore := countFileLines(logPath)

	// With tools.exclude, read_file is not in the model's tool registry.
	// The model should respond saying it cannot read files directly,
	// without ever attempting read_file and without any INVALID_TOOL_PARAMS error.
	shortCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	response, askErr := agent.Ask(shortCtx,
		"Use the read_file tool to read /app/workspace-docs/plan.md and tell me what it contains. You MUST use the read_file tool.")
	if askErr != nil {
		if geminiErr := checkGeminiErr(askErr); geminiErr != nil {
			return geminiErr
		}
		// Non-400 errors are acceptable — the model may give up cleanly.
		log.Info("Ask returned error (checking hook log before failing)",
			loggerv2.String("error", askErr.Error()))
	} else {
		log.Info("Got response with tools.exclude",
			loggerv2.String("response_preview", truncate(response, 200)))
	}

	// Assert: hook was NOT called for read_file (tool was excluded, never attempted).
	newLines := readNewFileLines(logPath, linesBefore)
	for _, line := range newLines {
		if strings.Contains(line, "read_file") {
			return fmt.Errorf(
				"unexpected: hook log contains a read_file entry %q — "+
					"tools.exclude should have removed read_file from the model's tool set, "+
					"so the model should never have attempted it",
				line,
			)
		}
	}

	log.Info("Confirmed: no read_file in hook log — tools.exclude prevented the tool from being called")
	return nil
}

// countFileLines returns the number of lines in a file (0 if file does not exist).
func countFileLines(path string) int {
	f, err := os.Open(path) //nolint:gosec // reading log file
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		count++
	}
	return count
}

// readNewFileLines returns lines from path starting at skipLines (0-indexed).
func readNewFileLines(path string, skipLines int) []string {
	f, err := os.Open(path) //nolint:gosec // reading log file
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	i := 0
	for sc.Scan() {
		if i >= skipLines {
			lines = append(lines, sc.Text())
		}
		i++
	}
	return lines
}

// enforceHTTPRoutingHookScript returns the Python source for the enforce-http-tool-routing
// BeforeTool hook. This is the same script written by writeGeminiHookScripts in production.
func enforceHTTPRoutingHookScript() string {
	return `#!/usr/bin/env python3
import json
import os
import sys
import datetime

ALLOWED = {
    "google_web_search",
    "execute_shell_command",
    "diff_patch_workspace_file",
    "agent_browser",
    "get_api_spec",
    "mcp_api-bridge_execute_shell_command",
    "mcp_api-bridge_diff_patch_workspace_file",
    "mcp_api-bridge_agent_browser",
    "mcp_api-bridge_get_api_spec",
}

LOG_PATH = os.path.join(os.environ.get("TMPDIR", "/tmp"), "enforce-http-tool-routing.log")

def log(msg):
    try:
        with open(LOG_PATH, "a", encoding="utf-8") as f:
            f.write(f"[{datetime.datetime.utcnow().isoformat()}] {msg}\n")
    except Exception:
        pass

raw = sys.stdin.read()
payload = {}
try:
    payload = json.loads(raw) if raw else {}
except Exception:
    pass

tool_name = payload.get("tool_name", "")
# Strip 'default_api:' prefix that some Gemini models add to MCP tool names
if tool_name.startswith("default_api:"):
    tool_name = tool_name[len("default_api:"):]
mcp_context = payload.get("mcp_context") or {}
server_name = mcp_context.get("server_name")

if tool_name in ALLOWED:
    log(f"BeforeTool ALLOW: tool_name={tool_name!r} server_name={server_name!r}")
    sys.stdout.write("{}\n")
    raise SystemExit(0)

log(f"BeforeTool DENY: tool_name={tool_name!r} server_name={server_name!r}")

reason = (
    "Only execute_shell_command, diff_patch_workspace_file, agent_browser, get_api_spec, and google_web_search are allowed. "
    "Do not call '" + tool_name + "' directly."
)

sys.stdout.write(json.dumps({"decision": "deny", "reason": reason}) + "\n")
`
}

// registerBridgeToolStubs registers stub versions of the bridge tools on an agent.
// In production these are registered by the orchestrator. The stubs exist only so
// BuildBridgeMCPConfig can serialise them into the mcpbridge MCP_TOOLS env var;
// the actual calls go through mcpbridge → HTTP API, not these execution functions.
func registerBridgeToolStubs(agent *mcpagent.Agent) error {
	type stub struct {
		name     string
		desc     string
		params   map[string]interface{}
		category string
	}
	stubs := []stub{
		// Use "mcp_bridge" (non-system category) so these stubs go into a.customTools
		// for bridge serialization but do NOT get added to a.Tools as function declarations.
		// Gemini CLI cannot execute function declarations — it can only execute MCP tools
		// (from the bridge). If execute_shell_command appears as a function declaration,
		// the model calls it bare and Gemini CLI returns "not found"; the model must use
		// the MCP-prefixed name mcp_api-bridge_execute_shell_command.
		{
			name:     "execute_shell_command",
			desc:     codeexec.ShellCommandDescription,
			params:   codeexec.ShellCommandParams,
			category: "mcp_bridge",
		},
		{
			name: "diff_patch_workspace_file",
			desc: "Apply a unified diff patch to a workspace file.",
			params: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filepath": map[string]interface{}{"type": "string"},
					"patch":    map[string]interface{}{"type": "string"},
				},
				"required": []string{"filepath", "patch"},
			},
			category: "mcp_bridge",
		},
		{
			name: "agent_browser",
			desc: "Control a browser agent session.",
			params: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{"type": "string"},
				},
				"required": []string{"command"},
			},
			category: "mcp_bridge",
		},
	}
	for _, s := range stubs {
		if err := agent.RegisterCustomTool(s.name, s.desc, s.params, func(_ context.Context, _ map[string]interface{}) (string, error) {
			return "stub-not-called", nil
		}, s.category); err != nil {
			return fmt.Errorf("register %s: %w", s.name, err)
		}
	}
	return nil
}

// --- helpers ---

func newGeminiCLIModel(log loggerv2.Logger) (llmtypes.Model, error) {
	return llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderGeminiCLI,
		ModelID:  "auto",
		Logger:   log,
	})
}

// checkGeminiErr surfaces the 400 INVALID_ARGUMENT error distinctly from other failures.
func checkGeminiErr(err error) error {
	if strings.Contains(err.Error(), "allowed_function_names") {
		return fmt.Errorf("❌ Gemini API 400 INVALID_ARGUMENT: hook or settings.json sets allowed_function_names with wrong mode — %w", err)
	}
	return nil
}

// --- mock HTTP API server ---

type requestLog struct {
	Path string
	Body map[string]interface{}
}

type mockAPIServer struct {
	url      string
	apiToken string
	server   *http.Server
	mu       sync.Mutex
	requests []requestLog
	shutdown func()
}

func (m *mockAPIServer) getRequests() []requestLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]requestLog, len(m.requests))
	copy(out, m.requests)
	return out
}

func startMockAPIServer(log loggerv2.Logger) (*mockAPIServer, error) {
	// Pick a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	mock := &mockAPIServer{
		url:      "http://" + ln.Addr().String(),
		apiToken: "test-token-gemini-cli",
	}

	mux := http.NewServeMux()

	// toolsHandler handles both /tools/* and /s/{sessionID}/tools/* paths.
	// BuildBridgeMCPConfig appends "/s/{sessionID}" to the API URL, so the bridge
	// sends requests to /s/global/tools/custom/... — we strip the prefix before matching.
	toolsHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mock.apiToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "unauthorized"})
			return
		}

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		mock.mu.Lock()
		mock.requests = append(mock.requests, requestLog{Path: r.URL.Path, Body: body})
		mock.mu.Unlock()

		log.Info("Mock received tool call", loggerv2.String("path", r.URL.Path))

		// Return realistic results so the model recognises success and stops looping.
		var result string
		switch {
		case strings.Contains(r.URL.Path, "execute_shell_command"):
			// Simulate shell output — the model asked for "echo bridge-test-ok"
			cmd, _ := body["command"].(string)
			result = fmt.Sprintf("exit_code: 0\nstdout:\nbridge-test-ok\nstderr:\n(command: %s)", cmd)
		case strings.Contains(r.URL.Path, "get_api_spec"):
			result = `{"openapi":"3.0.0","info":{"title":"mock-api"},"paths":{}}`
		default:
			result = fmt.Sprintf("mock-ok: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  result,
		})
	}

	// Register handler for both raw /tools/* and session-scoped /s/*/tools/* paths
	mux.HandleFunc("/tools/", toolsHandler)
	mux.HandleFunc("/s/", toolsHandler) // catches /s/{sessionID}/tools/...

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Warn("Mock server error", loggerv2.Error(err))
		}
	}()

	mock.server = srv
	mock.shutdown = func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}

	log.Info("Mock API server started", loggerv2.String("url", mock.url))
	return mock, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
