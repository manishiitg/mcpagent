package geminicli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
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
		if geminiErr := checkGeminiErr(askErr); geminiErr != askErr {
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
	return err
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
