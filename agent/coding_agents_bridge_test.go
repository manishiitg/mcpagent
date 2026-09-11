package mcpagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
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
		{name: "codex cli", provider: llm.ProviderCodexCLI, want: true},
		{name: "cursor cli", provider: llm.ProviderCursorCLI, want: true},
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
	if !isCodingCLIBridgeProvider(llm.ProviderPiCLI, "google/gemini-3.7-flash") {
		t.Fatal("pi-cli should be treated as bridge-capable through pi-mcp-adapter")
	}
}

func bridgeTestAgent() *Agent {
	return &Agent{logger: loggerv2.NewDefault()}
}

func TestBridgeRoutingExplicitInstructionsIncludesCustomLLMTools(t *testing.T) {
	prompt := bridgeRoutingExplicitInstructions(nil)
	for _, want := range []string{
		"Omit server_name normally",
		"$MCP_CUSTOM/list_published_llms",
		"$MCP_CUSTOM/list_provider_models",
		"$MCP_CUSTOM/save_published_llm",
		"Do not read or edit config/ files for LLM/provider configuration",
		"$MCP_CUSTOM/human_feedback",
		"curl in the FOREGROUND",
		"Never use nohup",
		"poll for completion",
		"returned body resumes your turn automatically",
		"at most 45 seconds",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("bridge routing prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unwanted := range []string{
		"api_bridge_call_sub_agent",
		"api_bridge_get_route_description",
		"custom categories",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("bridge routing prompt should not advertise sub-agent tools as native bridge tools: found %q\n%s", unwanted, prompt)
		}
	}
}

// A profile that removes a core bridge tool must not have it advertised anyway.
// defaultBridgeToolDef synthesizes a definition for an unregistered core tool,
// so before bridgeToolAdmit the CLI was handed execute_shell_command — whose
// description tells it to use it for HTTP calls — and every call then failed
// with "not registered for session".
func TestBuildBridgeMCPConfigOmitsCoreToolsTheProfileExcluded(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	names := func(t *testing.T, configJSON string) []string {
		t.Helper()
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		servers := config["mcpServers"].(map[string]interface{})
		bridge := servers["api-bridge"].(map[string]interface{})
		env := bridge["env"].(map[string]interface{})
		var defs []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(env["MCP_TOOLS"].(string)), &defs); err != nil {
			t.Fatalf("invalid MCP_TOOLS: %v", err)
		}
		out := make([]string, 0, len(defs))
		for _, d := range defs {
			out = append(out, d.Name)
		}
		return out
	}

	// Baseline: no predicate means no change for every existing caller.
	base := bridgeTestAgent()
	baseJSON, err := base.buildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("buildBridgeMCPConfig() error: %v", err)
	}
	baseNames := names(t, baseJSON)
	if !slices.Contains(baseNames, "execute_shell_command") {
		t.Fatalf("nil predicate must advertise the core tools unchanged, got %v", baseNames)
	}

	// A hybrid profile whose allowlist keeps agent_browser but not the shell or
	// the diff tool -- the CLI supplies its own for those.
	allowed := map[string]bool{"agent_browser": true}
	gated := bridgeTestAgent()
	gated.bridgeToolAdmit = func(name string) bool { return allowed[name] }
	gated.additionalBridgeTools = []string{"product_extra_tool"}
	if err := gated.registerDirectTool("product_extra_tool", "An explicitly added bridge tool.", map[string]interface{}{"type": "object"},
		func(context.Context, map[string]interface{}) (string, error) { return "", nil }, 0, "skills"); err != nil {
		t.Fatalf("register product_extra_tool: %v", err)
	}
	gatedJSON, err := gated.buildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("buildBridgeMCPConfig() error: %v", err)
	}
	gatedNames := names(t, gatedJSON)

	for _, excluded := range []string{"execute_shell_command", "diff_patch_workspace_file"} {
		if slices.Contains(gatedNames, excluded) {
			t.Fatalf("excluded core tool %q was still advertised: %v", excluded, gatedNames)
		}
	}
	// The discovery door and an explicitly-added tool never went through the
	// registration predicate, so the filter must not consult it for them.
	for _, kept := range []string{"get_api_spec", "product_extra_tool"} {
		if !slices.Contains(gatedNames, kept) {
			t.Fatalf("%q must survive the profile filter, got %v", kept, gatedNames)
		}
	}
}

