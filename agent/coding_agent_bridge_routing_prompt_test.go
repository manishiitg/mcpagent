package mcpagent

import (
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
		"Do not pipe through jq unless you explicitly preserve curl's nonzero status",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected compact MCP bridge rule %q in system prompt, got: %s", want, got)
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
	hybrid := &Agent{codingAgentToolsMode: codingAgentToolsHybrid}
	hybridPrompt := hybrid.codingAgentProviderRoutingPreamble()
	if !strings.Contains(hybridPrompt, "Provider-native tools are enabled") || strings.Contains(hybridPrompt, "tools are disabled") {
		t.Fatalf("hybrid routing preamble contradicts hybrid mode: %s", hybridPrompt)
	}

	mcpOnly := &Agent{codingAgentToolsMode: codingAgentToolsMCPOnly}
	mcpOnlyPrompt := mcpOnly.codingAgentProviderRoutingPreamble()
	if !strings.Contains(mcpOnlyPrompt, "Provider-native filesystem, shell, edit, and browser tools are disabled") {
		t.Fatalf("mcp-only routing preamble does not describe disabled native tools: %s", mcpOnlyPrompt)
	}
}

func TestCodingAgentProviderRoutingPromptDoesNotNameExcludedBridgeTools(t *testing.T) {
	a := &Agent{
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
