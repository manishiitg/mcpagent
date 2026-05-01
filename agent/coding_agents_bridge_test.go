package mcpagent

import (
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

func TestIsCodingCLIProviderIncludesKimiCodeOnly(t *testing.T) {
	t.Setenv("KIMI_CODE_TRANSPORT", "")

	tests := []struct {
		name     string
		provider llm.Provider
		modelID  string
		want     bool
	}{
		{name: "claude code", provider: llm.ProviderClaudeCode, want: true},
		{name: "gemini cli", provider: llm.ProviderGeminiCLI, want: true},
		{name: "codex cli", provider: llm.ProviderCodexCLI, want: true},
		{name: "kimi code", provider: llm.ProviderKimi, modelID: "kimi-code", want: true},
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
