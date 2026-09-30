package mcpagent

import (
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"strings"
	"testing"
)

const testDefaultPreamble = "DEFAULT PREAMBLE FOR TEST"

func TestAppendBridgeRoutingInstructionsDefaultUnchanged(t *testing.T) {
	a := &Agent{}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if !strings.Contains(got, testDefaultPreamble) {
		t.Fatalf("expected default preamble in system prompt, got: %s", got)
	}
	if !strings.Contains(got, "IMPORTANT — bridge tool routing") {
		t.Fatalf("expected default bridgeRoutingExplicitInstructions text in system prompt, got: %s", got)
	}
	for _, want := range []string{
		`curl --fail-with-body -sS --json '<payload>' -H "$MCP_AUTH" "$MCP_CUSTOM/<tool>"`,
		"MCP_AUTH is already the complete `Authorization: Bearer ...` header",
		"--json already selects POST and Content-Type",
		"Keep curl unpiped to preserve its nonzero HTTP-failure status",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected compact MCP bridge rule %q in system prompt, got: %s", want, got)
		}
	}
}

func TestAppendBridgeRoutingInstructionsNamesDirectPlatformReadImage(t *testing.T) {
	a := &Agent{additionalBridgeTools: []string{"read_image"}}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	for _, want := range []string{
		"read_image",
		"platform image analysis",
		"Only declared runtime tools are direct calls",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("direct read_image routing prompt missing %q: %s", want, got)
		}
	}
}

// Native MCP declares typed bridge tools and has no generic mcp() proxy.
// Custom HTTP tools must continue to route through the bridge shell and curl.
func TestAppendBridgeRoutingInstructionsNoLongerTeachesMcpWrapperSyntax(t *testing.T) {
	a := &Agent{}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if strings.Contains(got, "mcp({") {
		t.Fatalf("expected no mcp() wrapper syntax taught in system prompt (native MCP has no proxy), got: %s", got)
	}
	for _, want := range []string{
		"Call HTTP tools through execute_shell_command",
		"never a direct tool call by their bare name",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected custom-tools-via-curl-only guardrail %q in system prompt, got: %s", want, got)
		}
	}
}

func TestAppendBridgeRoutingInstructionsCustomOverride(t *testing.T) {
	custom := "MY CUSTOM ROUTING TEXT"
	a := &Agent{bridgeRoutingInstructionsOverride: &custom}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if !strings.Contains(got, custom) {
		t.Fatalf("expected custom override text in system prompt, got: %s", got)
	}
	if strings.Contains(got, testDefaultPreamble) {
		t.Fatalf("default preamble should NOT appear when overridden, got: %s", got)
	}
	if strings.Contains(got, "IMPORTANT — bridge tool routing") {
		t.Fatalf("default bridgeRoutingExplicitInstructions text should NOT appear when overridden, got: %s", got)
	}
}

func TestAppendBridgeRoutingInstructionsEmptyOverrideSuppresses(t *testing.T) {
	empty := ""
	a := &Agent{bridgeRoutingInstructionsOverride: &empty}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.instructions()
	if got != "" {
		t.Fatalf("expected empty system prompt when override is \"\" (suppressed), got: %s", got)
	}
}

func TestWithBridgeRoutingInstructionsOptionSetsOverride(t *testing.T) {
	a := &Agent{}
	opt := withBridgeRoutingInstructions("custom text")
	opt(a)

	if a.bridgeRoutingInstructionsOverride == nil {
		t.Fatal("expected bridgeRoutingInstructionsOverride to be set")
	}
	if *a.bridgeRoutingInstructionsOverride != "custom text" {
		t.Fatalf("expected override value %q, got %q", "custom text", *a.bridgeRoutingInstructionsOverride)
	}
}

func TestCodingAgentProviderRoutingPreambleMatchesConfiguredToolMode(t *testing.T) {
	for _, provider := range []llmproviders.Provider{llmproviders.ProviderClaudeCode, llmproviders.ProviderMuseCLI, llmproviders.ProviderCodexCLI, llmproviders.ProviderCursorCLI} {
		hybrid := &Agent{codingAgentToolsMode: codingAgentToolsHybrid, provider: provider}
		hybridPrompt := hybrid.codingAgentProviderRoutingPreamble()
		if !strings.Contains(hybridPrompt, "native read-only tools") || !strings.Contains(hybridPrompt, "Native shell and file writes are disabled") {
			t.Fatalf("%s hybrid routing preamble must describe read-only native tools with shell/writes on the bridge: %s", provider, hybridPrompt)
		}
	}
	// Providers without a read-only hybrid restriction stay bridge-only.
	piHybrid := &Agent{codingAgentToolsMode: codingAgentToolsHybrid, provider: llmproviders.ProviderPiCLI}
	if got := piHybrid.codingAgentProviderRoutingPreamble(); !strings.Contains(got, "tools are disabled") {
		t.Fatalf("pi hybrid preamble must stay bridge-only: %s", got)
	}

	mcpOnly := &Agent{codingAgentToolsMode: codingAgentToolsMCPOnly}
	mcpOnlyPrompt := mcpOnly.codingAgentProviderRoutingPreamble()
	if !strings.Contains(mcpOnlyPrompt, "Provider-native filesystem, shell, edit, and browser tools are disabled") {
		t.Fatalf("mcp-only routing preamble does not describe disabled native tools: %s", mcpOnlyPrompt)
	}
}

func TestCodingAgentProviderRoutingPromptDoesNotNameExcludedBridgeTools(t *testing.T) {
	a := &Agent{
		provider:             llmproviders.ProviderClaudeCode,
		codingAgentToolsMode: codingAgentToolsHybrid,
		bridgeToolAdmit: func(name string) bool {
			return name != "execute_shell_command" && name != "diff_patch_workspace_file"
		},
	}
	a.appendBridgeRoutingInstructions(a.codingAgentProviderRoutingPreamble())
	got := a.instructions()
	for _, excluded := range []string{"execute_shell_command", "diff_patch_workspace_file"} {
		if strings.Contains(got, excluded) {
			t.Fatalf("routing prompt advertised excluded tool %q: %s", excluded, got)
		}
	}
	if !strings.Contains(got, "get_api_spec") {
		t.Fatalf("routing prompt lost the always-available discovery tool: %s", got)
	}
}
