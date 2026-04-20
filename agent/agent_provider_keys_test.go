package mcpagent

import (
	"context"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	zaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/zai"
)

type providerKeyCarrierModel struct {
	keys *llm.ProviderAPIKeys
}

func (m *providerKeyCarrierModel) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return nil, nil
}

func (m *providerKeyCarrierModel) GetModelID() string {
	return "test-model"
}

func (m *providerKeyCarrierModel) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	return nil, nil
}

func (m *providerKeyCarrierModel) GetAPIKeys() *llm.ProviderAPIKeys {
	return m.keys
}

func TestGetLLMModelConfigIncludesZAIAndMiniMaxKeys(t *testing.T) {
	zaiKey := "zai-key"
	minimaxKey := "minimax-key"

	tests := []struct {
		name     string
		provider llm.Provider
		keys     *llm.ProviderAPIKeys
		want     *string
	}{
		{
			name:     "z-ai",
			provider: llm.ProviderZAI,
			keys: &llm.ProviderAPIKeys{
				ZAI: &zaiKey,
			},
			want: &zaiKey,
		},
		{
			name:     "minimax",
			provider: llm.ProviderMiniMax,
			keys: &llm.ProviderAPIKeys{
				MiniMax: &minimaxKey,
			},
			want: &minimaxKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{
				provider: tt.provider,
				ModelID:  "test-model",
				APIKeys:  tt.keys,
			}

			config := agent.GetLLMModelConfig()
			if config.APIKey == nil || *config.APIKey != *tt.want {
				t.Fatalf("expected api key %q, got %#v", *tt.want, config.APIKey)
			}
		})
	}
}

func TestExtractAPIKeysFromLLMPreservesZAIAndCodexCLI(t *testing.T) {
	zaiKey := "zai-key"
	codexKey := "codex-key"

	model := &providerKeyCarrierModel{
		keys: &llm.ProviderAPIKeys{
			ZAI:      &zaiKey,
			CodexCLI: &codexKey,
		},
	}

	keys := extractAPIKeysFromLLM(model)
	if keys == nil {
		t.Fatal("expected extracted keys, got nil")
	}
	if keys.ZAI == nil || *keys.ZAI != zaiKey {
		t.Fatalf("expected ZAI key %q, got %#v", zaiKey, keys.ZAI)
	}
	if keys.CodexCLI == nil || *keys.CodexCLI != codexKey {
		t.Fatalf("expected Codex CLI key %q, got %#v", codexKey, keys.CodexCLI)
	}
}

func TestGetEffectiveLLMConfigAddsDefaultZAIFallback(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderZAI,
		ModelID:  zaiadapter.ModelGLM51,
	}

	config := agent.getEffectiveLLMConfig()
	if len(config.Fallbacks) != 1 {
		t.Fatalf("expected 1 fallback, got %d: %#v", len(config.Fallbacks), config.Fallbacks)
	}
	if config.Fallbacks[0].Provider != string(llm.ProviderZAI) {
		t.Fatalf("expected fallback provider %q, got %q", llm.ProviderZAI, config.Fallbacks[0].Provider)
	}
	if config.Fallbacks[0].ModelID != zaiadapter.ModelGLM47 {
		t.Fatalf("expected fallback model %q, got %q", zaiadapter.ModelGLM47, config.Fallbacks[0].ModelID)
	}
}
