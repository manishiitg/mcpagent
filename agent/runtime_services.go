package mcpagent

import (
	"context"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// RunText is a compatibility convenience for command-line examples. Product
// code should retain Result from Agent.Run or Session.Run.
func RunText(ctx context.Context, agent *Agent, input string) (string, error) {
	result, err := agent.Run(ctx, Turn{Input: input})
	return result.Text, err
}

// RunHistory executes one turn from caller-owned history while returning the
// structured history produced by the runtime.
func RunHistory(ctx context.Context, agent *Agent, history []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	result, err := agent.Run(ctx, Turn{History: history})
	return result.Text, result.History, err
}

// ObserveAgent attaches a legacy event observer and returns its removal
// function. New streaming consumers should prefer Session.Events.
func ObserveAgent(agent *Agent, observer AgentEventListener) func() {
	if agent == nil || observer == nil {
		return func() {}
	}
	agent.addEventListener(observer)
	return func() { agent.removeEventListener(observer) }
}

func SubscribeAgentEvents(ctx context.Context, agent *Agent) (<-chan *events.AgentEvent, func(), bool) {
	if agent == nil {
		return nil, func() {}, false
	}
	return agent.subscribeToEvents(ctx)
}

// AgentDiagnostics is a read-only operational snapshot. Identity is exposed
// separately by Agent.Definition and turn accounting by Result.Usage.
type AgentDiagnostics struct {
	Usage           Usage
	ServerNames     []string
	DiscoveredTools int
	DeferredTools   int
}

func ReadAgentDiagnostics(agent *Agent) AgentDiagnostics {
	if agent == nil {
		return AgentDiagnostics{}
	}
	prompt, completion, total, cache, reasoning, calls, cacheCalls,
		inputCost, outputCost, reasoningCost, cacheCost, totalCost, contextUsage := agent.getTokenUsageWithPricing()
	return AgentDiagnostics{
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
		ServerNames:     agent.getServerNames(),
		DiscoveredTools: agent.getDiscoveredToolCount(),
		DeferredTools:   agent.getDeferredToolCount(),
	}
}

func AgentTokenUsage(agent *Agent) (promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCalls, cacheEnabledCalls int) {
	diagnostics := ReadAgentDiagnostics(agent)
	u := diagnostics.Usage
	return u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.CacheTokens, u.ReasoningTokens, u.LLMCalls, u.CacheEnabledLLMCalls
}

func AgentServerNames(agent *Agent) []string {
	return ReadAgentDiagnostics(agent).ServerNames
}

func AgentDiscoveredToolCount(agent *Agent) int {
	return ReadAgentDiagnostics(agent).DiscoveredTools
}

func AgentDeferredToolCount(agent *Agent) int {
	return ReadAgentDiagnostics(agent).DeferredTools
}

// The helpers below isolate low-level bridge and large-output test harnesses
// from the runtime Agent surface. They are not agent identity operations.
func BuildAgentBridgeConfig(agent *Agent) (string, error) {
	return agent.buildBridgeMCPConfig()
}

func BuildAgentLargeOutputPath(agent *Agent, filename string) string {
	return agent.buildLargeOutputFilePath(filename)
}

func InvokeAgentVirtualTool(ctx context.Context, agent *Agent, name string, args map[string]interface{}) (string, error) {
	return agent.handleVirtualTool(ctx, name, args)
}

func InvokeAgentLargeOutputTool(ctx context.Context, agent *Agent, name string, args map[string]interface{}) (string, error) {
	return agent.handleLargeOutputVirtualTool(ctx, name, args)
}

func ConfigureAgentToolOutput(agent *Agent, handler *ToolOutputHandler) {
	agent.setToolOutputHandler(handler)
}

func AgentToolOutput(agent *Agent) *ToolOutputHandler {
	return agent.getToolOutputHandler()
}

func CreateAgentVirtualTools(agent *Agent) []llmtypes.Tool {
	return agent.createVirtualTools()
}

func CreateAgentLargeOutputTools(agent *Agent) []llmtypes.Tool {
	return agent.createLargeOutputVirtualTools()
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

func AgentProvider(agent *Agent) llm.Provider {
	return ReadAgentRuntimeInfo(agent).Provider
}

func AgentLLMConfig(agent *Agent) LLMModel {
	return ReadAgentRuntimeInfo(agent).LLMConfig
}

func DefinitionToolExecutor(agent *Agent, name string) ToolExecutor {
	if agent == nil {
		return nil
	}
	return agent.getCustomToolExecutor(name)
}

func FindDefinitionTool(agent *Agent, name string) (ToolDefinitionView, bool) {
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

// Test-only construction helpers keep synthetic fixtures out of Agent's
// production method set.
func SetAgentProviderForTesting(agent *Agent, provider llm.Provider) {
	if agent != nil {
		agent.setProvider(provider)
	}
}

func DrainAgentSteerMessagesForTesting(agent *Agent) []string {
	if agent == nil {
		return nil
	}
	return agent.drainSteerMessages()
}

func SetAgentInstructionsForTesting(agent *Agent, instructions string) {
	if agent != nil {
		agent.setInstructions(instructions)
	}
}
