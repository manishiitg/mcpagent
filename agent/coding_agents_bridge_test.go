package mcpagent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/geminicli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
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
		{name: "pi cli", provider: llm.ProviderPiCLI, want: true},
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

func TestIsCodingCLIBridgeProviderIncludesPiWhenBridgeMounted(t *testing.T) {
	if !isCodingCLIBridgeProvider(llm.ProviderCodexCLI, "gpt-5.4") {
		t.Fatal("codex-cli should be recognized as a coding CLI bridge provider")
	}
	if !isCodingCLIBridgeProvider(llm.ProviderPiCLI, "google/gemini-3.5-flash") {
		t.Fatal("pi-cli should be treated as bridge-capable through pi-mcp-adapter")
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

func TestBridgeRoutingExplicitInstructionsIncludesCustomLLMTools(t *testing.T) {
	prompt := bridgeRoutingExplicitInstructions()
	for _, want := range []string{
		"mcp({ search:",
		"sub_agent_tools",
		"$MCP_CUSTOM/list_published_llms",
		"$MCP_CUSTOM/list_provider_models",
		"$MCP_CUSTOM/save_published_llm",
		"Do not read or edit config/ files for LLM/provider configuration",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("bridge routing prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unwanted := range []string{
		"api_bridge_call_sub_agent",
		"api_bridge_get_route_description",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("bridge routing prompt should not advertise sub-agent tools as native bridge tools: found %q\n%s", unwanted, prompt)
		}
	}
}

func TestBuildBridgeMCPConfigStaticURLWithSessionHeader(t *testing.T) {
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
	if apiURL != "http://localhost:8080" {
		t.Fatalf("MCP_API_URL = %q, want static URL http://localhost:8080", apiURL)
	}
	if env["MCP_SESSION_ID"].(string) != "sess-abc-123" {
		t.Fatalf("MCP_SESSION_ID = %q, want sess-abc-123", env["MCP_SESSION_ID"])
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

func TestBuildBridgeMCPConfigNormalizesMarkdownAPIURL(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "[http://127.0.0.1:45678](http://127.0.0.1:45678)")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
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
	if got := env["MCP_API_URL"].(string); got != "http://127.0.0.1:45678" {
		t.Fatalf("MCP_API_URL = %q, want plain URL", got)
	}
}

func TestBuildBridgeMCPConfigRejectsInvalidAPIURL(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "not a url")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
	_, err := agent.BuildBridgeMCPConfig()
	if err == nil || !strings.Contains(err.Error(), "invalid MCP bridge API URL") {
		t.Fatalf("BuildBridgeMCPConfig() error = %v, want invalid URL error", err)
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

	if env["MCP_API_URL"].(string) != "http://host-reachable:9090" {
		t.Fatalf("MCP_BRIDGE_API_URL should take priority over MCP_API_URL, got %q", env["MCP_API_URL"])
	}
	if env["MCP_SESSION_ID"].(string) != "s1" {
		t.Fatalf("MCP_SESSION_ID = %q, want s1", env["MCP_SESSION_ID"])
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

func TestAppendAgyCLIIntegrationOptionsEnablesBridgeOnlyHooks(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.SessionID = "app-session"
	agent.AgySessionID = "agy-conversation-id"

	opts, err := agent.appendAgyCLIIntegrationOptions(nil)
	if err != nil {
		t.Fatalf("appendAgyCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)

	mcpConfig, ok := got[agycli.MetadataKeyMCPConfig].(string)
	if !ok || !strings.Contains(mcpConfig, `"api-bridge"`) {
		t.Fatalf("Agy MCP config metadata = %#v, want api-bridge config", got[agycli.MetadataKeyMCPConfig])
	}
	if got[agycli.MetadataKeyBridgeOnlyTools] != true {
		t.Fatalf("Agy bridge-only metadata = %#v, want true", got[agycli.MetadataKeyBridgeOnlyTools])
	}
	if got[agycli.MetadataKeyResumeSessionID] != "agy-conversation-id" {
		t.Fatalf("Agy resume metadata = %#v, want agy-conversation-id", got[agycli.MetadataKeyResumeSessionID])
	}
}

func TestAppendCursorCLIIntegrationOptionsEnablesBridgeAndDenyHooks(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.SessionID = "app-session"

	opts, err := agent.appendCursorCLIIntegrationOptions(nil)
	if err != nil {
		t.Fatalf("appendCursorCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)

	mcpConfig, ok := got[cursorcli.MetadataKeyMCPConfig].(string)
	if !ok || !strings.Contains(mcpConfig, `"api-bridge"`) {
		t.Fatalf("Cursor MCP config metadata = %#v, want api-bridge config", got[cursorcli.MetadataKeyMCPConfig])
	}
	tools := bridgeToolsFromConfig(t, mcpConfig)
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "agent_browser", "get_api_spec"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("Cursor MCP config missing core bridge tool %q; tools=%v", name, mapKeys(tools))
		}
	}
	if got[cursorcli.MetadataKeyApproveMCPs] != true {
		t.Fatalf("Cursor approve-mcps metadata = %#v, want true", got[cursorcli.MetadataKeyApproveMCPs])
	}
	if got[cursorcli.MetadataKeyDenyBuiltinTools] != true {
		t.Fatalf("Cursor deny-builtin metadata = %#v, want true", got[cursorcli.MetadataKeyDenyBuiltinTools])
	}
	if _, ok := got[cursorcli.MetadataKeyForce]; ok {
		t.Fatalf("Cursor force metadata should not be set when bridge is configured: %#v", got[cursorcli.MetadataKeyForce])
	}
}

func TestCursorRunloopChatOptionsCarryBridgeAndWebAutoApproval(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.provider = llm.ProviderCursorCLI
	agent.ModelID = "cursor-cli"
	agent.SessionID = "app-session"
	agent.CursorPersistentInteractiveSession = true
	agent.CursorBridgeToolsMode = true

	opts := agent.appendCodingAgentInteractiveOptions(nil)
	var err error
	opts, err = agent.appendCursorCLIIntegrationOptions(opts)
	if err != nil {
		t.Fatalf("appendCursorCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)

	if got[cursorcli.MetadataKeyInteractiveSessionID] != "app-session" {
		t.Fatalf("Cursor interactive session metadata = %#v, want app-session", got[cursorcli.MetadataKeyInteractiveSessionID])
	}
	if got[cursorcli.MetadataKeyPersistentInteractive] != true {
		t.Fatalf("Cursor persistent metadata = %#v, want true", got[cursorcli.MetadataKeyPersistentInteractive])
	}
	if got[cursorcli.MetadataKeyAutoApproveWebSearch] != true {
		t.Fatalf("Cursor web auto-approval metadata = %#v, want true", got[cursorcli.MetadataKeyAutoApproveWebSearch])
	}
	mcpConfig, ok := got[cursorcli.MetadataKeyMCPConfig].(string)
	if !ok || !strings.Contains(mcpConfig, `"api-bridge"`) {
		t.Fatalf("Cursor MCP config metadata = %#v, want api-bridge config", got[cursorcli.MetadataKeyMCPConfig])
	}
	tools := bridgeToolsFromConfig(t, mcpConfig)
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "agent_browser", "get_api_spec"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("Cursor MCP config missing core bridge tool %q; tools=%v", name, mapKeys(tools))
		}
	}
	if got[cursorcli.MetadataKeyApproveMCPs] != true {
		t.Fatalf("Cursor approve-mcps metadata = %#v, want true", got[cursorcli.MetadataKeyApproveMCPs])
	}
	if got[cursorcli.MetadataKeyDenyBuiltinTools] != true {
		t.Fatalf("Cursor deny-builtin metadata = %#v, want true", got[cursorcli.MetadataKeyDenyBuiltinTools])
	}
	if _, ok := got[cursorcli.MetadataKeyMode]; ok {
		t.Fatalf("Cursor app path should not force --mode ask; metadata=%#v", got)
	}
	if _, ok := got[cursorcli.MetadataKeyForce]; ok {
		t.Fatalf("Cursor app path should not force yolo mode; metadata=%#v", got)
	}
}

func TestAppendCursorCLIIntegrationOptionsRequiresMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "")
	t.Setenv("MCP_BRIDGE_API_URL", "")
	t.Setenv("MCP_API_TOKEN", "")

	agent := bridgeTestAgent()
	if _, err := agent.appendCursorCLIIntegrationOptions(nil); err == nil {
		t.Fatal("appendCursorCLIIntegrationOptions() error = nil, want missing bridge config error")
	}
}

func TestAppendAgyCLIIntegrationOptionsRequiresMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "")
	t.Setenv("MCP_BRIDGE_API_URL", "")
	t.Setenv("MCP_API_TOKEN", "")

	agent := bridgeTestAgent()
	if _, err := agent.appendAgyCLIIntegrationOptions(nil); err == nil {
		t.Fatal("appendAgyCLIIntegrationOptions() error = nil, want missing bridge config error")
	}
}

func TestAppendCodexCLIIntegrationOptionsEnablesMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	opts, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
	if err != nil {
		t.Fatalf("appendCodexCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)
	overrides, ok := got[codexcli.MetadataKeyConfigOverrides].([]string)
	if !ok {
		t.Fatalf("Codex config overrides = %#v, want []string", got[codexcli.MetadataKeyConfigOverrides])
	}
	joined := strings.Join(overrides, "\n")
	for _, want := range []string{"mcp_servers.api-bridge.command", "mcp_servers.api-bridge.env.MCP_API_URL", "mcp_servers.api-bridge.env.MCP_API_TOKEN"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Codex config overrides missing %q:\n%s", want, joined)
		}
	}
}

func TestAppendCodexCLIIntegrationOptionsRequiresMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "")
	t.Setenv("MCP_BRIDGE_API_URL", "")
	t.Setenv("MCP_API_TOKEN", "")

	agent := bridgeTestAgent()
	if _, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{}); err == nil {
		t.Fatal("appendCodexCLIIntegrationOptions() error = nil, want missing bridge config error")
	}
}

