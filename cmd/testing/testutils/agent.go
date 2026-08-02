package testutils

import (
	"context"
	"fmt"
	"os"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestAgentConfig holds configuration for test agent creation
type TestAgentConfig struct {
	LLM        llmtypes.Model
	Provider   llm.Provider // LLM provider (needed for agent configuration)
	ServerName string
	ConfigPath string
	// ModelID is no longer needed - it's automatically extracted from LLM
	Tracer     observability.Tracer
	TraceID    observability.TraceID
	Logger     loggerv2.Logger
	Definition mcpagent.AgentDefinition
	Runtime    mcpagent.RuntimeConfig
}

// RunText keeps command-test ergonomics out of the public mcpagent API.
func RunText(ctx context.Context, agent *mcpagent.Agent, input string) (string, error) {
	result, err := agent.Run(ctx, mcpagent.Turn{Input: input})
	return result.Text, err
}

// AgentTokenUsage exposes the legacy tuple shape only inside command tests.
func AgentTokenUsage(agent *mcpagent.Agent) (promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCalls, cacheEnabledCalls int) {
	usage := mcpagent.ReadAgentDiagnostics(agent).Usage
	return usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, usage.CacheTokens, usage.ReasoningTokens, usage.LLMCalls, usage.CacheEnabledLLMCalls
}

// CreateTestAgent creates a test agent with the specified configuration.
func CreateTestAgent(ctx context.Context, cfg *TestAgentConfig) (*mcpagent.Agent, error) {
	if cfg == nil {
		return nil, fmt.Errorf("agent config cannot be nil")
	}

	if cfg.LLM == nil {
		return nil, fmt.Errorf("LLM cannot be nil")
	}

	if cfg.Logger == nil {
		cfg.Logger = loggerv2.NewNoop()
	}

	runtime := cfg.Runtime
	runtime.Model = cfg.LLM
	runtime.MCPConfigPath = cfg.ConfigPath
	if cfg.Provider != "" {
		runtime.Generation.Provider = cfg.Provider
	}
	if cfg.ServerName != "" {
		cfg.Definition.Tools.MCP = append(cfg.Definition.Tools.MCP, mcpagent.MCPToolSource{Name: cfg.ServerName})
	}
	if cfg.Tracer != nil {
		runtime.Observability.Tracers = append(runtime.Observability.Tracers, cfg.Tracer)
	}
	if cfg.TraceID != "" {
		runtime.Observability.TraceID = cfg.TraceID
	}
	if cfg.Logger != nil {
		runtime.Observability.Logger = cfg.Logger
	}

	agent, err := mcpagent.NewAgentFromDefinition(ctx, cfg.Definition, runtime)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	return agent, nil
}

// CreateMinimalAgent creates a minimal test agent with empty MCP config.
// Useful for tests that don't need MCP servers.
func CreateMinimalAgent(ctx context.Context, model llmtypes.Model, provider llm.Provider, tracer observability.Tracer, traceID observability.TraceID, logger loggerv2.Logger) (*mcpagent.Agent, error) {
	// Create temporary minimal config
	tempConfig := "/tmp/minimal-mcp-config.json"
	minimalConfig := `{"mcpServers": {}}`
	if err := os.WriteFile(tempConfig, []byte(minimalConfig), 0644); err != nil { //nolint:gosec // 0644 permissions are intentional for test config files
		return nil, fmt.Errorf("failed to create temp config: %w", err)
	}

	// Cleanup function
	defer func() {
		_ = os.Remove(tempConfig) //nolint:gosec // Cleanup errors are non-critical
	}()

	cfg := &TestAgentConfig{
		LLM:        model,
		Provider:   provider,
		ConfigPath: tempConfig,
		Tracer:     tracer,
		TraceID:    traceID,
		Logger:     logger,
	}

	return CreateTestAgent(ctx, cfg)
}

// CreateAgentWithTracer creates a test agent with a specific tracer.
func CreateAgentWithTracer(ctx context.Context, model llmtypes.Model, provider llm.Provider, configPath string, tracer observability.Tracer, traceID observability.TraceID, logger loggerv2.Logger, runtime ...mcpagent.RuntimeConfig) (*mcpagent.Agent, error) {
	var runtimeConfig mcpagent.RuntimeConfig
	if len(runtime) > 0 {
		runtimeConfig = runtime[0]
	}
	cfg := &TestAgentConfig{
		LLM:        model,
		Provider:   provider,
		ConfigPath: configPath,
		Tracer:     tracer,
		TraceID:    traceID,
		Logger:     logger,
		Runtime:    runtimeConfig,
	}

	return CreateTestAgent(ctx, cfg)
}

// IsNoopTracer checks if a tracer is a NoopTracer.
// This is useful for determining if tracing is actually enabled.
func IsNoopTracer(tracer observability.Tracer) bool {
	if tracer == nil {
		return true
	}
	_, ok := tracer.(observability.NoopTracer)
	return ok
}

// IsLangfuseTracer checks if a tracer is a LangfuseTracer (not NoopTracer).
// This is useful for determining if Langfuse tracing is enabled.
func IsLangfuseTracer(tracer observability.Tracer) bool {
	if tracer == nil {
		return false
	}
	// Check if it's NOT a NoopTracer
	_, isNoop := tracer.(observability.NoopTracer)
	if isNoop {
		return false
	}
	// If it's not a NoopTracer and not nil, assume it's a LangfuseTracer
	// (or another real tracer implementation)
	return true
}

// GetTracerWithLogger gets a tracer with the specified provider and logger.
// Returns the tracer and a boolean indicating if it's a real tracer (not NoopTracer).
func GetTracerWithLogger(provider string, logger loggerv2.Logger) (observability.Tracer, bool) {
	tracer := observability.GetTracerWithLogger(provider, logger)
	isReal := IsLangfuseTracer(tracer)
	return tracer, isReal
}

// GenerateTestTraceID generates a unique trace ID for testing.
func GenerateTestTraceID() observability.TraceID {
	return observability.TraceID(fmt.Sprintf("test-trace-%d", time.Now().UnixNano()))
}
