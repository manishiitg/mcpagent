package mcpagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
)

func realBridgeRandHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startRealExecutorServer boots the executor HTTP API the mcpbridge posts tool
// calls to — the SAME wiring examples/basic_claude_code uses — and exports
// MCP_API_URL / MCP_API_TOKEN. Returns a shutdown func.
func startRealExecutorServer(t *testing.T, configPath string) (string, string) {
	t.Helper()
	apiToken := executor.GenerateAPIToken()
	handlers := executor.NewExecutorHandlers(configPath, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)
	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path[len("/tools/mcp/"):]
		slash := strings.IndexByte(path, '/')
		if slash <= 0 || slash >= len(path)-1 {
			http.Error(w, "invalid tool path", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolMCPRequest(w, r, path[:slash], path[slash+1:])
	})
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Path[len("/tools/custom/"):]
		if tool == "" {
			http.Error(w, "missing custom tool name", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolCustomRequest(w, r, tool)
	})
	mux.HandleFunc("/tools/virtual/", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Path[len("/tools/virtual/"):]
		if tool == "" {
			http.Error(w, "missing virtual tool name", http.StatusBadRequest)
			return
		}
		handlers.HandlePerToolVirtualRequest(w, r, tool)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("executor listen: %v", err)
	}
	server := &http.Server{Handler: executor.AuthMiddleware(apiToken)(mux)} //nolint:gosec // test server, no timeouts needed
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	})
	apiBaseURL := "http://" + listener.Addr().String()
	time.Sleep(300 * time.Millisecond)
	return apiBaseURL, apiToken
}

// ensureRealBridgeBinary builds cmd/mcpbridge from source into a temp path so the
// test drives the ACTUAL production bridge binary (with its readiness marker +
// HTTP forwarding), not a stand-in.
func ensureRealBridgeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mcpbridge")
	//nolint:gosec // G204: constant build command, temp output path.
	out, err := exec.Command("go", "build", "-o", bin, "../cmd/mcpbridge").CombinedOutput()
	if err != nil {
		t.Fatalf("build mcpbridge: %v\n%s", err, out)
	}
	return bin
}

// realBridgeProviderCase is one coding-agent provider exercised through the REAL
// bridge. streamEnv is the transcript-streaming opt-in env var (empty when the
// provider streams structured chunks natively, e.g. pi's markers). apiKeyEnvs, if
// set, names the env vars to source the provider key from (pi); CLI-native-auth
// providers (claude/codex/cursor) leave it empty.
type realBridgeProviderCase struct {
	name       string
	provider   llm.Provider
	modelID    string
	cliBin     string
	streamEnv  string
	apiKeyEnvs []string
	makeKeys   func(key string) *llm.ProviderAPIKeys
}

func realBridgeProviderCases() []realBridgeProviderCase {
	return []realBridgeProviderCase{
		{name: "claude", provider: llm.ProviderClaudeCode, modelID: "claude-haiku-4-5", cliBin: "claude", streamEnv: "CLAUDE_CODE_STREAM_TRANSCRIPT"},
		{name: "codex", provider: llm.ProviderCodexCLI, modelID: "gpt-5.6-luna", cliBin: "codex", streamEnv: "CODEX_CLI_STREAM_TRANSCRIPT"},
		// cursor reaches the bridge via its GetMcpTools/CallMcpTool meta-tools; the
		// mcpagent cursor integration auto-approves the MCP bridge (WithCursorApproveMCPs).
		{name: "cursor", provider: llm.ProviderCursorCLI, modelID: "cursor-cli", cliBin: "cursor-agent", streamEnv: "CURSOR_CLI_STREAM_TRANSCRIPT"},
		// pi streams structured chunks natively via its injected marker hook (no
		// streamEnv) and needs a Gemini/Pi key.
		{
			name: "pi", provider: llm.ProviderPiCLI, modelID: "google/gemini-3.5-flash", cliBin: "pi", streamEnv: "",
			apiKeyEnvs: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "PI_API_KEY"},
			makeKeys:   func(k string) *llm.ProviderAPIKeys { return &llm.ProviderAPIKeys{PiCLI: &k} },
		},
	}
}

