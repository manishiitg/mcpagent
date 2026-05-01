package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestKimiCLIBridgeE2E(t *testing.T) {
	if os.Getenv("KIMI_CLI_BRIDGE_E2E") != "1" {
		t.Skip("set KIMI_CLI_BRIDGE_E2E=1 to run the live Kimi CLI bridge test")
	}
	if _, err := exec.LookPath("kimi"); err != nil {
		t.Skipf("kimi CLI not found in PATH: %v", err)
	}

	logger := loggerv2.NewDefault()
	bridgePath := ensureTestBridgeBinary(t)
	mock := startKimiBridgeMockAPIServer(t, logger)
	defer mock.shutdown()

	workDir := t.TempDir()
	configPath := filepath.Join(workDir, "mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatalf("write mcp config: %v", err)
	}

	t.Setenv("KIMI_CODE_TRANSPORT", "")
	t.Setenv("KIMI_CODE_CLI_TOOL_MODE", "none")
	t.Setenv("KIMI_CODE_CLI_WORK_DIR", workDir)
	t.Setenv("KIMI_CODE_CLI_MAX_STEPS_PER_TURN", "8")
	t.Setenv("MCP_BRIDGE_BINARY", bridgePath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderKimi,
		ModelID:  "kimi-code",
		Logger:   logger,
		Context:  ctx,
	})
	if err != nil {
		t.Fatalf("initialize Kimi CLI model: %v", err)
	}

	agent, err := NewAgent(ctx, model, configPath,
		WithProvider(llm.ProviderKimi),
		WithCodeExecutionMode(true),
		WithAPIConfig(mock.url, mock.apiToken),
		WithSessionID("kimi-bridge-e2e"),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer agent.Close()

	if err := registerKimiBridgeToolStubs(agent); err != nil {
		t.Fatalf("register bridge tool stubs: %v", err)
	}

	response, askErr := agent.Ask(ctx, "Use the api-bridge MCP tool execute_shell_command with command=\"echo kimi-bridge-ok\". Reply with only the observed output. If you need to discover tools first, use api-bridge get_api_spec.")
	if askErr != nil {
		t.Logf("Ask returned error; checking whether bridge was still reached: %v", askErr)
	} else {
		t.Logf("Kimi response: %s", truncateForTest(response, 500))
	}

	reqs := mock.getRequests()
	found := false
	var foundPath string
	for _, req := range reqs {
		if strings.Contains(req.Path, "execute_shell_command") || strings.Contains(req.Path, "get_api_spec") {
			found = true
			foundPath = req.Path
			break
		}
	}
	if !found {
		if askErr != nil {
			t.Fatalf("Kimi CLI did not reach mcpbridge mock server and Ask failed: %v", askErr)
		}
		t.Fatalf("Kimi CLI did not reach mcpbridge mock server; received %d mock requests", len(reqs))
	}
	t.Logf("bridge reached mock server via %s", foundPath)
}

func TestKimiCodeCLITransportEnabledDefaultsToKimiCodeCLI(t *testing.T) {
	t.Setenv("KIMI_CODE_TRANSPORT", "")
	if !kimiCodeCLITransportEnabled("kimi-code") {
		t.Fatal("kimi-code should default to CLI transport")
	}
	if kimiCodeCLITransportEnabled("kimi-k2.6") {
		t.Fatal("non-kimi-code models should not use CLI transport by default")
	}

	t.Setenv("KIMI_CODE_TRANSPORT", "http")
	if kimiCodeCLITransportEnabled("kimi-code") {
		t.Fatal("KIMI_CODE_TRANSPORT=http should force HTTP transport")
	}
}

func ensureTestBridgeBinary(t *testing.T) string {
	t.Helper()

	if envPath := os.Getenv("MCP_BRIDGE_BINARY"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
	}
	if path, err := exec.LookPath("mcpbridge"); err == nil {
		return path
	}

	root := findTestMcpagentRoot(t)
	out := filepath.Join(t.TempDir(), "mcpbridge")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/mcpbridge/") // #nosec G204 -- test builds the repo-local mcpbridge binary with fixed arguments.
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build mcpbridge: %v\n%s", err, string(output))
	}
	return out
}

func findTestMcpagentRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working dir: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "cmd", "mcpbridge")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find mcpagent repo root")
		}
		dir = parent
	}
}

func registerKimiBridgeToolStubs(agent *Agent) error {
	stubs := []struct {
		name   string
		desc   string
		params map[string]interface{}
	}{
		{
			name:   "execute_shell_command",
			desc:   codeexec.ShellCommandDescription,
			params: codeexec.ShellCommandParams,
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
		},
	}

	for _, stub := range stubs {
		if err := agent.RegisterCustomTool(stub.name, stub.desc, stub.params, func(_ context.Context, _ map[string]interface{}) (string, error) {
			return "stub-not-called", nil
		}, "mcp_bridge"); err != nil {
			return fmt.Errorf("register %s: %w", stub.name, err)
		}
	}
	return nil
}

type kimiBridgeRequestLog struct {
	Path string
	Body map[string]interface{}
}

type kimiBridgeMockAPIServer struct {
	url      string
	apiToken string
	server   *http.Server
	mu       sync.Mutex
	requests []kimiBridgeRequestLog
	shutdown func()
}

func (m *kimiBridgeMockAPIServer) getRequests() []kimiBridgeRequestLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]kimiBridgeRequestLog, len(m.requests))
	copy(out, m.requests)
	return out
}

func startKimiBridgeMockAPIServer(t *testing.T, logger loggerv2.Logger) *kimiBridgeMockAPIServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mock := &kimiBridgeMockAPIServer{
		url:      "http://" + ln.Addr().String(),
		apiToken: "test-token-kimi-cli",
	}

	mux := http.NewServeMux()
	toolsHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mock.apiToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "unauthorized"})
			return
		}

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		mock.mu.Lock()
		mock.requests = append(mock.requests, kimiBridgeRequestLog{Path: r.URL.Path, Body: body})
		mock.mu.Unlock()

		logger.Info("Kimi bridge mock received tool call", loggerv2.String("path", r.URL.Path))

		result := "mock-ok"
		switch {
		case strings.Contains(r.URL.Path, "execute_shell_command"):
			result = "exit_code: 0\nstdout:\nkimi-bridge-ok\nstderr:"
		case strings.Contains(r.URL.Path, "get_api_spec"):
			result = `{"openapi":"3.0.0","info":{"title":"mock-api"},"paths":{"/tools/custom/execute_shell_command":{"post":{"operationId":"execute_shell_command"}}}}`
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  result,
		})
	}
	mux.HandleFunc("/tools/", toolsHandler)
	mux.HandleFunc("/s/", toolsHandler)

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	mock.server = server
	mock.shutdown = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}

	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Warn("Kimi bridge mock server error", loggerv2.Error(err))
		}
	}()

	return mock
}

func truncateForTest(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
