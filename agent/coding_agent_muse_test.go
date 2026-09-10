package mcpagent

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// TestAppendMuseCLIIntegrationOptionsRequiresBridge mirrors the cursor
// bridge-required test: with no bridge configured the appender must fail
// loudly, never launch muse with no product tools.
func TestAppendMuseCLIIntegrationOptionsRequiresBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_API_URL", "")
	t.Setenv("MCP_API_TOKEN", "")

	agent := bridgeTestAgent()
	if _, err := agent.appendMuseCLIIntegrationOptions(nil); err == nil {
		t.Fatal("appendMuseCLIIntegrationOptions() error = nil, want missing bridge config error")
	}
}

// TestAppendMuseCLIIntegrationOptionsMountsBridge mirrors the codex MCP test:
// with a bridge configured, the MCP config metadata must carry the api-bridge
// server (the settings.json merge reads this key), and a preset native
// session id must resolve to the muse resume metadata key.
func TestAppendMuseCLIIntegrationOptionsMountsBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.museSessionID = "stream-id-123"
	opts, err := agent.appendMuseCLIIntegrationOptions(nil)
	if err != nil {
		t.Fatalf("appendMuseCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)
	mcpConfig, ok := got[musecli.MetadataKeyMuseMCPConfig].(string)
	if !ok {
		t.Fatalf("Muse MCP config = %#v, want JSON string", got[musecli.MetadataKeyMuseMCPConfig])
	}
	for _, want := range []string{`"api-bridge"`, `"mcpServers"`} {
		if !strings.Contains(mcpConfig, want) {
			t.Fatalf("Muse MCP config missing %q:\n%s", want, mcpConfig)
		}
	}
	if got[musecli.MetadataKeyMuseResumeSessionID] != "stream-id-123" {
		t.Fatalf("Muse resume session = %#v, want stream-id-123", got[musecli.MetadataKeyMuseResumeSessionID])
	}
}

// TestAppendMuseCLIIntegrationOptionsRegisteredInAppenders guards the wiring:
// the provider-keyed appender map must route muse-cli to the muse appender so
// a muse Agent actually mounts the bridge instead of running bare.
func TestAppendMuseCLIIntegrationOptionsRegisteredInAppenders(t *testing.T) {
	t.Setenv("MCP_BRIDGE_API_URL", "")
	t.Setenv("MCP_API_TOKEN", "")

	agent := bridgeTestAgent()
	appender, ok := codingAgentIntegrationAppenders["muse-cli"]
	if !ok {
		t.Fatal("codingAgentIntegrationAppenders has no muse-cli entry")
	}
	if _, err := appender(agent, nil, LLMModel{}); err == nil {
		t.Fatal("muse appender error = nil, want missing bridge config error")
	}
}
