package mcpagent

import (
	"context"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func SubscribeAgentEvents(ctx context.Context, agent *Agent) (<-chan *events.AgentEvent, func(), bool) {
	if agent == nil {
		return nil, func() {}, false
	}
	return agent.subscribeToEvents(ctx)
}

// AgentDiagnostics is a read-only operational snapshot. Identity is exposed
// separately by Agent.Definition and turn accounting by Result.Usage.
type AgentDiagnostics struct {
	Usage       Usage
	ServerNames []string
	Connections int
	LargeOutput LargeOutputDiagnostics
}

type LargeOutputDiagnostics struct {
	Enabled      bool
	OutputFolder string
	SessionID    string
}

func ReadAgentDiagnostics(agent *Agent) AgentDiagnostics {
	if agent == nil {
		return AgentDiagnostics{}
	}
	prompt, completion, total, cache, reasoning, calls, cacheCalls,
		inputCost, outputCost, reasoningCost, cacheCost, totalCost, contextUsage := agent.getTokenUsageWithPricing()
	diagnostics := AgentDiagnostics{
		Usage: Usage{
			PromptTokens:         prompt,
			CompletionTokens:     completion,
			TotalTokens:          total,
			CacheTokens:          cache,
			ReasoningTokens:      reasoning,
			LLMCalls:             calls,
			CacheEnabledLLMCalls: cacheCalls,
			InputCostUSD:         inputCost,
			OutputCostUSD:        outputCost,
			ReasoningCostUSD:     reasoningCost,
			CacheCostUSD:         cacheCost,
			TotalCostUSD:         totalCost,
			ContextUsagePercent:  contextUsage,
		},
		ServerNames: agent.getServerNames(),
		Connections: len(agent.clients),
	}
	if handler := agent.getToolOutputHandler(); handler != nil {
		diagnostics.LargeOutput = LargeOutputDiagnostics{
			Enabled:      agent.enableContextOffloading,
			OutputFolder: handler.GetToolOutputFolder(),
			SessionID:    handler.GetSessionID(),
		}
	}
	return diagnostics
}

// The helpers below isolate low-level bridge and large-output test harnesses
// from the runtime Agent surface. They are not agent identity operations.
func InvokeAgentVirtualTool(ctx context.Context, agent *Agent, name string, args map[string]interface{}) (string, error) {
	return agent.handleVirtualTool(ctx, name, args)
}

type AgentRuntimeInfo struct {
	Provider             llm.Provider
	LLMConfig            LLMModel
	ConfiguredServerName string
	SelectedTools        []string
}

func ReadAgentRuntimeInfo(agent *Agent) AgentRuntimeInfo {
	if agent == nil {
		return AgentRuntimeInfo{}
	}
	return AgentRuntimeInfo{
		Provider:             agent.getProvider(),
		LLMConfig:            agent.getLLMModelConfig(),
		ConfiguredServerName: agent.getConfiguredServerName(),
		SelectedTools:        agent.getSelectedTools(),
	}
}

func findDefinitionTool(agent *Agent, name string) (ToolDefinitionView, bool) {
	if agent == nil {
		return ToolDefinitionView{}, false
	}
	for _, tool := range agent.Definition().Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolDefinitionView{}, false
}

func DeliverAgentInput(ctx context.Context, agent *Agent, req UserMessageDeliveryRequest) (UserMessageDeliveryResult, error) {
	return agent.deliverUserMessage(ctx, req)
}

func DeliverAgentControlKey(ctx context.Context, agent *Agent, req ControlKeyDeliveryRequest) (ControlKeyDeliveryResult, error) {
	return agent.deliverControlKey(ctx, req)
}

func StartAgentTransportSession(ctx context.Context, agent *Agent) (*llmtypes.CodingProviderSessionHandle, error) {
	return agent.startCodingAgentTransportSession(ctx)
}

func SnapshotAgentSession(agent *Agent) *AgentSessionHandle {
	if agent == nil {
		return nil
	}
	return agent.currentAgentSessionHandle()
}

func ApplyAgentResumeHandle(agent *Agent, handle *AgentSessionHandle) {
	if agent != nil {
		agent.applyAgentSessionHandle(handle)
	}
}
