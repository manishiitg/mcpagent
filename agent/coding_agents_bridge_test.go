package mcpagent

import (
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
