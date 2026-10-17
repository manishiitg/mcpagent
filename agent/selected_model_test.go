package mcpagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

func TestLegacyFallbackConfigurationIsDiscarded(t *testing.T) {
	var config AgentLLMConfiguration
	if err := json.Unmarshal([]byte(`{"primary":{"provider":"codex-cli","model_id":"selected"},"fallbacks":[{"provider":"claude-code","model_id":"other"}]}`), &config); err != nil {
		t.Fatal(err)
	}
	a := &Agent{llmConfig: config}
	t.Setenv("CODEX_CLI_FALLBACK_MODELS", "other")
	t.Setenv("CODEX_CLI_CROSS_PROVIDER_FALLBACK_MODELS", "claude-code/other")
	effective := a.getEffectiveLLMConfig()
	encoded, err := json.Marshal(effective)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fallback") || effective.Primary.Provider != "codex-cli" || effective.Primary.ModelID != "selected" {
		t.Fatalf("legacy fallback survived or selection changed: %s", encoded)
	}
}

func TestQuotaExhaustionDoesNotSwitchProvider(t *testing.T) {
	t.Setenv("CODEX_CLI_FALLBACK_MODELS", "other")
	t.Setenv("CODEX_CLI_CROSS_PROVIDER_FALLBACK_MODELS", "claude-code/other")
	reset := time.Now().Add(time.Hour)
	a := &Agent{provider: llm.ProviderCodexCLI, modelID: "selected-quota-test", logger: loggerv2.NewNoop(), quotaExhaustedModels: map[quotaModelKey]time.Time{modelQuotaKey(LLMModel{Provider: "codex-cli", ModelID: "selected-quota-test"}): reset}}
	_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 1)
	if llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted || !llmerrors.RetryAtOrZero(err).Equal(reset) {
		t.Fatalf("quota error/reset was lost: %v", err)
	}
	if a.provider != llm.ProviderCodexCLI || a.modelID != "selected-quota-test" {
		t.Fatalf("selected agent changed: %s/%s", a.provider, a.modelID)
	}
}

func TestInitializationFailureDoesNotUseLegacyBackup(t *testing.T) {
	var config AgentLLMConfiguration
	if err := json.Unmarshal([]byte(`{"primary":{"provider":"unavailable-provider","model_id":"selected"},"fallbacks":[{"provider":"codex-cli","model_id":"backup"}]}`), &config); err != nil {
		t.Fatal(err)
	}
	listener := &recordingAgentEventListener{}
	a := &Agent{provider: llm.Provider(config.Primary.Provider), modelID: config.Primary.ModelID, llmConfig: config, logger: loggerv2.NewNoop(), listeners: []AgentEventListener{listener}}
	_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "unavailable-provider") {
		t.Fatalf("selected initialization failure was lost: %v", err)
	}
	if a.modelID != "selected" || string(a.provider) != "unavailable-provider" {
		t.Fatalf("selection mutated: %s/%s", a.provider, a.modelID)
	}
	for _, event := range listener.events {
		if strings.Contains(string(event.Type), "fallback") || string(event.Type) == "model_change" || string(event.Type) == "retry_attempt" {
			t.Fatalf("unexpected recovery event: %s", event.Type)
		}
	}
}

// A quota refusal belongs to the selected account, in both the current Agent
// and a fresh Agent for the next turn. The unavailable provider is intentional:
// reaching its initialization error proves B passed the quota gate without a
// real provider call or credentials.
func TestQuotaCooldownIsScopedToAccount(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, account := range []string{"account-A", ""} {
			name := "local/" + account
			if shared {
				name = "shared/" + account
			}
			t.Run(name, func(t *testing.T) {
				model := LLMModel{Provider: "unavailable-provider", ModelID: t.Name(), ConnectionID: account}
				key := modelQuotaKey(model)
				t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })
				reset := time.Now().Add(time.Hour)
				a := &Agent{llmConfig: AgentLLMConfiguration{Primary: model}, logger: loggerv2.NewNoop()}
				if shared {
					markModelQuotaExhaustedShared(key, reset)
				} else {
					a.quotaExhaustedModels = map[quotaModelKey]time.Time{key: reset}
				}
				checkExhausted := func() {
					t.Helper()
					_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 1)
					if llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted || !llmerrors.RetryAtOrZero(err).Equal(reset) {
						t.Fatalf("account A lost its quota/reset: %v", err)
					}
				}
				checkExhausted()
				a.llmConfig.Primary.ConnectionID = "account-B"
				_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 2)
				if err == nil || llmerrors.KindOf(err) == llmerrors.KindQuotaExhausted || !strings.Contains(err.Error(), "unavailable-provider") {
					t.Fatalf("account B did not reach its own provider initialization: %v", err)
				}
				a.llmConfig.Primary.ConnectionID = account
				if account == "" {
					// Explicit and implicit server accounts share the same cooldown.
					a.llmConfig.Primary.ConnectionID = "global:" + model.Provider
				}
				checkExhausted()
			})
		}
	}
}
