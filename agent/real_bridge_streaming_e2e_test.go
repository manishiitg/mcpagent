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

// TestRealBridgeStreamingClaudeE2E is the production-fidelity streaming test the
// stand-in-MCP-server tests were missing: a real Claude Code turn whose tools go
// through the REAL mcpbridge → executor HTTP API → a REAL mcpagent tool
// (execute_shell_command running an actual shell), with structured streaming
// captured at the mcpagent layer (events.StreamingChunkEvent). It proves the
// whole production path streams: bridge tool-call chunks reach the app AND the
// real shell tool actually ran (wrote + read a file on disk).
//
// Gated by RUN_MCPAGENT_REAL_BRIDGE_E2E=1; requires an authenticated `claude`
// CLI, tmux, and go (to build the bridge). No node / stand-in server.
func TestRealBridgeStreamingClaudeE2E(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge streaming e2e")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("authenticated `claude` CLI required")
	}

	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))
	// Structured transcript streaming is opt-in on the claude adapter.
	t.Setenv("CLAUDE_CODE_STREAM_TRANSCRIPT", "1")

	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	apiURL, apiToken := startRealExecutorServer(t, configPath)

	llmModel, err := llm.InitializeLLM(llm.Config{Provider: llm.ProviderClaudeCode, ModelID: "claude-haiku-4-5"})
	if err != nil {
		t.Fatalf("InitializeLLM: %v", err)
	}

	workDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	agent, err := NewAgent(ctx, llmModel, configPath,
		WithProvider(llm.ProviderClaudeCode),
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

	// The secret lives ONLY in a pre-seeded file, NEVER in the prompt, so the
	// model cannot answer without actually running the shell tool through the
	// bridge. Absolute path so it's independent of the shell tool's cwd.
	secret := "REALBRIDGE_SECRET_" + realBridgeRandHex(6)
	secretPath := filepath.Join(workDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	task := fmt.Sprintf(
		"You have one tool: execute_shell_command, which runs a shell command and returns its output. "+
			"Write one short sentence of narration, then use execute_shell_command to run EXACTLY this command:\n"+
			"  cat %s\n"+
			"Then reply on one line with the exact output of that command (a token you do not otherwise know).",
		secretPath)

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

	// Real work through the REAL bridge: the ONLY way to know the secret is to
	// have actually run `cat secret.txt` via execute_shell_command → mcpbridge →
	// executor → real shell. Its presence in the answer proves the whole path ran
	// (the secret was never in the prompt).
	if !strings.Contains(answer, secret) {
		t.Fatalf("answer %q does not contain the file secret %q — the real shell tool did not run through the bridge", answer, secret)
	}
	// Streaming through the real bridge: the tool call streamed as its own event...
	if toolChunks == 0 {
		t.Fatalf("no ToolCallStartEvent — the real bridge tool call did not stream to the mcpagent layer")
	}
	// ...and CLEAN transcript content (not merely raw terminal frames) reached the
	// app — the assistant's actual words that a no-terminal UI would render.
	if cleanContentChunks == 0 {
		t.Fatalf("no clean transcript content streamed (%d content chunks were all raw terminal frames)", contentChunks)
	}

	rec := agentreview.Write(t, "TestRealBridgeStreamingClaudeE2E",
		"Claude via the REAL mcpbridge → executor → real execute_shell_command (cat a pre-seeded secret), streamed at the mcpagent layer",
		map[string]any{
			"clean_transcript_content": cleanTexts,
			"clean_content_count":      cleanContentChunks,
			"total_content_chunks":     contentChunks,
			"tool_call_events":         toolChunks,
			"tool_names":               toolNames,
			"answer":                   strings.TrimSpace(answer),
			"secret_only_via_tool":     secret,
			"went_through_real_bridge": true,
		},
		map[string]any{"streamed_clean_content": cleanContentChunks > 0, "streamed_tool": toolChunks > 0},
	)
	agentreview.RequireReviewed(t, rec)
}