func TestMuseIntegrationConfiguresBestEffortNativePolicy(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
	agent.additionalBridgeTools = []string{"product_extra_tool", "read_image"}
	if err := agent.registerDirectTool("product_extra_tool", "extra", map[string]interface{}{"type": "object"},
		func(context.Context, map[string]interface{}) (string, error) { return "", nil }, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if err := agent.registerDirectTool("read_image", "platform image analysis", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filepath": map[string]interface{}{"type": "string"},
			"query":    map[string]interface{}{"type": "string"},
		},
	}, func(context.Context, map[string]interface{}) (string, error) { return "", nil }, 0, "workspace_advanced"); err != nil {
		t.Fatal(err)
	}
	opts, err := agent.appendMuseCLIIntegrationOptions(nil)
	if err != nil {
		t.Fatalf("append Muse options: %v", err)
	}
	raw, ok := metadataFromCallOptions(opts)[musecli.MetadataKeyMuseToolAllowlist]
	if !ok {
		t.Fatal("Muse tool allowlist option missing")
	}
	got, ok := raw.([]string)
	if !ok {
		t.Fatalf("Muse tool allowlist has type %T", raw)
	}
	for _, want := range []string{"web_search"} {
		if !slices.Contains(got, want) {
			t.Fatalf("Muse allowlist missing %q: %v", want, got)
		}
	}
	for _, forbidden := range []string{"bash", "write_file", "read_image", "request_user_input", "subagent_spawn", "cron_create", "mcp__api_bridge__execute_shell_command"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("Muse native tool %q must not be allowed: %v", forbidden, got)
		}
	}
	mcpConfig, ok := metadataFromCallOptions(opts)[musecli.MetadataKeyMuseMCPConfig].(string)
	if !ok {
		t.Fatal("Muse MCP config option missing")
	}
	if _, ok := bridgeToolsFromConfig(t, mcpConfig)["read_image"]; !ok {
		t.Fatalf("Muse MCP bridge did not expose platform read_image: %s", mcpConfig)
	}
}

func TestBuildBridgeMCPConfigStaticURLWithSessionHeader(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
	agent.sessionID = "sess-abc-123"
	workingDir := t.TempDir()
	agent.codingAgentWorkingDir = workingDir

	configJSON, err := agent.buildBridgeMCPConfig()
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
	wantToolOutputDir := filepath.Join(workingDir, DefaultToolOutputFolder)
	if got := env["MCP_TOOL_OUTPUT_DIR"].(string); got != wantToolOutputDir {
		t.Fatalf("MCP_TOOL_OUTPUT_DIR = %q", got)
	}
	if info, statErr := os.Stat(wantToolOutputDir); statErr != nil || !info.IsDir() {
		t.Fatalf("MCP tool output directory was not created during bridge setup: info=%v err=%v", info, statErr)
	}
	if bridge["command"].(string) != "/usr/local/bin/mcpbridge" {
		t.Fatalf("command mismatch")
	}
	if bridge["trust"] != true {
		t.Fatal("trust should be true")
	}
}

// PLAT-186. pi-mcp-adapter (the third-party MCP extension pi-cli loads)
// gates native "directTools" registration on a hash of the MCP server's
// entire declared config, env included -- confirmed by reading its own
// source (pi-mcp-adapter@2.27.0 metadata-cache.ts computeServerHash). If
// MCP_READY_FILE differs between two launches for the SAME agent/working
// directory identity, that hash never matches twice, and pi-cli is forced
// through the fragile double-JSON-encoded "mcp" proxy wrapper on every
// call regardless of directTools being configured correctly -- exactly
// what let a live incident happen (a model malformed that encoding and
// failed a whole run). Two consecutive builds for the same identity must
// produce byte-identical env, MCP_READY_FILE included, so pi-mcp-adapter's
// cache can actually stay valid across repeated launches.
//
// Fails before the fix (MCP_READY_FILE was a fresh os.CreateTemp path on
// every call, unconditionally, with no exceptions); passes after.
func TestBuildBridgeMCPConfigReadyFileIsStableAcrossRepeatedCallsForTheSameIdentity(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
	agent.sessionID = "sess-abc-123"
	agent.codingAgentWorkingDir = t.TempDir()

	readyFileFromConfig := func() string {
		configJSON, err := agent.buildBridgeMCPConfig()
		if err != nil {
			t.Fatalf("buildBridgeMCPConfig() error: %v", err)
		}
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		servers := config["mcpServers"].(map[string]interface{})
		bridge := servers["api-bridge"].(map[string]interface{})
		env := bridge["env"].(map[string]interface{})
		readyFile, _ := env["MCP_READY_FILE"].(string)
		if readyFile == "" {
			t.Fatal("MCP_READY_FILE missing from bridge env")
		}
		return readyFile
	}

	first := readyFileFromConfig()
	second := readyFileFromConfig()
	if first != second {
		t.Fatalf("MCP_READY_FILE changed across repeated calls for the same agent identity: %q != %q -- this alone defeats pi-mcp-adapter's cache-validity hash on every launch, regardless of any other config being correct", first, second)
	}
	if !strings.HasPrefix(first, agent.codingAgentWorkingDir) {
		t.Fatalf("MCP_READY_FILE = %q, want it anchored under the stable working directory %q, not a random temp path", first, agent.codingAgentWorkingDir)
	}
}

