package mcpagent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestLookupBridgeToolSynthesizesGetAPISpecWhenFilteredFromTools(t *testing.T) {
	agent := &Agent{}

	def := agent.lookupBridgeTool("get_api_spec", "virtual", loggerv2.NewDefault())
	if def == nil {
		t.Fatal("expected get_api_spec bridge tool definition")
	}
	if def.Name != "get_api_spec" {
		t.Fatalf("expected get_api_spec, got %q", def.Name)
	}
	if def.Type != "virtual" {
		t.Fatalf("expected virtual bridge tool, got %q", def.Type)
	}
	if len(def.InputSchema) == 0 {
		t.Fatal("expected get_api_spec input schema")
	}
}

func TestIsCodingCLIProviderExcludesKimiAPIProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider llm.Provider
		modelID  string
		want     bool
	}{
		{name: "claude code", provider: llm.ProviderClaudeCode, want: true},
		{name: "gemini cli", provider: llm.ProviderGeminiCLI, want: true},
		{name: "codex cli", provider: llm.ProviderCodexCLI, want: true},
		{name: "cursor cli", provider: llm.ProviderCursorCLI, want: true},
		{name: "agy cli", provider: llm.ProviderAgyCLI, want: true},
		{name: "opencode cli", provider: llm.ProviderOpenCodeCLI, want: true},
		{name: "kimi api model", provider: llm.ProviderKimi, modelID: "kimi-k2.6", want: false},
		{name: "anthropic", provider: llm.ProviderAnthropic, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCodingCLIProvider(tt.provider, tt.modelID); got != tt.want {
				t.Fatalf("isCodingCLIProvider(%q, %q) = %v, want %v", tt.provider, tt.modelID, got, tt.want)
			}
		})
	}
}

func TestGeminiRestrictToolsPolicyUsesCurrentMCPToolSyntax(t *testing.T) {
	policy := geminiRestrictToolsPolicyContent()

	for _, want := range []string{
		`toolName = "mcp_api-bridge_*"`,
		`toolName = "google_web_search"`,
		`toolName = "*"`,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}

	for _, deprecated := range []string{
		"mcp__api-bridge__",
		"tools.exclude",
	} {
		if strings.Contains(policy, deprecated) {
			t.Fatalf("policy contains deprecated syntax %q:\n%s", deprecated, policy)
		}
	}
}

func bridgeTestAgent() *Agent {
	return &Agent{Logger: loggerv2.NewDefault()}
}

func TestBuildBridgeMCPConfigSessionURLEmbedding(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
	agent.SessionID = "sess-abc-123"

	configJSON, err := agent.BuildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("BuildBridgeMCPConfig() error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	servers := config["mcpServers"].(map[string]interface{})
	bridge := servers["api-bridge"].(map[string]interface{})
	env := bridge["env"].(map[string]interface{})

	apiURL := env["MCP_API_URL"].(string)
	if apiURL != "http://localhost:8080/s/sess-abc-123" {
		t.Fatalf("MCP_API_URL = %q, want session-scoped URL", apiURL)
	}
	if env["MCP_API_TOKEN"].(string) != "test-token-123" {
		t.Fatalf("MCP_API_TOKEN mismatch")
	}
	if bridge["command"].(string) != "/usr/local/bin/mcpbridge" {
		t.Fatalf("command mismatch")
	}
	if bridge["trust"] != true {
		t.Fatal("trust should be true")
	}
}

func TestBuildBridgeMCPConfigNoSessionID(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	configJSON, err := agent.BuildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("BuildBridgeMCPConfig() error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	servers := config["mcpServers"].(map[string]interface{})
	bridge := servers["api-bridge"].(map[string]interface{})
	env := bridge["env"].(map[string]interface{})

	if env["MCP_API_URL"].(string) != "http://localhost:8080" {
		t.Fatalf("MCP_API_URL should not have session prefix when SessionID empty")
	}
}

func TestBuildBridgeMCPConfigBridgeURLOverride(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_BRIDGE_API_URL", "http://host-reachable:9090")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.SessionID = "s1"
	configJSON, err := agent.BuildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("BuildBridgeMCPConfig() error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	servers := config["mcpServers"].(map[string]interface{})
	bridge := servers["api-bridge"].(map[string]interface{})
	env := bridge["env"].(map[string]interface{})

	if env["MCP_API_URL"].(string) != "http://host-reachable:9090/s/s1" {
		t.Fatalf("MCP_BRIDGE_API_URL should take priority over MCP_API_URL, got %q", env["MCP_API_URL"])
	}
}

func TestBuildBridgeMCPConfigMissingURL(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	os.Unsetenv("MCP_API_URL")
	os.Unsetenv("MCP_BRIDGE_API_URL")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	_, err := agent.BuildBridgeMCPConfig()
	if err == nil {
		t.Fatal("expected error when API URL not configured")
	}
}

func TestBuildBridgeMCPConfigMissingToken(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	os.Unsetenv("MCP_API_TOKEN")

	agent := bridgeTestAgent()
	_, err := agent.BuildBridgeMCPConfig()
	if err == nil {
		t.Fatal("expected error when API token not configured")
	}
}

func TestBuildBridgeMCPConfigAPIBaseURLPriority(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://env-url:9090")
	t.Setenv("MCP_API_TOKEN", "env-token")

	agent := bridgeTestAgent()
	agent.APIBaseURL = "http://agent-url:7070"
	agent.APIToken = "agent-token"
	configJSON, err := agent.BuildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("BuildBridgeMCPConfig() error: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	servers := config["mcpServers"].(map[string]interface{})
	bridge := servers["api-bridge"].(map[string]interface{})
	env := bridge["env"].(map[string]interface{})

	if env["MCP_API_URL"].(string) != "http://agent-url:7070" {
		t.Fatalf("APIBaseURL should take priority, got %q", env["MCP_API_URL"])
	}
	if env["MCP_API_TOKEN"].(string) != "agent-token" {
		t.Fatalf("APIToken should take priority, got %q", env["MCP_API_TOKEN"])
	}
}

func TestBridgeToolsList(t *testing.T) {
	expected := map[string]string{
		"execute_shell_command":     "custom",
		"diff_patch_workspace_file": "custom",
		"agent_browser":             "custom",
		"get_api_spec":              "virtual",
	}

	if len(bridgeTools) != len(expected) {
		t.Fatalf("bridgeTools count = %d, want %d", len(bridgeTools), len(expected))
	}
	for _, bt := range bridgeTools {
		wantType, ok := expected[bt.name]
		if !ok {
			t.Fatalf("unexpected bridge tool %q", bt.name)
		}
		if bt.toolType != wantType {
			t.Fatalf("bridge tool %q type = %q, want %q", bt.name, bt.toolType, wantType)
		}
	}
}
