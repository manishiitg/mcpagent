package mcpagent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
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

// TestMuseUsesStructuredTransportWhileExecOnly pins the transport override:
// the provider contract declares tmux, but mcpagent runs the exec lane
// (the tmux lane is explicit opt-in the orchestrator never requests), so
// orchestration must treat muse as structured (queue delivery, structured
// continuation handles).
func TestMuseUsesStructuredTransportWhileExecOnly(t *testing.T) {
	agent := bridgeTestAgent()
	agent.provider = llm.ProviderMuseCLI
	if !agent.usesStructuredTransport() {
		t.Fatal("usesStructuredTransport = false for muse-cli, want true while exec-only")
	}
	if agent.supportsSteering() {
		t.Fatal("supportsSteering = true for muse-cli, want false (no live pane exists)")
	}
}

// TestMuseLayer2LiveTurn is the layer-2 evidence: two ContinueConversation
// turns through the REAL bridge and the real Meta model. Turn 1 states a
// code word, turn 2 recalls it — only provider-native --resume (structured
// transport, no persistent tmux) can supply it, so the recall proves the
// orchestrator's handle plumbing end to end. No tmux-kill step: the exec
// lane has no live session to destroy.
//
// Gate: RUN_MCPAGENT_MUSE_LIVE=1 plus stored `muse login` (Meta auth).
func TestMuseLayer2LiveTurn(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_MUSE_LIVE") != "1" {
		t.Skip("set RUN_MCPAGENT_MUSE_LIVE=1 to run the live muse layer-2 turn")
	}
	if _, err := exec.LookPath("muse"); err != nil {
		t.Skip("muse CLI required")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	tc := multiTurnProviderCase{
		name: "Muse", binary: "muse",
		provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor",
		persistentOpt:    func(bool) agentOption { return func(a *Agent) {} },
		strictBridgeOnly: false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	workDir := t.TempDir()
	convID := "muse-layer2-" + realBridgeRandHex(4)
	codeWord := "MUSEWORD_" + realBridgeRandHex(6) // only ever spoken, never written
	agent, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, convID, false)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	defer cleanup()
	store := NewFileCodingSessionStore(t.TempDir())

	ans1, err := agent.continueConversation(ctx, convID,
		"Please remember this code word for later: "+codeWord+". Just reply OK — do not use tools, do not write it anywhere.", store)
	if err != nil {
		t.Fatalf("turn 1 (state code word): %v", err)
	}
	t.Logf("turn 1 answer: %q", strings.TrimSpace(ans1))
	handle := agent.currentAgentSessionHandle()
	if handle == nil || handle.Provider.NativeSessionID == "" {
		t.Fatalf("turn 1 produced no resumable native session id: %+v", handle)
	}

	ans2, err := agent.continueConversation(ctx, convID,
		"What was the exact code word I asked you to remember earlier? Reply with ONLY that word.", store)
	if err != nil {
		t.Fatalf("turn 2 (recall): %v", err)
	}
	if recall := strings.TrimSpace(ans2); !strings.Contains(recall, codeWord) {
		t.Fatalf("layer-2 continuity FAILED: recall %q does not contain %q (native resume did not restore memory)", recall, codeWord)
	}
}