func TestAppendGeminiCLIIntegrationOptionsEnablesMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	opts, err := agent.appendGeminiCLIIntegrationOptions(nil)
	if err != nil {
		t.Fatalf("appendGeminiCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)
	settingsJSON, ok := got[geminicli.MetadataKeyProjectSettings].(string)
	if !ok || !strings.Contains(settingsJSON, `"api-bridge"`) {
		t.Fatalf("Gemini project settings = %#v, want api-bridge settings", got[geminicli.MetadataKeyProjectSettings])
	}
}

func TestAppendGeminiCLIIntegrationOptionsRequiresMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "")
	t.Setenv("MCP_BRIDGE_API_URL", "")
	t.Setenv("MCP_API_TOKEN", "")

	agent := bridgeTestAgent()
	if _, err := agent.appendGeminiCLIIntegrationOptions(nil); err == nil {
		t.Fatal("appendGeminiCLIIntegrationOptions() error = nil, want missing bridge config error")
	}
}

func TestAppendPiCLIIntegrationOptionsEnablesMCPBridgeOnlyTools(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.SessionID = "app-session"

	opts, err := agent.appendPiCLIIntegrationOptions(nil)
	if err != nil {
		t.Fatalf("appendPiCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)

	mcpConfig, ok := got[picli.MetadataKeyMCPConfig].(string)
	if !ok || !strings.Contains(mcpConfig, `"api-bridge"`) {
		t.Fatalf("Pi MCP config metadata = %#v, want api-bridge config", got[picli.MetadataKeyMCPConfig])
	}
	tools := bridgeToolsFromConfig(t, mcpConfig)
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "agent_browser", "get_api_spec"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("Pi MCP config missing core bridge tool %q; tools=%v", name, mapKeys(tools))
		}
	}
	for _, name := range []string{"call_sub_agent", "call_generic_agent", "get_route_description", "get_sub_agent_conversation"} {
		if _, ok := tools[name]; ok {
			t.Fatalf("Pi MCP config must not expose sub-agent tool %q as a native bridge tool; tools=%v", name, mapKeys(tools))
		}
	}
	if got[picli.MetadataKeyBridgeOnlyTools] != true {
		t.Fatalf("Pi bridge-only metadata = %#v, want true", got[picli.MetadataKeyBridgeOnlyTools])
	}
}

func TestAppendPiCLIIntegrationOptionsRequiresMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "")
	t.Setenv("MCP_BRIDGE_API_URL", "")
	t.Setenv("MCP_API_TOKEN", "")

	agent := bridgeTestAgent()
	if _, err := agent.appendPiCLIIntegrationOptions(nil); err == nil {
		t.Fatal("appendPiCLIIntegrationOptions() error = nil, want missing bridge config error")
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

func bridgeToolsFromConfig(t *testing.T, configJSON string) map[string]BridgeToolDef {
	t.Helper()

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("invalid config JSON: %v", err)
	}
	servers := config["mcpServers"].(map[string]interface{})
	bridge := servers["api-bridge"].(map[string]interface{})
	env := bridge["env"].(map[string]interface{})
	toolsJSON := env["MCP_TOOLS"].(string)

	var defs []BridgeToolDef
	if err := json.Unmarshal([]byte(toolsJSON), &defs); err != nil {
		t.Fatalf("invalid MCP_TOOLS JSON: %v", err)
	}

	tools := make(map[string]BridgeToolDef, len(defs))
	for _, def := range defs {
		tools[def.Name] = def
	}
	return tools
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
