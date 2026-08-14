package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type pricedEventTestModel struct{}

func (pricedEventTestModel) GenerateContent(context.Context, []llmtypes.MessageContent, ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return nil, nil
}

func (pricedEventTestModel) GetModelID() string { return "claude-opus-5" }

func (pricedEventTestModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{
		ModelID:                         "claude-opus-5",
		InputCostPer1MTokens:            5,
		OutputCostPer1MTokens:           25,
		CachedInputCostPer1MTokens:      0.5,
		CachedInputCostWritePer1MTokens: 6.25,
	}, nil
}

func TestLLMGenerationEndCarriesRuntimeCalculatedCost(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "pulse-cost-test",
		modelID:   "claude-opus-5",
		llmModel:  pricedEventTestModel{},
		logger:    loggerv2.NewNoop(),
		listeners: []AgentEventListener{listener},
	}
	prompt, completion, cacheRead := 1_273_577, 34_200, 1_273_527
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{
			PromptTokens:        &prompt,
			CompletionTokens:    &completion,
			CachedContentTokens: &cacheRead,
			Additional: map[string]interface{}{
				"claude_code_model":           "claude-opus-5",
				"cache_read_input_tokens":     cacheRead,
				"prompt_tokens_include_cache": true,
			},
		},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		CacheTokens:      cacheRead,
	}, resp)

	var completed *events.LLMGenerationEndEvent
	for _, event := range listener.events {
		if value, ok := event.Data.(*events.LLMGenerationEndEvent); ok {
			completed = value
		}
	}
	if completed == nil {
		t.Fatal("LLMGenerationEnd event was not emitted")
	}

	// 50 fresh input tokens * $5/M + 34,200 output * $25/M +
	// 1,273,527 cache-read tokens * $0.50/M.
	want := 0.00025 + 0.855 + 0.6367635
	got, ok := completed.Metadata["cost_usd_estimated"].(float64)
	if !ok || got != want {
		t.Fatalf("cost_usd_estimated = %#v, want %.7f", completed.Metadata["cost_usd_estimated"], want)
	}
	if gotModel := completed.Metadata["cost_model_id"]; gotModel != "claude-opus-5" {
		t.Fatalf("cost_model_id = %#v, want claude-opus-5", gotModel)
	}
}

func TestRuntimePricingDoesNotSubtractSeparateCacheTwice(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "codex-cost-test",
		modelID:   "claude-opus-5",
		llmModel:  pricedEventTestModel{},
		logger:    loggerv2.NewNoop(),
		listeners: []AgentEventListener{listener},
	}
	freshPrompt, completion, cacheRead := 50, 100, 1_000
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{
			PromptTokens:        &freshPrompt,
			CompletionTokens:    &completion,
			CachedContentTokens: &cacheRead,
			Additional: map[string]interface{}{
				"cache_read_input_tokens":     cacheRead,
				"prompt_tokens_include_cache": false,
			},
		},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens:     freshPrompt,
		CompletionTokens: completion,
		TotalTokens:      freshPrompt + completion,
		CacheTokens:      cacheRead,
	}, resp)

	var got float64
	for _, event := range listener.events {
		if completed, ok := event.Data.(*events.LLMGenerationEndEvent); ok {
			got, _ = completed.Metadata["cost_usd_estimated"].(float64)
		}
	}
	want := 0.00025 + 0.0025 + 0.0005
	if got != want {
		t.Fatalf("cost_usd_estimated = %.9f, want %.9f", got, want)
	}
}

func TestCumulativeUsageRetainsEstimatedProviderMarker(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "estimated-usage-test",
		modelID:   "cursor-auto",
		llmModel:  pricedEventTestModel{},
		logger:    loggerv2.NewNoop(),
		listeners: []AgentEventListener{listener},
	}
	prompt, completion := 10, 5
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{
			PromptTokens:     &prompt,
			CompletionTokens: &completion,
			Additional:       map[string]interface{}{"token_usage_estimated": true},
		},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion,
	}, resp)
	agent.emitTotalTokenUsageEvent(context.Background(), time.Second)

	for _, event := range listener.events {
		total, ok := event.Data.(*events.TokenUsageEvent)
		if !ok || total.Context != "conversation_total" {
			continue
		}
		if got, _ := total.GenerationInfo["token_usage_estimated"].(bool); !got {
			t.Fatalf("cumulative token_usage_estimated = %#v, want true", total.GenerationInfo["token_usage_estimated"])
		}
		return
	}
	t.Fatal("conversation_total token event was not emitted")
}

func TestCodingCLIAggregateUsageDoesNotPretendToBeContextPercentage(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID:          "coding-cli-context-test",
		modelID:            "gpt-5.6",
		modelContextWindow: 200_000,
		logger:             loggerv2.NewNoop(),
		listeners:          []AgentEventListener{listener},
	}
	prompt := 643_364
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{Additional: map[string]interface{}{
			"context_window_usage_known": false,
		}},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens: prompt,
		TotalTokens:  prompt,
	}, resp)

	if agent.contextWindowUsageKnown {
		t.Fatal("aggregate coding-CLI usage must not be treated as a context snapshot")
	}
	if agent.currentContextWindowUsage != 0 {
		t.Fatalf("currentContextWindowUsage = %d, want 0 when unknown", agent.currentContextWindowUsage)
	}
	for _, event := range listener.events {
		completed, ok := event.Data.(*events.LLMGenerationEndEvent)
		if !ok {
			continue
		}
		if got := completed.Metadata["context_usage_percent"]; got != float64(0) {
			t.Fatalf("context_usage_percent = %#v, want 0", got)
		}
		if _, found := completed.Metadata["model_context_window"]; found {
			t.Fatal("unknown context usage must not publish a model-context percentage denominator")
		}
		return
	}
	t.Fatal("LLMGenerationEnd event was not emitted")}