// A fresh agent with no stable working directory has no cache-validity
// benefit to protect (a brand-new temp dir never had a prior cache entry
// either), so it must keep the original random-path fallback rather than
// collide on some fixed name.
func TestBuildBridgeMCPConfigReadyFileFallsBackToRandomPathWithoutAWorkingDir(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
	agent.codingAgentWorkingDir = ""

	configJSON, err := agent.buildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("buildBridgeMCPConfig() error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	servers := config["mcpServers"].(map[string]interface{})
	bridge := servers["api-bridge"].(map[string]interface{})
	env := bridge["env"].(map[string]interface{})
	readyFile, _ := env["MCP_READY_FILE"].(string)
	if readyFile == "" {
		t.Fatal("MCP_READY_FILE missing from bridge env")
	}
	if !strings.Contains(readyFile, "mcpbridge-ready-") {
		t.Fatalf("MCP_READY_FILE = %q, want the random-path fallback pattern when there is no working directory to anchor to", readyFile)
	}
}

// PLAT-186 follow-up (post-implementation review, 2026-08-23). Making the
// ready-file path stable per working directory made it unsafe when two
// sessions with the same MCP config run concurrently in one working
// directory -- a case pi-cli deliberately allows
// (acquirePiWorkspaceMCPConfigLease, multi-llm-provider-go). Both launches'
// os.Remove/write/read would race on the same shared path, and
// WaitForMCPReadyFile's plain existence check is satisfied by whichever
// bridge writes first, not necessarily the caller's own -- reintroducing
// the exact cold-turn tool-unavailable race the marker exists to prevent,
// specifically for concurrent launches.
//
// Fails before the fix (both calls always returned the identical stable
// path with no concurrency awareness at all); passes after.
func TestBuildBridgeMCPConfigReadyFileFallsBackToPrivatePathForConcurrentLaunchesInSameWorkingDir(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	workingDir := t.TempDir()
	readyFileFor := func(sessionID string) string {
		agent := bridgeTestAgent()
		agent.codingAgentWorkingDir = workingDir
		agent.sessionID = sessionID
		configJSON, err := agent.buildBridgeMCPConfig()
		if err != nil {
			t.Fatalf("buildBridgeMCPConfig() error: %v", err)
		}
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		servers := config["mcpServers"].(map[string]interface{})
		bridge := servers["api-bridge"].(map[string]interface{})
		env := bridge["env"].(map[string]interface{})
		readyFile, _ := env["MCP_READY_FILE"].(string)
		if readyFile == "" {
			t.Fatal("MCP_READY_FILE missing from bridge env")
		}
		return readyFile
	}

	stablePath := filepath.Join(workingDir, ".mcpbridge-ready.marker")

	// First launch for this working directory (session A): no prior
	// acquisition on record, so it gets the stable, cache-friendly path.
	first := readyFileFor("session-A")
	if first != stablePath {
		t.Fatalf("first launch MCP_READY_FILE = %q, want the stable path %q", first, stablePath)
	}

	// Session A itself repeating in the same working directory, immediately
	// after -- NOT concurrent with itself, so it must keep getting the
	// stable path back. This is the case the guard must never break: it is
	// the entire mechanism PLAT-186's fix depends on.
	firstAgain := readyFileFor("session-A")
	if firstAgain != stablePath {
		t.Fatalf("session A's own repeated launch MCP_READY_FILE = %q, want the stable path %q -- the concurrency guard must never defeat same-session reuse", firstAgain, stablePath)
	}

	// A DIFFERENT session (session B) for the SAME working directory,
	// immediately after -- simulating a genuinely concurrent session
	// (pi-cli allows this for matching configs). It must NOT reuse the
	// path session A is plausibly still waiting on.
	second := readyFileFor("session-B")
	if second == stablePath {
		t.Fatal("session B reused the stable path while session A's readiness wait may still be in flight -- this is exactly the race the concurrency guard exists to prevent")
	}
	if second == first {
		t.Fatalf("session B's private path (%q) collided with session A's path", second)
	}

	// Once the concurrency window has genuinely elapsed, a later launch from
	// yet another session is safe to reuse the stable path again --
	// simulated directly rather than sleeping in a unit test.
	piReadyMarkerLastAcquired.Store(workingDir, piReadyMarkerAcquisition{
		sessionID:  "session-A",
		acquiredAt: time.Now().Add(-2 * piReadyMarkerConcurrencyWindow),
	})
	third := readyFileFor("session-C")
	if third != stablePath {
		t.Fatalf("session C's launch (after the concurrency window elapsed) MCP_READY_FILE = %q, want the stable path %q back", third, stablePath)
	}
}

func TestBuildBridgeMCPConfigFailsWhenToolOutputDirectoryCannotBeCreated(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	workingDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(workingDir, []byte("file"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	agent := bridgeTestAgent()
	agent.codingAgentWorkingDir = workingDir

	_, err := agent.buildBridgeMCPConfig()
	if err == nil || !strings.Contains(err.Error(), "create MCP tool output directory") {
		t.Fatalf("buildBridgeMCPConfig error = %v, want tool output directory creation failure", err)
	}
}

func TestAttachedSkillAddsReadSkillToCodingAgentMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	if err := agent.attachSkill(&llmtypes.Skill{
		Name:    "builder-reference",
		Content: "transport-neutral instructions",
		SupportingFiles: []llmtypes.SkillFile{
			{RelPath: "references/example.md", Content: []byte("example")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	configJSON, err := agent.buildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("build bridge config: %v", err)
	}
	tools := bridgeToolsFromConfig(t, configJSON)
	readSkill, ok := tools[readSkillToolName]
	if !ok {
		t.Fatalf("attached skill did not add %s to MCP bridge; tools=%v", readSkillToolName, mapKeys(tools))
	}
	if readSkill.Type != "custom" {
		t.Fatalf("read_skill bridge type = %q, want custom", readSkill.Type)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(readSkill.InputSchema, &schema); err != nil {
		t.Fatalf("decode read_skill schema: %v", err)
	}
	properties, _ := schema["properties"].(map[string]interface{})
	if len(properties) != 1 || properties["skills"] == nil {
		t.Fatalf("read_skill bridge schema is incomplete: %s", readSkill.InputSchema)
	}
	skillsSchema, _ := properties["skills"].(map[string]interface{})
	if skillsSchema["maxItems"] != float64(maxReadSkillBatchSize) {
		t.Fatalf("read_skill batch maxItems = %#v, want %d", skillsSchema["maxItems"], maxReadSkillBatchSize)
	}
	items, _ := skillsSchema["items"].(map[string]interface{})
	itemProperties, _ := items["properties"].(map[string]interface{})
	if itemProperties["name"] == nil || itemProperties["path"] == nil {
		t.Fatalf("read_skill batch item schema is incomplete: %s", readSkill.InputSchema)
	}
}

func TestBuildBridgeMCPConfigNormalizesMarkdownAPIURL(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "[http://127.0.0.1:45678](http://127.0.0.1:45678)")
	t.Setenv("MCP_API_TOKEN", "test-token-123")

	agent := bridgeTestAgent()
	configJSON, err := agent.buildBridgeMCPConfig()
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
	_, err := agent.buildBridgeMCPConfig()
	if err == nil || !strings.Contains(err.Error(), "invalid MCP bridge API URL") {
		t.Fatalf("BuildBridgeMCPConfig() error = %v, want invalid URL error", err)
	}
}

func TestBuildBridgeMCPConfigNoSessionID(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	configJSON, err := agent.buildBridgeMCPConfig()
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
	agent.sessionID = "s1"
	configJSON, err := agent.buildBridgeMCPConfig()
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
	_, err := agent.buildBridgeMCPConfig()
	if err == nil {
		t.Fatal("expected error when API URL not configured")
	}
}

func TestBuildBridgeMCPConfigMissingToken(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	os.Unsetenv("MCP_API_TOKEN")

	agent := bridgeTestAgent()
	_, err := agent.buildBridgeMCPConfig()
	if err == nil {
		t.Fatal("expected error when API token not configured")
	}
}

func TestBuildBridgeMCPConfigAPIBaseURLPriority(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://env-url:9090")
	t.Setenv("MCP_API_TOKEN", "env-token")

	agent := bridgeTestAgent()
	agent.apiBaseURL = "http://agent-url:7070"
	agent.apiToken = "agent-token"
	configJSON, err := agent.buildBridgeMCPConfig()
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

func TestAppendCursorCLIIntegrationOptionsEnablesBridgeAndDenyHooks(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.sessionID = "app-session"
	agent.enableStreaming = true

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
	if got[cursorcli.MetadataKeyStreamTranscript] != true {
		t.Fatalf("Cursor transcript streaming metadata = %#v, want true", got[cursorcli.MetadataKeyStreamTranscript])
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
	agent.modelID = "cursor-cli"
	agent.sessionID = "app-session"
	agent.cursorPersistentInteractiveSession = true
	agent.cursorBridgeToolsMode = true

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

func TestAppendCodexCLIIntegrationOptionsEnablesMCPBridge(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")
	t.Setenv("CODING_AGENT_MCP_TOOL_TIMEOUT", "17m")

	agent := bridgeTestAgent()
	agent.enableStreaming = true
	opts, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
	if err != nil {
		t.Fatalf("appendCodexCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)
	mcpServersJSON, ok := got[codexcli.MetadataKeyMCPServers].(string)
	if !ok {
		t.Fatalf("Codex MCP servers = %#v, want JSON string", got[codexcli.MetadataKeyMCPServers])
	}
	for _, want := range []string{"\"api-bridge\"", "\"MCP_API_URL\"", "\"MCP_API_TOKEN\"", "\"tool_timeout_sec\":1020", "\"default_tools_approval_mode\":\"approve\""} {
		if !strings.Contains(mcpServersJSON, want) {
			t.Fatalf("Codex MCP server JSON missing %q:\n%s", want, mcpServersJSON)
		}
	}
	if overrides, ok := got[codexcli.MetadataKeyConfigOverrides].([]string); ok && strings.Contains(strings.Join(overrides, "\n"), "MCP_API_TOKEN") {
		t.Fatalf("Codex config overrides leaked bridge credentials: %#v", overrides)
	}
	if got[codexcli.MetadataKeyStreamTranscript] != true {
		t.Fatalf("Codex transcript streaming metadata = %#v, want true", got[codexcli.MetadataKeyStreamTranscript])
	}
}

func TestCodingCLIStreamingKeepsTranscriptAndTerminalSnapshots(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	tests := []struct {
		name            string
		metadataKey     string
		tmuxMetadataKey string
		append          func(*Agent) ([]llmtypes.CallOption, error)
	}{
		{
			name:            "claude",
			metadataKey:     claudecode.MetadataKeyStreamTranscript,
			tmuxMetadataKey: claudecode.MetadataKeyStreamTmuxScreen,
			append: func(agent *Agent) ([]llmtypes.CallOption, error) {
				return agent.appendClaudeCodeIntegrationOptions(nil, LLMModel{})
			},
		},
		{
			name:            "codex",
			metadataKey:     codexcli.MetadataKeyStreamTranscript,
			tmuxMetadataKey: codexcli.MetadataKeyStreamTmuxScreen,
			append: func(agent *Agent) ([]llmtypes.CallOption, error) {
				return agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
			},
		},
		{
			name:            "cursor",
			metadataKey:     cursorcli.MetadataKeyStreamTranscript,
			tmuxMetadataKey: cursorcli.MetadataKeyStreamTmuxScreen,
			append: func(agent *Agent) ([]llmtypes.CallOption, error) {
				return agent.appendCursorCLIIntegrationOptions(nil)
			},
		},
		{
			name:            "muse",
			metadataKey:     musecli.MetadataKeyMuseStreamTranscript,
			tmuxMetadataKey: musecli.MetadataKeyMuseStreamTmuxScreen,
			append: func(agent *Agent) ([]llmtypes.CallOption, error) {
				return agent.appendMuseCLIIntegrationOptions(nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nonStreamingAgent := bridgeTestAgent()
			opts, err := tt.append(nonStreamingAgent)
			if err != nil {
				t.Fatalf("append non-streaming options: %v", err)
			}
			if got := metadataFromCallOptions(opts)[tt.metadataKey]; got != nil {
				t.Fatalf("non-streaming agent must not enable transcript tailing, got %#v", got)
			}

			tmuxAgent := bridgeTestAgent()
			tmuxAgent.enableStreaming = true
			opts, err = tt.append(tmuxAgent)
			if err != nil {
				t.Fatalf("append tmux options: %v", err)
			}
			if got := metadataFromCallOptions(opts)[tt.metadataKey]; got != true {
				t.Fatalf("streaming transcript metadata = %#v, want true", got)
			}
			if got := metadataFromCallOptions(opts)[tt.tmuxMetadataKey]; got != true {
				t.Fatalf("streaming tmux-screen metadata = %#v, want true", got)
			}

			callbackAgent := bridgeTestAgent()
			callbackAgent.streamingCallback = func(llmtypes.StreamChunk) {}
			opts, err = tt.append(callbackAgent)
			if err != nil {
				t.Fatalf("append callback options: %v", err)
			}
			if got := metadataFromCallOptions(opts)[tt.metadataKey]; got != true {
				t.Fatalf("streaming callback transcript metadata = %#v, want true", got)
			}
			if got := metadataFromCallOptions(opts)[tt.tmuxMetadataKey]; got != true {
				t.Fatalf("streaming callback tmux-screen metadata = %#v, want true", got)
			}

			structuredAgent := bridgeTestAgent()
			structuredAgent.enableStreaming = true
			structuredAgent.codingAgentTransport = llm.CodingAgentTransportStructured
			opts, err = tt.append(structuredAgent)
			if err != nil {
				t.Fatalf("append structured options: %v", err)
			}
			if got := metadataFromCallOptions(opts)[tt.metadataKey]; got != nil {
				t.Fatalf("structured transport must not enable transcript tailing, got %#v", got)
			}
		})
	}
}

// TestAppendCodexCLIIntegrationOptionsSandboxDefault pins the default posture:
// no CodexSandboxMode set -> "workspace-write" (native writes; network still
// off unless CodexNetworkAccess is also set) — matching how codex ran for most
// of this project's life, and the right default for the common case (an
// interactive caller, or one where the bridge already grants shell access
// anyway, so blocking codex's native writes stops nothing real). See the
// CodexSandboxMode field doc on Agent.
func TestAppendCodexCLIIntegrationOptionsSandboxDefault(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	opts, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
	if err != nil {
		t.Fatalf("appendCodexCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)
	if sandbox, _ := got[codexcli.MetadataKeySandbox].(string); sandbox != "workspace-write" {
		t.Fatalf("default sandbox = %q, want %q", sandbox, "workspace-write")
	}
	if _, ok := got[codexcli.MetadataKeyConfigOverrides]; ok {
		t.Fatalf("default sandbox must not set network-access config overrides unless CodexNetworkAccess is also set: %#v", got[codexcli.MetadataKeyConfigOverrides])
	}
}

func TestHybridCodingProviderAutoOptions(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	t.Run("Claude Code", func(t *testing.T) {
		agent := bridgeTestAgent()
		agent.codingAgentToolsMode = codingAgentToolsHybrid
		opts, err := agent.appendClaudeCodeIntegrationOptions(nil, LLMModel{})
		if err != nil {
			t.Fatal(err)
		}
		got := metadataFromCallOptions(opts)
		if got[claudecode.MetadataKeyTools] != "default" {
			t.Fatalf("tools = %#v, want default", got[claudecode.MetadataKeyTools])
		}
		if got["claude_code_permission_mode"] != "auto" {
			t.Fatalf("permission mode = %#v, want auto", got["claude_code_permission_mode"])
		}
		if _, dangerous := got[claudecode.MetadataKeyDangerouslySkipPermissions]; dangerous {
			t.Fatalf("provider_auto must not skip Claude permissions: %#v", got)
		}
	})

	t.Run("Cursor", func(t *testing.T) {
		agent := bridgeTestAgent()
		agent.codingAgentToolsMode = codingAgentToolsHybrid
		opts, err := agent.appendCursorCLIIntegrationOptions(nil)
		if err != nil {
			t.Fatal(err)
		}
		got := metadataFromCallOptions(opts)
		if got["cursor_auto_review"] != true {
			t.Fatalf("auto review = %#v, want true", got["cursor_auto_review"])
		}
		if got[cursorcli.MetadataKeyDenyBuiltinTools] != nil || got[cursorcli.MetadataKeyForce] != nil {
			t.Fatalf("hybrid provider_auto must not deny builtins or force Cursor: %#v", got)
		}
	})

	t.Run("Codex", func(t *testing.T) {
		agent := bridgeTestAgent()
		agent.codingAgentToolsMode = codingAgentToolsHybrid
		opts, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
		if err != nil {
			t.Fatal(err)
		}
		got := metadataFromCallOptions(opts)
		if _, disabled := got[codexcli.MetadataKeyDisableShellTool]; disabled {
			t.Fatalf("hybrid must retain Codex native shell: %#v", got)
		}
		if got[codexcli.MetadataKeyApprovalPolicy] != "untrusted" {
			t.Fatalf("approval policy = %#v, want untrusted", got[codexcli.MetadataKeyApprovalPolicy])
		}
		overrides, _ := got[codexcli.MetadataKeyConfigOverrides].([]string)
		if !strings.Contains(strings.Join(overrides, "\n"), `approvals_reviewer="auto_review"`) {
			t.Fatalf("config overrides = %#v, want auto reviewer", overrides)
		}
	})
}

func TestHybridCodingApproveAllOptions(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	t.Run("Claude Code", func(t *testing.T) {
		agent := bridgeTestAgent()
		agent.codingAgentToolsMode = codingAgentToolsHybrid
		agent.codingAgentApprovalsMode = codingAgentApprovalsAll
		opts, err := agent.appendClaudeCodeIntegrationOptions(nil, LLMModel{})
		if err != nil {
			t.Fatal(err)
		}
		got := metadataFromCallOptions(opts)
		if got[claudecode.MetadataKeyDangerouslySkipPermissions] != true {
			t.Fatalf("approve_all must bypass Claude approvals: %#v", got)
		}
	})

	t.Run("Cursor", func(t *testing.T) {
		agent := bridgeTestAgent()
		agent.codingAgentToolsMode = codingAgentToolsHybrid
		agent.codingAgentApprovalsMode = codingAgentApprovalsAll
		opts, err := agent.appendCursorCLIIntegrationOptions(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := metadataFromCallOptions(opts)[cursorcli.MetadataKeyForce]; got != true {
			t.Fatalf("approve_all Cursor force = %#v, want true", got)
		}
	})

	t.Run("Codex", func(t *testing.T) {
		agent := bridgeTestAgent()
		agent.codingAgentToolsMode = codingAgentToolsHybrid
		agent.codingAgentApprovalsMode = codingAgentApprovalsAll
		opts, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
		if err != nil {
			t.Fatal(err)
		}
		got := metadataFromCallOptions(opts)
		if got[codexcli.MetadataKeyApprovalPolicy] != "never" {
			t.Fatalf("approve_all Codex policy = %#v, want never", got)
		}
		if _, reviewer := got[codexcli.MetadataKeyConfigOverrides]; reviewer {
			t.Fatalf("approve_all must not configure Codex auto-review: %#v", got)
		}
	})
}

// TestAppendCodexCLIIntegrationOptionsSandboxNetworkAccess proves a caller that
// also wants native network under the default workspace-write sandbox can opt
// in via withCodexNetworkAccess, without needing to also set withCodexSandbox.
func TestAppendCodexCLIIntegrationOptionsSandboxNetworkAccess(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.codexNetworkAccess = true
	opts, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
	if err != nil {
		t.Fatalf("appendCodexCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)
	if sandbox, _ := got[codexcli.MetadataKeySandbox].(string); sandbox != "workspace-write" {
		t.Fatalf("sandbox = %q, want %q", sandbox, "workspace-write")
	}
	overrides, ok := got[codexcli.MetadataKeyConfigOverrides].([]string)
	if !ok || !strings.Contains(strings.Join(overrides, "\n"), "sandbox_workspace_write.network_access=true") {
		t.Fatalf("config overrides = %#v, want sandbox_workspace_write.network_access=true", overrides)
	}
}

// TestAppendCodexCLIIntegrationOptionsSandboxReadOnlyOptIn proves the narrow
// containment case still works: a caller that deliberately restricts its tool
// set (e.g. "web_search only, no shell on the bridge") or needs an audit-trail
// guarantee can opt INTO "read-only" via withCodexSandbox. This is no longer
// the default, but it must remain available and correctly wired.
func TestAppendCodexCLIIntegrationOptionsSandboxReadOnlyOptIn(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.codexSandboxMode = "read-only"
	// CodexNetworkAccess is meaningless under read-only (network is
	// unconditionally off there) — set it anyway to prove it's correctly
	// ignored rather than producing a bogus config override.
	agent.codexNetworkAccess = true
	opts, err := agent.appendCodexCLIIntegrationOptions(nil, LLMModel{})
	if err != nil {
		t.Fatalf("appendCodexCLIIntegrationOptions() error = %v", err)
	}
	got := metadataFromCallOptions(opts)
	if sandbox, _ := got[codexcli.MetadataKeySandbox].(string); sandbox != "read-only" {
		t.Fatalf("sandbox = %q, want %q", sandbox, "read-only")
	}
	if _, ok := got[codexcli.MetadataKeyConfigOverrides]; ok {
		t.Fatalf("read-only sandbox must not set network-access config overrides even with CodexNetworkAccess=true: %#v", got[codexcli.MetadataKeyConfigOverrides])
	}
}

// TestWithCodexSandboxAgentOption proves the agentOption wires into the field
// the appender reads.
func TestWithCodexSandboxAgentOption(t *testing.T) {
	a := &Agent{}
	withCodexSandbox("workspace-write")(a)
	if a.codexSandboxMode != "workspace-write" {
		t.Fatalf("CodexSandboxMode = %q, want %q", a.codexSandboxMode, "workspace-write")
	}
	withCodexNetworkAccess(true)(a)
	if !a.codexNetworkAccess {
		t.Fatal("CodexNetworkAccess = false, want true")
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

func TestAppendPiCLIIntegrationOptionsEnablesMCPBridgeOnlyTools(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")

	agent := bridgeTestAgent()
	agent.sessionID = "app-session"

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

// Explicit bridge configuration must beat the process environment: that is
// what lets two executors coexist in one process (a host application's own
// and one started by an embedded session) without either clobbering the
// other through MCP_* variables.
func TestBuildBridgeMCPConfigPrefersExplicitOverEnvironment(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/env/mcpbridge")
	t.Setenv("MCP_BRIDGE_API_URL", "http://env-bridge:1")
	t.Setenv("MCP_API_URL", "http://env-api:2")
	t.Setenv("MCP_API_TOKEN", "env-token")

	a := bridgeTestAgent()
	a.bridgeBinary = "/explicit/mcpbridge"
	a.bridgeAPIBaseURL = "http://127.0.0.1:43210"
	a.apiBaseURL = "http://127.0.0.1:43210"
	a.apiToken = "explicit-token"

	configJSON, err := a.buildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("buildBridgeMCPConfig() error: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	bridge := config["mcpServers"].(map[string]interface{})["api-bridge"].(map[string]interface{})
	if got := bridge["command"]; got != "/explicit/mcpbridge" {
		t.Fatalf("command = %v, want the explicit binary over MCP_BRIDGE_BINARY", got)
	}
	env := bridge["env"].(map[string]interface{})
	if got := env["MCP_API_URL"]; got != "http://127.0.0.1:43210" {
		t.Fatalf("MCP_API_URL = %v, want the explicit bridge URL over the environment", got)
	}
	if got := env["MCP_API_TOKEN"]; got != "explicit-token" {
		t.Fatalf("MCP_API_TOKEN = %v, want the explicit token", got)
	}

	// Without explicit values the environment still applies, unchanged.
	b := bridgeTestAgent()
	fallbackJSON, err := b.buildBridgeMCPConfig()
	if err != nil {
		t.Fatalf("fallback buildBridgeMCPConfig() error: %v", err)
	}
	var fallback map[string]interface{}
	_ = json.Unmarshal([]byte(fallbackJSON), &fallback)
	fb := fallback["mcpServers"].(map[string]interface{})["api-bridge"].(map[string]interface{})
	if fb["command"] != "/env/mcpbridge" || fb["env"].(map[string]interface{})["MCP_API_URL"] != "http://env-bridge:1" {
		t.Fatalf("environment fallback broken: %v", fb)
	}
}
