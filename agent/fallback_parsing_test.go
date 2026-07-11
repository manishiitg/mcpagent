package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

func TestParseFallbackModelRef(t *testing.T) {
	tests := []struct {
		name            string
		primaryProvider string
		fallbackRef     string
		wantProvider    string
		wantModelID     string
		wantOK          bool
	}{
		{
			name:            "same-provider model ID",
			primaryProvider: "anthropic",
			fallbackRef:     "claude-sonnet-4-6-20250514",
			wantProvider:    "anthropic",
			wantModelID:     "claude-sonnet-4-6-20250514",
			wantOK:          true,
		},
		{
			name:            "cross-provider with known provider prefix",
			primaryProvider: "anthropic",
			fallbackRef:     "openai/gpt-5-mini",
			wantProvider:    "openai",
			wantModelID:     "gpt-5-mini",
			wantOK:          true,
		},
		{
			name:            "slash in model ID with unknown prefix stays same-provider",
			primaryProvider: "openai",
			fallbackRef:     "x-ai/grok-code-fast-1",
			wantProvider:    "openai",
			wantModelID:     "x-ai/grok-code-fast-1",
			wantOK:          true,
		},
		{
			name:            "empty ref returns false",
			primaryProvider: "anthropic",
			fallbackRef:     "",
			wantOK:          false,
		},
		{
			name:            "whitespace-only ref returns false",
			primaryProvider: "anthropic",
			fallbackRef:     "   ",
			wantOK:          false,
		},
		{
			name:            "leading slash treated as same-provider",
			primaryProvider: "openai",
			fallbackRef:     "/gpt-5",
			wantProvider:    "openai",
			wantModelID:     "/gpt-5",
			wantOK:          true,
		},
		{
			name:            "trailing slash treated as same-provider",
			primaryProvider: "anthropic",
			fallbackRef:     "anthropic/",
			wantProvider:    "anthropic",
			wantModelID:     "anthropic/",
			wantOK:          true,
		},
		{
			name:            "whitespace around ref is trimmed",
			primaryProvider: "anthropic",
			fallbackRef:     "  openai/gpt-5-mini  ",
			wantProvider:    "openai",
			wantModelID:     "gpt-5-mini",
			wantOK:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseFallbackModelRef(tt.primaryProvider, tt.fallbackRef)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Provider != tt.wantProvider {
				t.Fatalf("Provider = %q, want %q", got.Provider, tt.wantProvider)
			}
			if got.ModelID != tt.wantModelID {
				t.Fatalf("ModelID = %q, want %q", got.ModelID, tt.wantModelID)
			}
		})
	}
}

func TestDedupeFallbacks(t *testing.T) {
	tests := []struct {
		name      string
		input     []LLMModel
		wantCount int
		wantKeys  []string
	}{
		{
			name: "removes exact duplicates",
			input: []LLMModel{
				{Provider: "openai", ModelID: "gpt-5-mini"},
				{Provider: "openai", ModelID: "gpt-5-mini"},
				{Provider: "anthropic", ModelID: "claude-sonnet-4-6"},
			},
			wantCount: 2,
			wantKeys:  []string{"openai/gpt-5-mini", "anthropic/claude-sonnet-4-6"},
		},
		{
			name: "preserves order of first occurrence",
			input: []LLMModel{
				{Provider: "anthropic", ModelID: "claude-opus-4-7"},
				{Provider: "openai", ModelID: "gpt-5"},
				{Provider: "anthropic", ModelID: "claude-opus-4-7"},
			},
			wantCount: 2,
			wantKeys:  []string{"anthropic/claude-opus-4-7", "openai/gpt-5"},
		},
		{
			name: "strips whitespace before deduping",
			input: []LLMModel{
				{Provider: " openai ", ModelID: " gpt-5 "},
				{Provider: "openai", ModelID: "gpt-5"},
			},
			wantCount: 1,
			wantKeys:  []string{"openai/gpt-5"},
		},
		{
			name: "drops entries with empty provider or model",
			input: []LLMModel{
				{Provider: "", ModelID: "gpt-5"},
				{Provider: "openai", ModelID: ""},
				{Provider: "openai", ModelID: "gpt-5"},
			},
			wantCount: 1,
			wantKeys:  []string{"openai/gpt-5"},
		},
		{
			name:      "empty input returns empty",
			input:     nil,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupeFallbacks(tt.input)
			if len(got) != tt.wantCount {
				t.Fatalf("count = %d, want %d: %v", len(got), tt.wantCount, got)
			}
			for i, wantKey := range tt.wantKeys {
				gotKey := got[i].Provider + "/" + got[i].ModelID
				if gotKey != wantKey {
					t.Fatalf("result[%d] = %q, want %q", i, gotKey, wantKey)
				}
			}
		})
	}
}

func TestGetEffectiveLLMConfigLegacyFieldPromotion(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderAnthropic,
		ModelID:  "claude-opus-4-7-20250610",
	}

	config := agent.getEffectiveLLMConfig()

	if config.Primary.Provider != string(llm.ProviderAnthropic) {
		t.Fatalf("Primary.Provider = %q, want %q", config.Primary.Provider, llm.ProviderAnthropic)
	}
	if config.Primary.ModelID != "claude-opus-4-7-20250610" {
		t.Fatalf("Primary.ModelID = %q, want claude-opus-4-7-20250610", config.Primary.ModelID)
	}
}

func TestGetEffectiveLLMConfigNewConfigPassthrough(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderOpenAI,
		ModelID:  "gpt-5",
		LLMConfig: AgentLLMConfiguration{
			Primary: LLMModel{Provider: "openai", ModelID: "gpt-5"},
			Fallbacks: []LLMModel{
				{Provider: "anthropic", ModelID: "claude-sonnet-4-6"},
			},
		},
	}

	config := agent.getEffectiveLLMConfig()

	if config.Primary.Provider != "openai" {
		t.Fatalf("Primary.Provider = %q, want openai", config.Primary.Provider)
	}
	if len(config.Fallbacks) < 1 {
		t.Fatal("expected at least 1 explicit fallback to be preserved")
	}
	if config.Fallbacks[0].Provider != "anthropic" || config.Fallbacks[0].ModelID != "claude-sonnet-4-6" {
		t.Fatalf("Fallbacks[0] = %s/%s, want anthropic/claude-sonnet-4-6", config.Fallbacks[0].Provider, config.Fallbacks[0].ModelID)
	}
}

func TestGetEffectiveLLMConfigDeduplicatesFallbacks(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderOpenAI,
		ModelID:  "gpt-5",
		LLMConfig: AgentLLMConfiguration{
			Primary: LLMModel{Provider: "openai", ModelID: "gpt-5"},
			Fallbacks: []LLMModel{
				{Provider: "anthropic", ModelID: "claude-sonnet-4-6"},
				{Provider: "anthropic", ModelID: "claude-sonnet-4-6"},
			},
		},
	}

	config := agent.getEffectiveLLMConfig()

	anthropicCount := 0
	for _, fb := range config.Fallbacks {
		if fb.Provider == "anthropic" && fb.ModelID == "claude-sonnet-4-6" {
			anthropicCount++
		}
	}
	if anthropicCount != 1 {
		t.Fatalf("duplicate fallback not removed: anthropic/claude-sonnet-4-6 appears %d times", anthropicCount)
	}
}