// TestRealBridgeStreamingE2E is the production-fidelity streaming test the
// stand-in-MCP-server tests were missing: a real coding-agent turn whose tools go
// through the REAL mcpbridge → executor HTTP API → a REAL mcpagent tool
// (execute_shell_command running an actual shell), with structured streaming
// captured at the mcpagent layer (events.StreamingChunkEvent). It proves the
// whole production path streams: bridge tool-call chunks reach the app AND the
// real shell tool actually ran — per provider.
//
// Gated by RUN_MCPAGENT_REAL_BRIDGE_E2E=1 (optional MCPAGENT_REAL_BRIDGE_ONLY=<name>);
// requires the provider's authenticated CLI, tmux, and go (to build the bridge).
// No node / stand-in server.
func TestRealBridgeStreamingE2E(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge streaming e2e")
	}
	only := os.Getenv("MCPAGENT_REAL_BRIDGE_ONLY")
	bridgeBin := ensureRealBridgeBinary(t)
	for _, pc := range realBridgeProviderCases() {
		if only != "" && only != pc.name {
			continue
		}
		t.Run(pc.name, func(t *testing.T) { runRealBridgeStreaming(t, pc, bridgeBin) })
	}
}

func runRealBridgeStreaming(t *testing.T, pc realBridgeProviderCase, bridgeBin string) {
	if _, err := exec.LookPath(pc.cliBin); err != nil {
		t.Skipf("authenticated %q CLI required", pc.cliBin)
	}

	t.Setenv("MCP_BRIDGE_BINARY", bridgeBin)
	if pc.streamEnv != "" {
		t.Setenv(pc.streamEnv, "1")
	}

	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	apiURL, apiToken := startRealExecutorServer(t, configPath)

	cfg := llm.Config{Provider: pc.provider, ModelID: pc.modelID}
	if len(pc.apiKeyEnvs) > 0 {
		for _, envPath := range []string{"../.env", "../../multi-llm-provider-go/.env"} {
			_ = godotenv.Load(envPath)
		}
		var key string
		for _, e := range pc.apiKeyEnvs {
			if v := strings.TrimSpace(os.Getenv(e)); v != "" {
				key = v
				break
			}
		}
		if key == "" {
			t.Skipf("one of %v required for %s", pc.apiKeyEnvs, pc.name)
		}
		if pc.makeKeys != nil {
			cfg.APIKeys = pc.makeKeys(key)
		}
	}
	llmModel, err := llm.InitializeLLM(cfg)
	if err != nil {
		t.Fatalf("InitializeLLM: %v", err)
	}

	workDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	agent, err := NewAgent(ctx, llmModel, configPath,
		WithProvider(pc.provider),
		WithAPIConfig(apiURL, apiToken),
		WithStreaming(true),
		WithCodingAgentWorkingDir(workDir),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer agent.Close()

	// Register the REAL shell tool the bridge will expose and route to.
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if err := agent.RegisterCustomTool(
		"execute_shell_command",
		codeexec.ShellCommandDescription,
		codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return codeexec.ExecuteShellCommand(ctx, args, shellEnv)
		},
		"workspace_advanced",
	); err != nil {
		t.Fatalf("RegisterCustomTool: %v", err)
	}

	listener := &recordingAgentEventListener{}
	agent.AddEventListener(listener)

	// A rich, real multi-step task: READ a real file (a project build-id that is
	// only in the file, not the prompt — anti-cheat, benign framing so safety-tuned
	// models don't refuse), WRITE a real file (a markdown table), then read it back.
	// Absolute paths so they're independent of the shell tool's cwd.
	codeWord := "BUILD_ID_" + realBridgeRandHex(6)
	buildIDPath := filepath.Join(workDir, "build_id.txt")
	reportPath := filepath.Join(workDir, "report.md")
	if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
		t.Fatal(err)
	}
	task := fmt.Sprintf(
		"You are a build assistant with one tool: execute_shell_command, which runs a shell command and returns its output. "+
			"Do these steps in order, writing one short sentence of narration BEFORE each command:\n"+
			"1. Run: cat %[1]s   — this prints the project build id.\n"+
			"2. Using a shell command, write a GitHub-flavored markdown report table to the file %[2]s with EXACTLY this "+
			"structure, substituting <BUILD_ID> with the build id from step 1:\n"+
			"| Field | Value |\n|-------|-------|\n| build_id | <BUILD_ID> |\n| status | ok |\n"+
			"3. Run: cat %[2]s\n"+
			"Finally, reply with the exact contents of %[2]s (the markdown table).",
		buildIDPath, reportPath)

	answer, err := agent.Ask(ctx, task)
	if err != nil {
		t.Fatalf("agent.Ask: %v", err)
	}

	// Collect the structured stream the mcpagent layer emitted. Content arrives as
	// StreamingChunkEvent; tool calls arrive as ToolCallStartEvent (a distinct
	// event type at this layer) — both must appear for a streamed tool turn.
	// StreamingChunkEvent.Source now separates raw terminal frames from clean
	// content, so a no-terminal UI selects Source != "terminal" (no heuristics).
	var contentChunks, cleanContentChunks, toolChunks int
	var cleanTexts, toolNames []string
	for _, ev := range listener.events {
		switch d := ev.Data.(type) {
		case *events.StreamingChunkEvent:
			if d.IsToolCall || strings.TrimSpace(d.Content) == "" {
				continue
			}
			contentChunks++
			if d.Source != events.StreamingChunkSourceTerminal {
				cleanContentChunks++
				cleanTexts = append(cleanTexts, d.Content)
			}
		case *events.ToolCallStartEvent:
			toolChunks++
			toolNames = append(toolNames, d.ToolName)
		}
	}
	t.Logf("real-bridge stream: %d content chunk(s) (%d clean transcript, rest terminal), %d tool-call event(s) %v; answer=%q",
		contentChunks, cleanContentChunks, toolChunks, toolNames, strings.TrimSpace(answer))

	// The clean view must be free of raw terminal frames (ANSI escapes) now that
	// Source separates them — proves the fix on real output, not a heuristic.
	for _, c := range cleanTexts {
		if strings.Contains(c, "\x1b") {
			t.Fatalf("a Source!=terminal chunk still contained raw terminal ANSI: %q", c)
		}
	}

	// Real READ through the bridge: the build id (never in the prompt) proves the
	// tool genuinely ran the `cat build_id.txt` step.
	if !strings.Contains(answer, codeWord) {
		t.Fatalf("answer %q does not contain the file build id %q — the real shell tool did not run through the bridge", answer, codeWord)
	}
	// Real WRITE through the bridge: the model actually created report.md on disk.
	//nolint:gosec // G304: reportPath is a test-controlled temp path (t.TempDir()).
	report, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		t.Fatalf("report.md was not written by the real shell tool through the bridge: %v", readErr)
	}
	reportStr := string(report)
	// The written file is a real markdown table carrying the build id it just read.
	if !strings.Contains(reportStr, codeWord) || !strings.Contains(reportStr, "|") ||
		!strings.Contains(reportStr, "build_id") || !strings.Contains(reportStr, "status") {
		t.Fatalf("report.md is not the expected markdown table with the build id: %q", reportStr)
	}
	// Streaming through the real bridge: the tool call streamed as its own event...
	if toolChunks == 0 {
		t.Fatalf("no ToolCallStartEvent — the real bridge tool call did not stream to the mcpagent layer")
	}
	// ...and CLEAN transcript content (no raw terminal frames) reached the app,
	// INCLUDING the rich markdown table the model produced — i.e. a no-terminal UI
	// receives the renderable table, not just plain lines.
	if cleanContentChunks == 0 {
		t.Fatalf("no clean transcript content streamed (%d content chunks were all raw terminal frames)", contentChunks)
	}
	cleanJoined := strings.Join(cleanTexts, "\n")
	if !strings.Contains(cleanJoined, "|") || !strings.Contains(cleanJoined, codeWord) {
		t.Fatalf("the markdown table (pipes + build id) did not stream as clean content; clean stream:\n%s", cleanJoined)
	}

	rec := agentreview.Write(t, "TestRealBridgeStreaming_"+pc.name,
		pc.name+" via the REAL mcpbridge → executor → real execute_shell_command: read a build-id file, write a markdown table, read it back — streamed at the mcpagent layer",
		map[string]any{
			"clean_transcript_content": cleanTexts,
			"clean_content_count":      cleanContentChunks,
			"total_content_chunks":     contentChunks,
			"tool_call_events":         toolChunks,
			"tool_names":               toolNames,
			"answer":                   strings.TrimSpace(answer),
			"report_md_on_disk":        reportStr,
			"build_id_only_via_tool":   codeWord,
			"went_through_real_bridge": true,
		},
		map[string]any{"streamed_clean_content": cleanContentChunks > 0, "streamed_tool": toolChunks > 0, "streamed_table": strings.Contains(cleanJoined, "|")},
	)
	agentreview.RequireReviewed(t, rec)
}
