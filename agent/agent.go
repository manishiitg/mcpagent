package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/convrecord"
	"github.com/manishiitg/mcpagent/agent/prompt"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/mcpagent/toolcalllog"
)

// AgentEventListener defines the interface for event listeners
type AgentEventListener interface {
	HandleEvent(ctx context.Context, event *events.AgentEvent) error
	Name() string
}

// AgentMode defines the type of agent behavior
type AgentMode string

const (
	// SimpleAgent is the standard tool-using agent without explicit reasoning
	SimpleAgent AgentMode = "simple"
)

// agentOption defines a functional option for configuring an Agent.
// These options modify the Agent's state during initialization (newAgent).
type agentOption func(*Agent)

// withLogger sets a custom logger implementation.
//
// Allows injecting a specialized logger for structured logging or integrating
// with existing application loggers.
//
// Default: loggerv2.NewDefault() (Standard output logger)
func withLogger(logger loggerv2.Logger) agentOption {
	return func(a *Agent) {
		a.logger = logger
	}
}

// withTracer adds an observability tracer to the agent.
//
// The provided tracer will be wrapped in a StreamingTracer to support real-time
// event streaming. Multiple tracers can be added by calling this option multiple times.
//
// Parameters:
//   - tracer: The observability tracer implementation (e.g., Langfuse, Console, etc.).
//
// Default: No tracers (unless newAgentWithObservability is used)
func withTracer(tracer observability.Tracer) agentOption {
	return func(a *Agent) {
		if tracer != nil {
			// Create streaming tracer that wraps the base tracer
			streamingTracer := NewStreamingTracer(tracer, 100)
			// Add to tracers slice
			a.tracers = append(a.tracers, streamingTracer)
		}
	}
}

// withTraceID sets a specific Trace ID for the agent session.
//
// Useful for correlating agent activities with external systems or requests
// (e.g., setting the TraceID to match an incoming HTTP request ID).
//
// Default: Generated automatically by newAgent.
func withTraceID(traceID observability.TraceID) agentOption {
	return func(a *Agent) {
		a.traceID = traceID
	}
}

// withProvider explicitly sets the LLM provider name.
//
// This is primarily used for logging and tracking purposes, as the actual
// provider logic is encapsulated in the llmtypes.Model interface.
func withProvider(provider llm.Provider) agentOption {
	return func(a *Agent) {
		a.provider = provider
	}
}

// withClaudeCodePersistentInteractiveSession keeps Claude Code tmux sessions
// alive across completed turns. Coding CLI providers now use this interactive
// path whenever an owner session id is available; this option remains for
// callers that set metadata explicitly.
func withClaudeCodePersistentInteractiveSession(enabled bool) agentOption {
	return func(a *Agent) {
		a.claudeCodePersistentInteractiveSession = enabled
	}
}

// withClaudeCodeTransport selects the Claude Code transport for this agent.
// Use llm.ClaudeCodeTransportExperimental for the normal interactive tmux/no
// -p path, or llm.ClaudeCodeTransportTmux for explicit tmux transport.
func withClaudeCodeTransport(transport string) agentOption {
	return func(a *Agent) {
		a.claudeCodeTransport = transport
	}
}

// withCodingAgentWorkingDir sets the process working directory for interactive
// coding CLI providers. The caller owns choosing the right user/workspace path.
func withCodingAgentWorkingDir(dir string) agentOption {
	return func(a *Agent) {
		a.codingAgentWorkingDir = strings.TrimSpace(dir)
	}
}

// withIsolatedSessionWorkspace asks the coding-CLI session to run in a
// fresh per-call os.MkdirTemp directory instead of CodingAgentWorkingDir.
// When enabled, the agent:
//
//   - Creates a new tmp dir before launching the CLI session
//   - Overrides the CLI's cwd / --dir option to that tmp path
//   - rm -rf's the tmp dir after the session completes
//
// The MCP bridge config (which already carries the actual workflow dir
// paths in its env / args) is unchanged — the bridge subprocess runs
// outside the CLI's sandbox so it can still touch the user's workflow
// dir for file ops the model invokes via bridge tools.
//
// Intended for WORKFLOW STEPS where:
//   - Resume is never needed (each step is a fresh conversation)
//   - Concurrent steps must not collide on the same workspace files
//   - The user's actual workflow dir must be protected from accidental
//     model writes via the CLI's built-in editing tools
//
// Chat code paths (multi-agent + builder) should NOT set this — they
// need the agent to operate directly on the user's chosen workspace
// dir for the "agent edits my files" UX and need resume-tied-to-dir
// for session continuity.
//
// See docs/WORKFLOW_STEP_ISOLATION.md in multi-llm-provider-go for the
// design rationale and per-CLI sandbox interaction details.
func withIsolatedSessionWorkspace(enabled bool) agentOption {
	return func(a *Agent) {
		a.isolatedSessionWorkspace = enabled
	}
}

// withCodexPersistentInteractiveSession keeps Codex CLI tmux sessions alive
// across completed turns. Coding CLI providers now use this interactive path
// whenever an owner session id is available; this option remains for callers
// that set metadata explicitly.
func withCodexPersistentInteractiveSession(enabled bool) agentOption {
	return func(a *Agent) {
		a.codexPersistentInteractiveSession = enabled
	}
}

// withCodexSandbox overrides codex's sandbox mode ("read-only" [default],
// "workspace-write", or "danger-full-access"). See the CodexSandboxMode field
// doc comment on Agent for when to change this from the default.
func withCodexSandbox(mode string) agentOption {
	return func(a *Agent) {
		a.codexSandboxMode = mode
	}
}

// withCodexNetworkAccess enables codex's native network access under
// "workspace-write" sandbox mode. No effect under "read-only" or
// "danger-full-access". See the CodexNetworkAccess field doc comment on Agent.
func withCodexNetworkAccess(enabled bool) agentOption {
	return func(a *Agent) {
		a.codexNetworkAccess = enabled
	}
}

// withCodingAgentTransport is THE way to choose a coding-agent CLI provider's
// process transport for this agent:
//
//   - llm.CodingAgentTransportTmux — a live tmux pane. Choose when a human may
//     steer the turn mid-flight, or the pane is rendered in a UI.
//   - llm.CodingAgentTransportStructured — one-shot JSON (`claude -p
//     --output-format stream-json`, `codex exec --json`, `cursor-agent --print`,
//     `pi --print --mode json`). Choose for unattended work: explicit
//     completion and token-usage events instead of scraping a terminal, and no
//     live-steering (Deliver queues instead).
//   - "" (unset) — use the provider contract's declared transport.
//
// Every current coding CLI supports BOTH; transport is a property of the use
// case, not the provider. This is the ONLY way to choose transport: the older
// overlapping mechanisms (WithForceStructuredCodingAgent and four per-provider
// With*StructuredTransport options) were removed — they let the same decision
// be expressed four different ways, and the generic one silently didn't work.
func withCodingAgentTransport(transport llm.CodingAgentTransport) agentOption {
	return func(a *Agent) {
		a.codingAgentTransport = transport
	}
}

// withCursorPersistentInteractiveSession keeps Cursor CLI tmux sessions alive
// across completed chat turns. Use only for interactive chat; workflow steps
// should keep the default per-turn lifecycle.
func withCursorPersistentInteractiveSession(enabled bool) agentOption {
	return func(a *Agent) {
		a.cursorPersistentInteractiveSession = enabled
	}
}

// withPiPersistentInteractiveSession keeps Pi CLI tmux sessions alive across
// completed chat turns.
func withPiPersistentInteractiveSession(enabled bool) agentOption {
	return func(a *Agent) {
		a.piPersistentInteractiveSession = enabled
	}
}

// withCursorBridgeToolsMode marks a chat as preferring MCP bridge tools.
// The flag is retained for API compatibility but no longer sets --mode ask:
// that mode hard-refuses natural-language writes with "Switch to Agent mode",
// making chat unusable. Cursor runs in its default agent mode; the MCP bridge
// is still mounted via .cursor/mcp.json for tools the model chooses to use.
func withCursorBridgeToolsMode(enabled bool) agentOption {
	return func(a *Agent) {
		a.cursorBridgeToolsMode = enabled
	}
}

// withMaxTurns sets the maximum number of conversation turns allowed.
//
// A turn consists of one user message and one agent response (which may include multiple tool calls).
// This prevents infinite loops or excessive token usage.
//
// Parameters:
//   - maxTurns: The specific limit to set.
//     Use a negative value to disable the turn cap.
//
// Default: Value returned by GetDefaultMaxTurns()
func withMaxTurns(maxTurns int) agentOption {
	return func(a *Agent) {
		a.maxTurns = maxTurns
	}
}

// withTemperature sets the sampling temperature for the LLM.
//
// Higher values (e.g., 0.8) make output more random/creative.
// Lower values (e.g., 0.2) make output more focused/deterministic.
//
// Default: 0.0 (Deterministic)
func withTemperature(temperature float64) agentOption {
	return func(a *Agent) {
		a.temperature = temperature
	}
}

// withToolChoice forces a specific tool choice strategy.
//
// Parameters:
//   - toolChoice: "auto", "none", or a specific tool name (depending on provider support).
//
// Default: "auto"
func withToolChoice(toolChoice string) agentOption {
	return func(a *Agent) {
		a.toolChoice = toolChoice
	}
}

// withContextOffloading enables the "Context Offloading" pattern.
//
// When enabled, if a tool returns a massive output (exceeding LargeOutputThreshold),
// the agent will automatically save it to a file and provide the LLM with a "virtual tool"
// to read that file on demand, rather than flooding the context window.
//
// Default: true (Enabled)
func withContextOffloading(enabled bool) agentOption {
	return func(a *Agent) {
		a.enableContextOffloading = enabled
	}
}

// withLargeOutputThreshold sets the token count threshold for context offloading.
//
// Tool outputs larger than this value will be offloaded to the filesystem.
// The count is based on token estimate, not character count.
//
// Parameters:
//   - threshold: Token count limit.
//
// Default: 10,000 tokens
func withLargeOutputThreshold(threshold int) agentOption {
	return func(a *Agent) {
		a.largeOutputThreshold = threshold
	}
}

// withToolOutputRetentionPeriod sets the retention policy for offloaded tool output files.
//
// Files created by context offloading will be deleted if they are older than this duration.
// A periodic cleanup routine runs every hour to remove files older than the retention period.
//
// Parameters:
//   - retentionPeriod: Duration to keep files. Set to 0 to disable automatic cleanup.
//
// Default: 7 days (DefaultToolOutputRetentionPeriod). Periodic cleanup runs every hour.
func withToolOutputRetentionPeriod(retentionPeriod time.Duration) agentOption {
	return func(a *Agent) {
		a.toolOutputRetentionPeriod = retentionPeriod
	}
}

// withCleanupToolOutputOnSessionEnd configures immediate cleanup behavior.
//
// If enabled, all tool output files created during this session will be deleted
// when EndAgentSession is called.
//
// Default: false (Files persist for debugging or future reference)
func withCleanupToolOutputOnSessionEnd(enabled bool) agentOption {
	return func(a *Agent) {
		a.cleanupToolOutputOnSessionEnd = enabled
	}
}

// withContextSummarization enables automatic conversation summarization.
//
// When the context window fills up (based on TokenThresholdPercent), the agent will
// summarize older messages to free up space while retaining context.
//
// Default: false (Disabled)
func withContextSummarization(enabled bool) agentOption {
	return func(a *Agent) {
		a.enableContextSummarization = enabled
	}
}

// withSummarizeOnTokenThreshold configures the trigger for summarization.
//
// Parameters:
//   - enabled: Whether to use token-based triggering.
//   - thresholdPercent: The percentage of the model's context window (0.0 - 1.0)
//     that triggers summarization.
//
// Default: 0.8 (80%) if enabled.
func withSummarizeOnTokenThreshold(enabled bool, thresholdPercent float64) agentOption {
	return func(a *Agent) {
		a.summarizeOnTokenThreshold = enabled
		if thresholdPercent > 0 && thresholdPercent <= 1.0 {
			a.tokenThresholdPercent = thresholdPercent
		} else {
			a.tokenThresholdPercent = 0.8 // Default to 80%
		}
	}
}

// withSummarizeOnFixedTokenThreshold enables fixed token-based summarization triggering
// When enabled, summarization triggers when token usage exceeds the fixed threshold
// (e.g., 200000 = 200k tokens, regardless of context window size)
// Requires EnableContextSummarization to be true
// Can be used together with withSummarizeOnTokenThreshold (OR logic: either threshold can trigger)
func withSummarizeOnFixedTokenThreshold(enabled bool, thresholdTokens int) agentOption {
	return func(a *Agent) {
		a.summarizeOnFixedTokenThreshold = enabled
		if thresholdTokens > 0 {
			a.fixedTokenThreshold = thresholdTokens
		}
	}
}

// withSummaryKeepLastMessages sets the number of recent messages to keep when summarizing
// Default is 4 messages (roughly 2 turns)
func withSummaryKeepLastMessages(count int) agentOption {
	return func(a *Agent) {
		a.summaryKeepLastMessages = count
	}
}

// withSummarizationCooldown sets the number of turns to wait after summarization before allowing another
// This prevents repeated summarization loops when the summarized context is still large
// Default is 3 turns
func withSummarizationCooldown(turns int) agentOption {
	return func(a *Agent) {
		a.summarizationCooldownTurns = turns
	}
}

// withParallelToolExecution enables concurrent execution of multiple tool calls.
//
// When the LLM returns multiple tool calls in a single response, they will be
// executed concurrently using goroutines (fork-join pattern). Results are collected
// in deterministic order matching the original tool call order.
//
// Default: false (Sequential execution)
func withParallelToolExecution(enabled bool) agentOption {
	return func(a *Agent) {
		a.enableParallelToolExecution = enabled
	}
}

// withContextEditing enables dynamic context reduction.
//
// Unlike summarization (which compresses history), context editing targets specific
// large tool outputs in the history and replaces them with references if they become
// too old or too large, optimizing the context window.
//
// Default: false (Disabled)
func withContextEditing(enabled bool) agentOption {
	return func(a *Agent) {
		a.enableContextEditing = enabled
	}
}

// withContextEditingThreshold sets the size threshold for context editing.
//
// Tool outputs larger than this token count are candidates for compaction when they
// become "stale" (old).
//
// Default: 1000 tokens
func withContextEditingThreshold(threshold int) agentOption {
	return func(a *Agent) {
		a.contextEditingThreshold = threshold
	}
}

// withContextEditingTurnThreshold sets the age threshold for context editing.
//
// Tool outputs must be at least this many turns old before they are compacted.
// This ensures recent tool outputs stay in context for immediate reference.
//
// Default: 10 turns
func withContextEditingTurnThreshold(turns int) agentOption {
	return func(a *Agent) {
		a.contextEditingTurnThreshold = turns
	}
}

// withToolTimeout sets a global timeout for tool execution.
//
// If a tool takes longer than this duration, it will be cancelled.
// A timeout <= 0 means no agent-level tool timeout.
//
// Default: no timeout
func withToolTimeout(timeout time.Duration) agentOption {
	return func(a *Agent) {
		a.toolTimeout = timeout
	}
}

// withSystemPrompt sets a custom system prompt.
//
// This overrides the default system prompt generation logic. The agent will use
// this exact string as the system instruction.
//
// Note: To add supplementary instructions, use AddInstructions.
func withSystemPrompt(systemPrompt string) agentOption {
	return func(a *Agent) {
		a.systemPrompt = systemPrompt
		a.hasCustomSystemPrompt = true
	}
}

// withBridgeRoutingInstructions overrides the default bridge-tool-routing
// system-prompt text mcpagent appends for EVERY CLI coding-agent provider —
// Claude Code, Codex CLI, Cursor CLI, and Pi CLI each get their own
// provider-specific preamble plus the shared bridgeRoutingExplicitInstructions
// block (see coding_agent_bridge_routing_prompt.go and the per-provider
// auto-configure sections in newAgent). The default is tuned for AgentWorks'
// large, dynamic tool catalog — discovering tools via get_api_spec and
// calling them through execute_shell_command + curl with $MCP_CUSTOM/$MCP_AUTH
// — and uses urgent "CRITICAL"/"DO NOT"/override-style language to make sure
// the model doesn't give up on a denied built-in. That same language is
// close enough to a textbook prompt-injection shape that some providers'
// (notably Claude Code's) own safety training can flag it to the user, and
// no caller-side system prompt reliably talks it out of that once triggered
// — asking a model to stand down on a suspected injection is exactly the
// kind of instruction safety training is built to resist.
//
// Callers with a SMALL, fixed, natively-registered tool set (e.g.
// agentsession-based apps, where every custom tool is directly callable by
// name — see docs/core/mcp_bridge_layer.md) don't need the discovery/curl
// routing at all, and can pass calmer, app-specific wording here instead —
// or "" to suppress the block entirely for this agent. Applies uniformly to
// whichever provider this agent ends up using, not just Claude Code.
func withBridgeRoutingInstructions(text string) agentOption {
	return func(a *Agent) {
		a.bridgeRoutingInstructionsOverride = &text
	}
}

// withConversationSink attaches a convrecord.Sink so every completed LLM
// call is persisted as a convrecord.TurnRecord — messages, tool calls (with
// timing), token usage, and cost. OFF by default: no file/store I/O happens
// unless a caller opts in.
//
// This exists to close a real, observed duplication — see the convrecord
// package doc for the full story (AgentWorks and sparkquill each
// independently reimplemented "extract a completed turn and persist it,"
// in two different, incompatible shapes, one of them tracking no cost at
// all). Use convrecord.NewFileJSONSink(path) for the common case, or
// implement Sink yourself for a different store (SQLite, etc.).
func withConversationSink(sink convrecord.Sink) agentOption {
	return func(a *Agent) {
		a.conversationSink = sink
	}
}

// withAdditionalBridgeTools exposes the named custom tools (already
// registered via RegisterCustomTool) as NATIVE MCP bridge tools for THIS
// agent instance — callable directly by name, without the get_api_spec +
// execute_shell_command+curl discovery route. Scoped to this agent only; it
// does NOT touch the shared package-level bridgeTools list (execute_shell_command,
// diff_patch_workspace_file, agent_browser, get_api_spec), which stays fixed
// across every consumer of this module.
//
// Use this for a small, app-specific, known-in-advance tool set (e.g. an
// agentsession-based app) where native calling is more reliable than asking
// the model to discover-then-curl each tool. Do not use the shared
// bridgeTools var for this — see coding_agents_bridge.go.
func withAdditionalBridgeTools(names ...string) agentOption {
	return func(a *Agent) {
		a.additionalBridgeTools = append(a.additionalBridgeTools, names...)
	}
}

// withDiscoverResource enables/disables automatic resource discovery.
//
// If enabled, the agent will query all connected MCP servers for their available resources
// and include them in the system prompt.
//
// Default: true
func withDiscoverResource(enabled bool) agentOption {
	return func(a *Agent) {
		a.discoverResource = enabled
	}
}

// withDiscoverPrompt enables/disables automatic prompt discovery.
//
// If enabled, the agent will query all connected MCP servers for their available prompts
// and include them in the system prompt.
//
// Default: true
func withDiscoverPrompt(enabled bool) agentOption {
	return func(a *Agent) {
		a.discoverPrompt = enabled
	}
}

// withLLMConfig sets the full LLM configuration (primary + fallbacks).
// This is the canonical configuration for provider and model fallback routing.
func withLLMConfig(config AgentLLMConfiguration) agentOption {
	return func(a *Agent) {
		a.llmConfig = config
		// Sync legacy fields for backward compatibility
		a.modelID = config.Primary.ModelID
		a.provider = llm.Provider(config.Primary.Provider)
	}
}

// withSelectedTools restricts the agent to a specific subset of tools.
//
// Parameters:
//   - tools: A list of tool identifiers in "server:tool" format (e.g., "github:create_issue").
//
// Only the specified tools will be available to the agent.
func withSelectedTools(tools []string) agentOption {
	return func(a *Agent) {
		a.selectedTools = tools
	}
}

// withSelectedServers restricts the agent to tools from specific servers.
//
// Parameters:
//   - servers: A list of server names (e.g., "github", "filesystem").
//
// All tools from these servers will be available. Tools from other servers will be hidden.
func withSelectedServers(servers []string) agentOption {
	return func(a *Agent) {
		// Store selected servers for tool filtering logic
		// This is used to determine which servers should use "all tools" mode
		a.selectedServers = servers
	}
}

// withCodeExecutionMode enables the Code Execution mode.
//
// In this mode, both MCP server tools and custom tools are accessed via HTTP endpoints documented in an OpenAPI spec.
// The LLM uses get_api_spec to discover endpoints, then writes code in any language
// (Python, bash, curl, etc.) to call them.
//
//   - Enabled: get_api_spec only. MCP and custom tools via HTTP API.
//   - Disabled: All MCP tools are exposed directly (Standard mode).
//
// Default: false (Standard mode)
func withCodeExecutionMode(enabled bool) agentOption {
	return func(a *Agent) {
		a.useCodeExecutionMode = enabled
	}
}

// withDisableCache controls the MCP client connection cache.
//
//   - disable=true: Always establish fresh connections (slower, but safer for ephemeral tasks).
//   - disable=false: Reuse connections from the pool (faster, default).
//
// Default: false (Caching enabled)
func withDisableCache(disable bool) agentOption {
	return func(a *Agent) {
		a.disableCache = disable
	}
}

// withRuntimeOverrides sets runtime configuration overrides for MCP servers.
//
// This allows workflow-specific modifications to server configs, such as:
//   - Changing output directories per workflow run
//   - Adding workflow-specific environment variables
//   - Appending additional command arguments
//
// Example:
//
//	overrides := mcpclient.RuntimeOverrides{
//	    "custom-server": {
//	        EnvOverride: map[string]string{"WORKFLOW_ID": "workflow-123"},
//	    },
//	}
//	agent, _ := mcpagent.newAgent(ctx, llm, configPath, mcpagent.withRuntimeOverrides(overrides))
func withRuntimeOverrides(overrides mcpclient.RuntimeOverrides) agentOption {
	return func(a *Agent) {
		a.runtimeOverrides = overrides
	}
}

// withStreaming enables streaming for LLM text responses.
//
// When enabled, provider stream chunks are consumed by the agent. Generation
// streaming events can be independently disabled with
// withGenerationStreamingEvents while still keeping provider stream chunks for
// CLI tool observability.
//
// Default: false (Streaming disabled)
func withStreaming(enabled bool) agentOption {
	return func(a *Agent) {
		a.enableStreaming = enabled
	}
}

// withGenerationStreamingEvents controls whether provider text chunks emit
// generation streaming events. Terminal snapshot chunks may still emit
// StreamingChunkEvent/StreamingEndEvent because they drive terminal
// observability, not chat text generation.
//
// Default: true (emit generation streaming events when streaming is enabled)
func withGenerationStreamingEvents(enabled bool) agentOption {
	return func(a *Agent) {
		a.suppressGenerationStreamingEvents = !enabled
	}
}

// withStreamingCallback sets an optional callback function for streaming chunks.
//
// The callback is invoked for each streaming chunk (content fragments only).
// Tool calls are not passed to this callback - they are processed normally.
//
// Parameters:
//   - callback: Function that receives StreamChunk objects (content fragments only).
//
// Default: nil (No callback)
func withStreamingCallback(callback func(chunk llmtypes.StreamChunk)) agentOption {
	return func(a *Agent) {
		a.streamingCallback = callback
	}
}

// withPromptLogLabel sets the diagnostic label used for prompt-log filenames.
// The label is runtime observability metadata and is fixed at construction.
func withPromptLogLabel(label string) agentOption {
	return func(a *Agent) {
		a.promptLogLabel = label
	}
}

// withAPIKeys supplies provider credentials used when constructing fallback
// models. The value is cloned so callers cannot mutate agent runtime state
// after construction.
func withAPIKeys(keys *AgentAPIKeys) agentOption {
	return func(a *Agent) {
		a.apiKeys = keys.Clone()
	}
}

// withServerName filters the agent to connect to a specific server(s).
//
// Parameters:
//   - serverName: A specific server name, a comma-separated list, or "all".
//
// Default: "all" (Connect to all configured servers)
func withServerName(serverName string) agentOption {
	return func(a *Agent) {
		a.serverName = serverName
	}
}

// withSessionID sets the session ID for connection sharing across agents.
//
// When set: MCP connections are managed by SessionConnectionRegistry and persist across
// multiple agents with the same SessionID. Agent.Close() does NOT close connections.
// Call CloseSession(sessionID) explicitly when the workflow/conversation ends.
//
// When empty, constructors normalize the value to "global" so connection management
// always goes through the session registry.
//
// Usage:
//
//	// Create agents with shared session
//	agent1, _ := newSimpleAgent(ctx, llm, config, withSessionID("workflow-123"))
//	agent1.Close() // Connections preserved
//
//	agent2, _ := newSimpleAgent(ctx, llm, config, withSessionID("workflow-123"))
//	agent2.Close() // Connections still preserved
//
//	// At workflow end
//	CloseSession("workflow-123") // Now connections are closed
func withSessionID(sessionID string) agentOption {
	return func(a *Agent) {
		a.sessionID = sessionID
	}
}

// withAPIConfig sets the executor API base URL and bearer token for code execution mode.
// Code execution subprocesses receive these as MCP_API_URL and MCP_API_TOKEN environment variables.
// The consumer application is responsible for starting the HTTP server and generating the token
// (see executor.GenerateAPIToken and executor.AuthMiddleware).
func withAPIConfig(baseURL, token string) agentOption {
	return func(a *Agent) {
		a.apiBaseURL = baseURL
		a.apiToken = token
	}
}

// withUserID sets the user ID for per-user OAuth token isolation.
//
// When set, OAuth tokens for MCP servers are stored at user-specific paths:
// ~/.config/mcpagent/tokens/{userID}/{serverName}.json
//
// This enables multi-user deployments where each user's OAuth credentials
// are isolated from other users.
//
// When empty (default): OAuth tokens use the path from MCP server configuration
// (typically a shared default path).
func withUserID(userID string) agentOption {
	return func(a *Agent) {
		a.userID = userID
	}
}

func isCodingCLIProvider(provider llm.Provider, modelID string) bool {
	return llm.IsCodingAgentProvider(provider, modelID)
}

func isCodingCLIBridgeProvider(provider llm.Provider, modelID string) bool {
	contract, ok := llm.GetCodingAgentProviderContract(provider, modelID)
	return ok && contract.UsesMCPBridge
}

// Agent wraps MCP clients, an LLM, and an observability tracer to answer questions using tool calls.
// It is the central component that orchestrates interactions between the Large Language Model (LLM),
// Model Context Protocol (MCP) servers, and various tools.
//
// The Agent is designed to be generic and reusable across different contexts such as CLI commands,
// backend services, or test suites. It manages conversation history, tool execution, context window
// optimization, and observability.
type Agent struct {
	// Context for cancellation and lifecycle management
	ctx context.Context

	// MCP clients keyed by server name.
	clients map[string]mcpclient.ClientInterface

	// Map tool name → server name (quick dispatch)
	toolToServer map[string]string

	llmModel llmtypes.Model
	tracers  []observability.Tracer // Support multiple tracers
	tools    []llmtypes.Tool

	// Configuration knobs
	maxTurns        int
	temperature     float64
	toolChoice      string
	modelID         string
	agentMode       AgentMode     // NEW: Agent mode (Simple or ReAct)
	toolTimeout     time.Duration // Tool execution timeout (default: 5 minutes)
	selectedTools   []string      // Selected tools in "server:tool" format
	selectedServers []string      // Selected servers list for "all tools" mode determination
	toolFilter      *ToolFilter   // Unified tool filter for consistent filtering

	// Enhanced tracking info
	systemPrompt string
	definition   *AgentDefinition
	traceID      observability.TraceID
	configPath   string // Path to MCP config file for on-demand connections
	serverName   string // Server name(s) to connect to (default: AllServers)

	// cached list of server names (for metadata convenience)
	servers []string

	// Provider information
	provider llm.Provider

	// Latest provider-native continuation handle returned by a coding-agent
	// adapter. This is the typed replacement for provider-specific session ID
	// fields below; the legacy fields remain while callers migrate.
	codingProviderSessionHandle llmtypes.CodingProviderSessionHandle

	// Claude Code CLI session ID for --resume on subsequent turns
	claudeCodeSessionID string

	// Claude Code experimental persistent tmux mode for interactive chat
	claudeCodePersistentInteractiveSession bool

	// Claude Code transport override for this agent.
	claudeCodeTransport string

	// Process working directory for interactive coding CLI providers.
	codingAgentWorkingDir string

	// CLISecurityPolicy is resolved by the owning application before launch.
	// Providers receive an immutable copy through CallOptions. Nil preserves the
	// backward-compatible compatibility mode.
	cliSecurityPolicy *llmtypes.CLISecurityPolicy

	// IsolatedSessionWorkspace, when true, asks the coding-CLI session
	// to run in a fresh os.MkdirTemp directory instead of
	// CodingAgentWorkingDir. The tmp dir is created at session launch
	// and rm -rf'd at session teardown. Intended for workflow steps;
	// chat code paths leave this false. See withIsolatedSessionWorkspace
	// and docs/WORKFLOW_STEP_ISOLATION.md in multi-llm-provider-go.
	isolatedSessionWorkspace bool

	// isolatedWorkspacePath and isolatedWorkspaceOnce back the lazy
	// per-Agent tmp dir created when IsolatedSessionWorkspace is true.
	// sync.Once guarantees exactly one tmp dir per Agent lifetime even
	// if appendCodingAgentWorkingDirOptionForProvider is called from
	// multiple goroutines. Agent.Close rm -rf's the dir if it was
	// ever created. Unexported because the lifecycle is managed
	// internally; callers control the feature via
	// withIsolatedSessionWorkspace.
	isolatedWorkspacePath string
	isolatedWorkspaceOnce sync.Once

	// isolatedWorkspaceStable reports that isolatedWorkspacePath was derived
	// from the session ID and is therefore SHARED by every turn of this
	// session. Agent.Close must not remove such a dir: a new Agent is built
	// per turn, so deleting on close would destroy the coding CLI's resumable
	// conversation between turns. Session-scoped dirs are reclaimed by
	// CloseSession (true end of session); only the random fallback dir is
	// removed on Agent.Close.
	isolatedWorkspaceStable bool

	// Codex CLI sandbox mode ("read-only", "workspace-write", or
	// "danger-full-access"). Defaults to "workspace-write" (see
	// appendCodexCLIIntegrationOptions) — codex gets native writes, matching how
	// it ran for most of this project's life, and the right default for the
	// common case: an interactive caller, or one where the bridge already grants
	// shell access anyway (so blocking codex's NATIVE writes stops nothing real —
	// the bridge can already write; see codex's unremovable `functions.exec`
	// tool, which is why this containment exists at all). Only opt into
	// "read-only" for a session that deliberately restricts its tool set (e.g.
	// "web_search only, no shell on the bridge" — read-only is the only thing
	// that makes that restriction hold for codex) or needs every action to hit
	// an audit trail native exec would otherwise bypass. Read-only is a narrow,
	// deliberate opt-in, not a safe-by-default choice: it also kills codex's
	// native network (there is no read-only+network mode), and codex tends to
	// disengage from tools entirely when its own preamble says "read-only, no
	// network".
	codexSandboxMode string

	// CodexNetworkAccess enables codex's native network access when
	// CodexSandboxMode is "workspace-write" (via `-c
	// sandbox_workspace_write.network_access=true`). Off by default — codex
	// never had native network before either; bridge tools (web_search etc.) get
	// network via the executor process regardless of this setting. Meaningless
	// under "read-only" (network is unconditionally off there) or
	// "danger-full-access" (network is unconditionally on there).
	codexNetworkAccess bool

	// Codex CLI project directory ID for per-invocation isolation (hooks, config)
	codexProjectDirID string

	// Codex CLI thread ID for legacy exec-json resume on subsequent turns
	codexSessionID string

	// Codex CLI persistent tmux mode
	codexPersistentInteractiveSession bool

	// Cursor CLI persistent tmux mode for interactive chat
	cursorPersistentInteractiveSession bool

	// Pi CLI persistent tmux mode for interactive chat
	piPersistentInteractiveSession bool

	// Pi CLI native session ID for --session-id resume on subsequent turns.
	piSessionID string

	// turnInFlight tracks whether a ContinueConversation turn is currently
	// running for this agent. Deliver reads it to make the steer-vs-query
	// decision: a message that arrives while a turn is in flight is steered
	// into the running turn (or queued for query-only transports); a message
	// that arrives idle starts a fresh (warm-reused or --resumed) turn.
	// Guarded by turnInFlightMu. See coding_session.go.
	turnInFlight   bool
	turnInFlightMu sync.Mutex

	// Cursor CLI session ID for native --resume on subsequent turns.
	// Populated by the structured adapter from cursor's stream-json init
	// event (and by the interactive adapter from its sqlite agentId after
	// each turn — best-effort), so a restored chat can pick up cursor's
	// native chat memory instead of starting fresh.
	cursorSessionID string

	// CursorBridgeToolsMode marks a chat as preferring MCP bridge tools.
	// Retained for API compatibility; no longer sets --mode ask (that mode
	// refuses natural-language writes with "Switch to Agent mode" and breaks
	// chat). Cursor runs in default agent mode regardless of this flag.
	cursorBridgeToolsMode bool

	// CodingAgentTransport is THE explicit transport choice for coding-agent
	// CLI providers: llm.CodingAgentTransportTmux or
	// llm.CodingAgentTransportStructured. Empty means "use the provider
	// contract's declared transport" (tmux for every current CLI provider).
	//
	// Transport is a property of the USE CASE, not of the provider — every
	// coding CLI supports both. Pick tmux when a human may steer the turn
	// live or the pane is shown; pick structured for unattended one-shot work
	// (workflow steps, background agents) where explicit completion/usage
	// events beat scraping a terminal.
	//
	// Set via withCodingAgentTransport — the single source of truth, resolved
	// by wantsStructuredTransport. Replaced the older overlapping mechanisms
	// (ForceStructuredCodingAgent + four per-provider *StructuredTransport
	// flags), which are gone.
	codingAgentTransport llm.CodingAgentTransport

	// Context offloading: handles offloading large tool outputs to filesystem
	toolOutputHandler *ToolOutputHandler

	// Context offloading configuration: enables virtual tools for accessing offloaded outputs
	enableContextOffloading bool

	// Context offloading threshold: custom threshold for when to offload tool outputs (0 = use default)
	largeOutputThreshold int

	// Tool output cleanup configuration
	toolOutputRetentionPeriod     time.Duration // How long to keep tool output files (0 = use default, default: 7 days)
	cleanupToolOutputOnSessionEnd bool          // Whether to clean up current session folder on session end
	cleanupMu                     sync.Mutex    // Protects cleanup routine lifecycle fields
	cleanupTicker                 *time.Ticker  // Ticker for periodic cleanup of old tool output files
	cleanupDone                   chan struct{} // Closed to signal the cleanup routine to stop

	// Context summarization configuration (see context_summarization.go)
	enableContextSummarization     bool    // Enable context summarization feature
	summaryKeepLastMessages        int     // Number of recent messages to keep when summarizing (0 = use default)
	summarizeOnTokenThreshold      bool    // Enable token-based summarization trigger (percentage-based)
	tokenThresholdPercent          float64 // Percentage of context window to trigger summarization (0.0-1.0, default: 0.8 = 80%)
	summarizeOnFixedTokenThreshold bool    // Enable fixed token-based summarization trigger
	fixedTokenThreshold            int     // Fixed token threshold to trigger summarization (e.g., 200000 = 200k tokens)
	summarizationCooldownTurns     int     // Number of turns to wait after summarization before allowing another (0 = use default: 3)
	lastSummarizationTurn          int     // Track when last summarization occurred (turn number)

	// Context editing configuration (see context_editing.go)
	enableContextEditing        bool // Enable context editing (dynamic context reduction)
	contextEditingThreshold     int  // Token threshold for context editing (0 = use default: 1000)
	contextEditingTurnThreshold int  // Turn age threshold for context editing (0 = use default: 10)

	// Parallel tool execution configuration
	// When enabled and LLM returns multiple tool calls in a single response,
	// tool calls execute concurrently using goroutines (fork-join pattern).
	// Results are collected in deterministic order matching the original tool call order.
	// When disabled (default): tool calls execute sequentially as before.
	enableParallelToolExecution bool

	// Mutex for concurrent access to Clients map during parallel tool execution
	// Used by broken pipe recovery to safely read/write the Clients map
	clientsMu sync.RWMutex

	// Mutex for concurrent access to event hierarchy state during parallel tool execution
	// Protects currentParentEventID and currentHierarchyLevel in EmitTypedEvent
	eventMu sync.Mutex

	// Steer messages: user messages injected mid-execution between tool results and next LLM call.
	// Written by HTTP handler (AddSteerMessage), read by agent loop (DrainSteerMessages).
	pendingSteerMessages []string
	steerMu              sync.Mutex

	// Tool call log: accumulated tool call entries for prompt logging.
	// Populated by EmitTypedEvent for tool_call_start/end events (works for ALL providers
	// including coding-agent CLIs where tool calls happen inside the CLI).
	// Cleared at start of AskWithHistory, dumped by logConversationEnd.
	toolCallLog   []string
	toolCallLogMu sync.Mutex

	// Dynamic tool allow list: when non-nil, only tools whose names appear in this set
	// are included in filteredTools (and the code-exec tool index). Updated per-turn via
	// SetToolAccess lets the workshop builder restrict tools
	// based on the current mode (build/optimize/debug/run/eval/output).
	toolAllowList   map[string]bool // nil = no restriction (all tools allowed)
	toolAllowListMu sync.RWMutex

	// Store prompts and resources for system prompt rebuilding
	prompts   map[string][]mcp.Prompt
	resources map[string][]mcp.Resource

	// Flag to track if a custom system prompt was provided
	hasCustomSystemPrompt bool

	// bridgeRoutingInstructionsOverride replaces the default per-provider
	// bridge-tool-routing preamble + bridgeRoutingExplicitInstructions text
	// (appended for every CLI coding-agent provider: Claude Code, Codex,
	// Cursor, Pi) when set via withBridgeRoutingInstructions. nil means
	// use the default for whichever provider this agent runs; a pointer to
	// "" suppresses the block entirely for this agent.
	bridgeRoutingInstructionsOverride *string

	// conversationSink, when set via withConversationSink, receives one
	// convrecord.TurnRecord per completed LLM call. nil (the default) means
	// no persistence happens at all.
	conversationSink convrecord.Sink
	// lastToolCallRecordedAt tracks the most recent tool-call CompletedAt this
	// agent has already included in a TurnRecord, so repeated non-destructive
	// toolcalllog.Snapshot reads (required — GetAndClear would break
	// agent_go's own cancellation-recovery reader of the same shared
	// in-process registry) don't re-emit the same calls turn after turn.
	lastToolCallRecordedAt time.Time

	// toolRegistry is the sole name-keyed source for tool identity, schema,
	// direct execution, timeout, and display metadata. Provider-facing tool
	// slices and code-execution session maps are derived runtime projections.
	toolRegistry        *canonicalToolRegistry
	canonicalRegistryMu sync.Mutex

	// additionalBridgeTools are custom tool names exposed as NATIVE MCP bridge
	// tools for THIS agent instance only, on top of the small fixed set in
	// bridgeTools (execute_shell_command, diff_patch_workspace_file,
	// agent_browser, get_api_spec). Set via withAdditionalBridgeTools —
	// callers must NOT edit the shared package-level bridgeTools var to add
	// their own tools, since that list is global across every consumer of
	// this module (see docs/core/mcp_bridge_layer.md and
	// TestBridgeToolsList, which pins bridgeTools to exactly those 4 entries).
	additionalBridgeTools []string

	// bridgeReadyFile is the per-launch path the mcpbridge subprocess creates
	// once the CLI completes its tools/list handshake (the tools-connected
	// marker). BuildBridgeMCPConfig allocates a fresh unique path each call and
	// stores it here; the coding-agent option builders read it immediately after
	// and hand it to the adapter via WithMCPReadyFile so a cold session holds its
	// first prompt until the tools are connected. A fresh unique temp path per
	// call guarantees a stale marker from a prior session can never satisfy the
	// gate. Empty when no bridge is in use.
	bridgeReadyFile string

	// toolArgTransformers maps tool names to functions that mutate their arguments in-place
	// before execution. This is the PRIMARY interception point — agent-internal tool calls
	// go through conversation.go (not the HTTP handler), so transformers must live here.
	// Registered via SetToolArgTransformer(), applied in conversation.go before all execution
	// branches (virtual tools, custom tools, MCP tools via session/codeexec/mcpcache).
	toolArgTransformers map[string]func(args map[string]interface{})

	// Custom logger (optional) - uses v2.Logger interface
	logger loggerv2.Logger

	// Listeners for typed events
	listeners []AgentEventListener
	mu        sync.RWMutex

	// Pre-filtered tool set used for the outgoing LLM call. Updated by
	// request-scoped allow-list filters and otherwise mirrors the registered tools.
	filteredTools []llmtypes.Tool

	// Track appended system prompts so callers can rebuild the final
	// prompt after SetInstructions replaces the base.
	appendedSystemPrompts []string // Supplementary instructions, composed at request time
	hasAppendedPrompts    bool     // Flag to indicate if any prompts were appended

	// Skills attached to this agent. Skills are Anthropic-format SKILL.md
	// bundles (folder = one skill) that adapters project to provider-native
	// locations (.claude/skills/, .agents/skills/, etc.) at session launch.
	// API transports surface skills via the system prompt listing plus the
	// intrinsic read_skill tool instead of disk projection. See agent/skill.go for the attachment
	// methods (AttachSkill / AttachedSkills / ClearSkills); the Skill
	// value type lives in llmtypes so adapters can reference it without
	// importing mcpagent.
	attachedSkills []*llmtypes.Skill
	// read_skill is intrinsic to attached skill identity. These flags reserve
	// its model-facing name from caller tools while allowing the internal
	// construction path to register it through the normal direct-tool runtime.
	skillReaderInstalled  bool
	installingSkillReader bool

	// Hierarchy tracking fields for event tree structure
	currentParentEventID  string // Track current parent event ID
	currentHierarchyLevel int    // Track current hierarchy level (0=root, 1=child, etc.)

	// Resource discovery configuration
	discoverResource bool // If true, include resource details in system prompt (default: true)

	// Prompt discovery configuration
	discoverPrompt bool // If true, include prompt details in system prompt (default: true)

	// Code execution mode configuration
	// When enabled: Custom tools + get_api_spec virtual tool are exposed to the LLM
	// MCP server tools are accessed via HTTP API (documented in OpenAPI specs from get_api_spec)
	// When disabled (default): All MCP tools are added directly as LLM tools
	useCodeExecutionMode bool

	// Cache configuration
	// When enabled: Skips cache lookup and always performs fresh connections
	// When disabled (default): Uses cache to speed up connection establishment (60-85% faster)
	disableCache bool

	// Runtime MCP configuration overrides
	// Allows workflow-specific modifications to server configs (e.g., output directories)
	runtimeOverrides mcpclient.RuntimeOverrides

	// Session-scoped connection management
	// When set: Connections are stored in SessionConnectionRegistry and shared across agents with same SessionID
	//           Agent.Close() does NOT close connections - call CloseSession(sessionID) at workflow end
	// Constructors normalize an empty value to "global".
	sessionID string

	// PromptLogLabel is an optional label used in prompt log filenames to identify
	// the agent type (e.g. "workflow-builder", "step-execution", "learning", "todo-task").
	// Set by the orchestrator before execution. If empty, derived from system prompt header.
	promptLogLabel string

	// API configuration for code execution mode
	// When set, code execution subprocesses receive these as MCP_API_URL and MCP_API_TOKEN env vars
	apiBaseURL string
	apiToken   string

	// Cached OpenAPI specs per server (generated on-demand by get_api_spec)
	openAPISpecCache   map[string][]byte
	openAPISpecCacheMu sync.RWMutex

	// All MCP tool definitions (stored in code execution mode for OpenAPI spec generation)
	// In code execution mode, MCP tools are excluded from a.Tools (accessed via HTTP API),
	// but their definitions are needed by handleGetAPISpec to generate OpenAPI specs.
	allMCPToolDefs []llmtypes.Tool

	// User ID for per-user OAuth token isolation
	// When set: OAuth tokens are stored per-user at ~/.config/mcpagent/tokens/{UserID}/{serverName}.json
	// When empty: OAuth tokens use the default path from MCP config
	userID string

	// Streaming configuration
	// EnableStreaming consumes provider stream chunks. SuppressGenerationStreamingEvents
	// controls whether those chunks are hidden from streaming_start/chunk/end events.
	enableStreaming                   bool                             // Enable provider streaming (default: false)
	suppressGenerationStreamingEvents bool                             // Suppress generation streaming events (default: false)
	streamingCallback                 func(chunk llmtypes.StreamChunk) // Optional callback for streaming chunks

	// Folder guard paths for code execution mode
	// These paths are validated at AST level before code execution
	folderGuardReadPaths  []string // Paths allowed for read operations
	folderGuardWritePaths []string // Paths allowed for write operations

	// API keys for providers (used for fallback LLM creation)
	apiKeys *AgentAPIKeys

	// Cumulative token tracking for entire conversation
	cumulativePromptTokens     int          // Cumulative prompt/input tokens
	cumulativeCompletionTokens int          // Cumulative completion/output tokens
	cumulativeTotalTokens      int          // Cumulative total tokens
	cumulativeCacheTokens      int          // Cumulative cache tokens (sum of all cache-related tokens)
	cumulativeReasoningTokens  int          // Cumulative reasoning tokens (for models like o3)
	cumulativeCacheDiscount    float64      // Sum of cache discounts (for averaging)
	llmCallCount               int          // Number of LLM calls made
	cacheEnabledCallCount      int          // Number of calls with cache tokens > 0
	tokenTrackingMutex         sync.RWMutex // Mutex for thread-safe token accumulation

	// Cumulative pricing tracking for entire conversation
	cumulativeInputCost     float64 // Cumulative cost for input tokens (in USD)
	cumulativeOutputCost    float64 // Cumulative cost for output tokens (in USD)
	cumulativeReasoningCost float64 // Cumulative cost for reasoning tokens (in USD)
	cumulativeCacheCost     float64 // Cumulative cost for cached input tokens (in USD)
	cumulativeTotalCost     float64 // Total cumulative cost (in USD)
	lastTurnCost            float64 // Cost of the most recent single turn only (in USD) — see recordConversationTurn

	// Context window usage tracking
	// currentContextWindowUsage represents the actual tokens currently in the context window.
	// This is reset after summarization to reflect only the tokens in the current context
	// (system + summary + recent messages), and is used for percentage calculation.
	// Note: This is separate from cumulativePromptTokens which is truly cumulative across
	// all conversation phases (never reset) for accurate pricing and overall usage reporting.
	// Context window is based on input tokens only, not output tokens.
	currentContextWindowUsage int
	modelContextWindow        int // Cached model context window size (0 = not cached yet)

	// LLM Configuration
	llmConfig AgentLLMConfiguration

	// quotaExhaustedModels tracks models that hit permanent quota exhaustion (daily/monthly limits).
	// These are skipped on subsequent turns to avoid wasted API calls.
	// Key: "provider/model_id"
	quotaExhaustedModels map[string]bool
}

// LLMModel represents a single LLM configuration
type LLMModel struct {
	Provider string `json:"provider"` // "anthropic", "openai", "bedrock", etc.
	ModelID  string `json:"model_id"` // "claude-sonnet-4.5", "gpt-5", etc.

	// Auth per model
	APIKey *string `json:"api_key,omitempty"` // For OpenRouter, OpenAI, Anthropic, Vertex
	Region *string `json:"region,omitempty"`  // For Bedrock

	// Model-specific options
	Temperature *float64               `json:"temperature,omitempty"` // Override default temperature (0.0-1.0)
	Options     map[string]interface{} `json:"options,omitempty"`     // Provider-specific options (reasoning_effort, thinking_level, etc.)
}

// AgentLLMConfiguration holds the primary and fallback LLM configurations
type AgentLLMConfiguration struct {
	Primary   LLMModel   `json:"primary"`
	Fallbacks []LLMModel `json:"fallbacks"`
}

// AgentAPIKeys is an alias for llm.ProviderAPIKeys (canonical type).
// Add new provider fields to the canonical struct, not here.
type AgentAPIKeys = llm.ProviderAPIKeys

// AgentBedrockConfig is an alias for llm.BedrockConfig (canonical type).
type AgentBedrockConfig = llm.BedrockConfig

// AgentAzureConfig is an alias for llm.AzureAPIConfig (canonical type).
type AgentAzureConfig = llm.AzureAPIConfig

// AddSteerMessage queues a user message to be injected into the conversation
// between tool results and the next LLM call. Thread-safe — called by HTTP handlers.
func (a *Agent) addSteerMessage(msg string) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	a.pendingSteerMessages = append(a.pendingSteerMessages, msg)
}

// DrainSteerMessages returns and clears all pending steer messages.
// Thread-safe — called by the agent loop in conversation.go.
func (a *Agent) drainSteerMessages() []string {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if len(a.pendingSteerMessages) == 0 {
		return nil
	}
	msgs := a.pendingSteerMessages
	a.pendingSteerMessages = nil
	return msgs
}

// GetProvider returns the provider
func (a *Agent) getProvider() llm.Provider {
	return a.provider
}

// GetToolOutputHandler returns the tool output handler
func (a *Agent) getToolOutputHandler() *ToolOutputHandler {
	return a.toolOutputHandler
}

// GetLLMModelConfig returns the agent's primary LLM configuration as an LLMModel.
// If LLMConfig.Primary is set (via withLLMConfig), it's returned directly.
// Otherwise, constructs one from the legacy provider/ModelID/APIKeys fields.
func (a *Agent) getLLMModelConfig() LLMModel {
	if a.llmConfig.Primary.Provider != "" {
		return a.llmConfig.Primary
	}
	config := LLMModel{
		Provider: string(a.provider),
		ModelID:  a.modelID,
	}
	if a.apiKeys != nil {
		switch a.provider {
		case llm.ProviderAnthropic:
			config.APIKey = a.apiKeys.Anthropic
		case llm.ProviderOpenAI:
			config.APIKey = a.apiKeys.OpenAI
		case llm.ProviderOpenRouter:
			config.APIKey = a.apiKeys.OpenRouter
		case llm.ProviderVertex:
			config.APIKey = a.apiKeys.Vertex
		case llm.ProviderZAI:
			config.APIKey = a.apiKeys.ZAI
		case llm.ProviderKimi:
			config.APIKey = a.apiKeys.Kimi
		case llm.ProviderCodexCLI:
			config.APIKey = a.apiKeys.CodexCLI
		case llm.ProviderCursorCLI:
			config.APIKey = a.apiKeys.CursorCLI
		case llm.ProviderPiCLI:
			config.APIKey = a.apiKeys.PiCLI
		case llm.ProviderMiniMax:
			config.APIKey = a.apiKeys.MiniMax
		case llm.ProviderMiniMaxCodingPlan:
			config.APIKey = a.apiKeys.MiniMaxCodingPlan
		}
	}
	return config
}

// SetFolderGuardPaths sets the folder guard paths for code execution validation
// readPaths: paths allowed for read operations (workspace package read functions)
// writePaths: paths allowed for write operations (workspace package write functions)
func (a *Agent) setFolderGuardPaths(readPaths, writePaths []string) {
	a.folderGuardReadPaths = readPaths
	a.folderGuardWritePaths = writePaths
	if a.logger != nil {
		a.logger.Info("🔒 [CODE_EXECUTION] Folder guard paths set",
			loggerv2.Any("read_paths", readPaths),
			loggerv2.Any("write_paths", writePaths))
	}
}

// extractModelIDFromLLM extracts the model ID from the LLM instance
// Returns the model ID from llm.GetModelID(), or "unknown" if empty
//
// GetModelID() is now part of the llmtypes.Model interface, so all implementations
// must provide it. This makes the extraction straightforward and type-safe.
func extractModelIDFromLLM(llm llmtypes.Model) string {
	modelID := llm.GetModelID()
	if modelID == "" {
		return "unknown"
	}
	return modelID
}

// extractProviderFromLLM extracts the provider from the LLM instance
// Checks if the LLM implements GetProvider() method
func extractProviderFromLLM(model llmtypes.Model) llm.Provider {
	// Check if model implements GetProvider()
	if p, ok := model.(interface{ GetProvider() llm.Provider }); ok {
		return p.GetProvider()
	}
	return ""
}

// extractAPIKeysFromLLM extracts the API keys from the LLM instance
// Checks if the LLM implements GetAPIKeys() method (e.g., ProviderAwareLLM)
// This allows the agent to automatically use keys passed when creating the LLM
func extractAPIKeysFromLLM(model llmtypes.Model) *AgentAPIKeys {
	// Check if model implements GetAPIKeys()
	if p, ok := model.(interface{ GetAPIKeys() *llm.ProviderAPIKeys }); ok {
		providerKeys := p.GetAPIKeys()
		if providerKeys == nil {
			return nil
		}
		// Convert llm.ProviderAPIKeys to AgentAPIKeys
		agentKeys := &AgentAPIKeys{
			OpenRouter:           providerKeys.OpenRouter,
			OpenAI:               providerKeys.OpenAI,
			Anthropic:            providerKeys.Anthropic,
			ClaudeCodeOAuthToken: providerKeys.ClaudeCodeOAuthToken,
			ZAI:                  providerKeys.ZAI,
			Kimi:                 providerKeys.Kimi,
			Vertex:               providerKeys.Vertex,
			CodexCLI:             providerKeys.CodexCLI,
			PiCLI:                providerKeys.PiCLI,
			MiniMax:              providerKeys.MiniMax,
			MiniMaxCodingPlan:    providerKeys.MiniMaxCodingPlan,
		}
		if providerKeys.Bedrock != nil {
			agentKeys.Bedrock = &AgentBedrockConfig{
				Region: providerKeys.Bedrock.Region,
			}
		}
		if providerKeys.Azure != nil {
			agentKeys.Azure = &AgentAzureConfig{
				Endpoint:   providerKeys.Azure.Endpoint,
				APIKey:     providerKeys.Azure.APIKey,
				APIVersion: providerKeys.Azure.APIVersion,
				Region:     providerKeys.Azure.Region,
			}
		}
		return agentKeys
	}
	return nil
}

// newAgent creates a new Agent instance with the provided configuration.
//
// It initializes the agent with the given context, LLM model, and MCP configuration path.
// Additional behavior can be configured using agentOption functions.
//
// Parameters:
//   - ctx: The base context for the agent's lifecycle.
//   - llm: The LLM provider implementation (must implement llmtypes.Model).
//   - configPath: Path to the MCP configuration file (e.g., mcp_config.json).
//   - options: Variadic list of agentOption functions to configure the agent.
//
// Returns:
//   - *Agent: A pointer to the initialized Agent.
//   - error: An error if initialization fails (e.g., LLM is nil, config load fails).
//
// By default, the agent connects to all servers defined in the config. Use withServerName() option to filter.
func newAgent(ctx context.Context, llm llmtypes.Model, configPath string, options ...agentOption) (*Agent, error) {
	if llm == nil {
		return nil, fmt.Errorf("LLM cannot be nil")
	}

	// Extract model ID from LLM instance
	// This ensures the modelID matches the actual LLM being used
	modelID := extractModelIDFromLLM(llm)

	// Create agent with default values
	ag := &Agent{
		ctx:                           ctx,
		llmModel:                      llm,
		tracers:                       []observability.Tracer{}, // Default: empty tracers array
		maxTurns:                      GetDefaultMaxTurns(),
		temperature:                   0.0,    // Default temperature
		toolChoice:                    "auto", // Default tool choice
		modelID:                       modelID,
		agentMode:                     SimpleAgent,                      // Default to simple mode
		traceID:                       "",                               // Default: empty trace ID
		provider:                      "",                               // Will be set by caller or extracted
		enableContextOffloading:       true,                             // Default to enabled
		largeOutputThreshold:          0,                                // Default: 0 means use default threshold (10000)
		toolOutputRetentionPeriod:     DefaultToolOutputRetentionPeriod, // Default: 7 days
		cleanupToolOutputOnSessionEnd: false,                            // Default: false means files persist after session
		enableContextSummarization:    false,                            // Default to disabled
		summarizeOnTokenThreshold:     false,                            // Default to disabled
		tokenThresholdPercent:         0.8,                              // Default to 80% if enabled
		summaryKeepLastMessages:       0,                                // Default: 0 means use default (4 messages)
		summarizationCooldownTurns:    0,                                // Default: 0 means use default (3 turns)
		lastSummarizationTurn:         -1,                               // Default: -1 means never summarized
		enableContextEditing:          false,                            // Default to disabled
		contextEditingThreshold:       0,                                // Default: 0 means use default threshold (1000)
		contextEditingTurnThreshold:   0,                                // Default: 0 means use default (10 turns)
		logger:                        loggerv2.NewDefault(),            // Default logger

		// Initialize hierarchy tracking fields
		currentParentEventID:  "", // Start with no parent
		currentHierarchyLevel: 0,  // Start at root level

		// Initialize resource discovery (default: true - include resources in system prompt)
		discoverResource: true,

		// Initialize prompt discovery (default: true - include prompts in system prompt)
		discoverPrompt: true,

		// Initialize cache (default: false - caching enabled by default)
		disableCache: false,

		// Initialize streaming (default: provider streaming disabled, event emission enabled if streaming is turned on)
		enableStreaming:                   false,
		suppressGenerationStreamingEvents: false,
		streamingCallback:                 nil,

		// Initialize server name (default: AllServers - connect to all servers)
		serverName: mcpclient.AllServers,
	}

	// Apply all options
	for _, option := range options {
		option(ag)
	}

	// If provider is not set, try to extract it from LLM
	if ag.provider == "" {
		ag.provider = extractProviderFromLLM(llm)
	}

	// Extract API keys from LLM if available
	// This allows users to pass keys only when creating the LLM
	if ag.apiKeys == nil {
		ag.apiKeys = extractAPIKeysFromLLM(llm)
	}

	// Use logger from options (or default if not set)
	logger := ag.logger
	if logger == nil {
		logger = loggerv2.NewDefault()
		ag.logger = logger
	}

	// Use serverName from options (or default AllServers)
	serverName := ag.serverName
	if serverName == "" {
		serverName = mcpclient.AllServers
	}

	// Initialize TraceID if not set (prevent empty folder collisions)
	if ag.traceID == "" {
		ag.traceID = observability.TraceID(uuid.New().String())
	}

	logger.Info("🔍 [DEBUG] newAgent: Starting initialization", loggerv2.String("config_path", configPath), loggerv2.String("server_name", serverName))
	logger.Info("newAgent started", loggerv2.String("config_path", configPath))
	logger.Info("newAgent initialization", loggerv2.String("server_name", serverName), loggerv2.String("config_path", configPath))

	// Load merged MCP servers configuration (base + user)
	logger.Info("🔍 [DEBUG] newAgent: About to load merged MCP config", loggerv2.String("config_path", configPath))
	configLoadStartTime := time.Now()
	config, err := mcpclient.LoadMergedConfig(configPath, logger)
	configLoadDuration := time.Since(configLoadStartTime)
	if err != nil {
		logger.Error("❌ [DEBUG] newAgent: Failed to load merged MCP config", err, loggerv2.String("duration", configLoadDuration.String()))
		return nil, fmt.Errorf("failed to load merged MCP config: %w", err)
	}
	logger.Info("✅ [DEBUG] newAgent: Merged MCP config loaded successfully", loggerv2.String("duration", configLoadDuration.String()), loggerv2.Int("server_count", len(config.MCPServers)))

	logger.Debug("Merged config contains servers", loggerv2.Int("server_count", len(config.MCPServers)))
	for name := range config.MCPServers {
		logger.Debug("Server found", loggerv2.String("server_name", name))
	}

	if modelID == "unknown" {
		logger.Warn("Could not extract model ID from LLM instance, using 'unknown'",
			loggerv2.String("fallback", "unknown"))
	}

	logger.Info("🔍 [DEBUG] newAgent: About to call NewAgentConnectionWithSession", loggerv2.String("server_name", serverName), loggerv2.String("config_path", configPath), loggerv2.Any("disable_cache", ag.disableCache), loggerv2.String("session_id", ag.sessionID))
	connectionStartTime := time.Now()

	// Check if session-scoped connection management is enabled
	var clients map[string]mcpclient.ClientInterface
	var toolToServer map[string]string
	var allLLMTools []llmtypes.Tool
	var servers []string
	var prompts map[string][]mcp.Prompt
	var resources map[string][]mcp.Resource
	var systemPrompt string

	// SessionID is mandatory for connection management via the session registry.
	// Default to "global" if not set, so all agents share connections and we never
	// fall into the legacy path that spawns fresh subprocesses on every call.
	if ag.sessionID == "" {
		ag.sessionID = "global"
		logger.Warn("SessionID not set — defaulting to 'global' for shared connection management")
	}

	logger.Info("Using session-scoped connection management", loggerv2.String("session_id", ag.sessionID))
	clients, toolToServer, allLLMTools, servers, prompts, resources, systemPrompt, err =
		NewAgentConnectionWithSession(ctx, llm, serverName, configPath, ag.sessionID, string(ag.traceID), ag.tracers, logger, ag.disableCache, ag.runtimeOverrides, ag.userID)

	connectionDuration := time.Since(connectionStartTime)
	if err != nil {
		logger.Error("❌ [DEBUG] newAgent: NewAgentConnectionWithSession failed", err, loggerv2.String("duration", connectionDuration.String()), loggerv2.String("server_name", serverName))
		return nil, err
	}
	logger.Info("✅ [DEBUG] newAgent: NewAgentConnectionWithSession completed successfully", loggerv2.String("duration", connectionDuration.String()), loggerv2.Int("clients_count", len(clients)), loggerv2.Int("tools_count", len(allLLMTools)), loggerv2.Int("servers_count", len(servers)), loggerv2.String("session_id", ag.sessionID))

	// Initialize tool output handler
	toolOutputHandler := NewToolOutputHandler()

	// Apply custom threshold if set via withLargeOutputThreshold option
	if ag.largeOutputThreshold > 0 {
		toolOutputHandler.SetThreshold(ag.largeOutputThreshold)
		logger.Info("Context offloading threshold set", loggerv2.Int("threshold", ag.largeOutputThreshold))
	}

	// Large output handling is now done via virtual tools, not MCP server
	// Virtual tools are enabled by default and handle file operations directly
	toolOutputHandler.SetServerAvailable(true) // Always available with virtual tools

	// Set session ID for organizing files by conversation
	toolOutputHandler.SetSessionID(string(ag.traceID))

	// Set LLM for provider-aware token counting
	toolOutputHandler.SetLLM(llm)

	// Update the existing agent with connection data
	ag.clients = clients
	ag.toolToServer = toolToServer
	if err := ag.initializeCanonicalToolRegistry(allLLMTools, toolToServer); err != nil {
		return nil, fmt.Errorf("initialize canonical tool registry: %w", err)
	}
	// Only take the connection-derived default system prompt when the caller
	// didn't supply one via withSystemPrompt/AddInstructions. Unconditionally
	// overwriting here clobbered a caller's custom prompt (set by an agentOption
	// applied earlier in this same constructor, before this connection setup
	// runs) with whatever NewAgentConnectionWithSession computed on its own —
	// confirmed live: ag.systemPrompt held the real ~14k-char custom prompt
	// right after withSystemPrompt ran, then dropped to just the much shorter
	// bridge-routing preamble by the time the LLM call was built, because this
	// line reset it in between with no guard. Mirrors the same
	// hasCustomSystemPrompt check already used at the BuildSystemPromptWithoutTools
	// call site below.
	if !ag.hasCustomSystemPrompt {
		ag.systemPrompt = systemPrompt
	}
	ag.servers = servers
	ag.toolOutputHandler = toolOutputHandler
	ag.prompts = prompts
	ag.resources = resources
	ag.configPath = configPath

	// Start periodic cleanup routine for tool output files
	ag.startCleanupRoutine()

	// In code execution mode, OpenAPI specs are generated on-demand per server
	// when the LLM calls get_api_spec(server_name=...) — no upfront code generation needed
	if ag.useCodeExecutionMode {
		ag.openAPISpecCache = make(map[string][]byte)
		logger.Debug("Code execution mode: OpenAPI specs will be generated on-demand via get_api_spec")
	}

	// Set selectedServers based on serverName parameter if not already set via options
	// This ensures discover_code_structure filters correctly when a single server is specified
	// IMPORTANT: Only auto-assign selectedServers if BOTH selectedServers AND selectedTools are empty
	// If selectedTools is set, the user wants specific tool filtering, not all tools from the server
	if len(ag.selectedServers) == 0 && len(ag.selectedTools) == 0 && serverName != "" && serverName != "all" {
		// serverName was specified and no filtering was configured via options
		// Use the servers list from the session-scoped connection setup (already filtered by serverName).
		ag.selectedServers = servers
		logger.Debug("Set selectedServers from serverName parameter",
			loggerv2.Any("selected_servers", ag.selectedServers))
	} else if len(ag.selectedServers) == 0 && len(ag.selectedTools) > 0 {
		// selectedTools is set but selectedServers is not - respect the specific tool filtering
		logger.Debug("Using selectedTools for filtering, not auto-assigning selectedServers",
			loggerv2.Any("selected_tools", ag.selectedTools))
	}

	// Create unified ToolFilter for consistent filtering across both modes
	// This filter is used by both LLM tool registration and discovery
	customCategories := ag.getCustomToolCategories()
	ag.toolFilter = NewToolFilter(
		ag.selectedTools,
		ag.selectedServers,
		clients,
		customCategories,
		logger,
	)

	// Pre-detect coding CLI providers to set code execution mode BEFORE MCP tool filtering.
	// CLI providers always need code execution mode (tools accessed via HTTP bridge).
	// Without this, allMCPToolDefs remains empty and get_api_spec cannot resolve tools.
	if isCodingCLIBridgeProvider(ag.provider, ag.modelID) {
		if !ag.useCodeExecutionMode {
			ag.useCodeExecutionMode = true
			logger.Debug("[BRIDGE_DEBUG] Pre-set UseCodeExecutionMode for CLI provider before MCP tool filtering",
				loggerv2.String("provider", string(ag.provider)))
		}
	}

	// Handle code execution mode: filter out MCP and custom tools (both accessed via HTTP API)
	var toolsToUse []llmtypes.Tool
	if ag.useCodeExecutionMode {
		// Code execution mode: Only virtual tools as direct LLM calls
		// Exclude both MCP server tools and custom tools (they're accessed via HTTP endpoints documented in OpenAPI spec)
		logger.Debug("Code execution mode enabled - excluding MCP and custom tools from LLM (accessed via HTTP API)")

		for _, tool := range allLLMTools {
			if tool.Function == nil {
				continue
			}
			// Check if this tool is an MCP tool (exists in toolToServer and NOT custom)
			serverName, isMCPTool := toolToServer[tool.Function.Name]

			// In code execution mode, exclude both MCP server tools and custom tools
			// They're all accessed via HTTP API endpoints documented in OpenAPI spec
			// Keep only virtual tools (get_api_spec will be filtered in later)
			if isMCPTool && serverName != "custom" {
				// Store MCP tool definitions for OpenAPI spec generation
				ag.allMCPToolDefs = append(ag.allMCPToolDefs, tool)
				continue // Skip MCP server tools — they're behind the HTTP API
			}
			if isMCPTool && serverName == "custom" {
				// System-category custom tools (execute_shell_command, workspace tools) stay as direct LLM calls
				// Non-system custom tools (e.g. get_weather) go through HTTP API
				if direct, exists := ag.lookupDirectTool(tool.Function.Name); exists && ag.toolFilter.IsSystemCategory(direct.DisplayGroup) {
					toolsToUse = append(toolsToUse, tool)
				}
				continue
			}
			toolsToUse = append(toolsToUse, tool)
		}
		logger.Debug("Code execution mode: tools available (virtual only, MCP + custom excluded)",
			loggerv2.Int("tool_count", len(toolsToUse)),
			loggerv2.Int("mcp_tool_defs_stored", len(ag.allMCPToolDefs)))
	} else {
		// Normal mode: Use all tools
		toolsToUse = allLLMTools
	}

	ag.tools = toolsToUse
	ag.filteredTools = toolsToUse

	// Apply selected tools filter using unified ToolFilter
	// This ensures consistent filtering between LLM tools and discovery
	// Empty selectedTools/selectedServers means "use all tools" (no filtering)
	// Non-empty means "use only matching tools"
	// Also supports "server:*" pattern to explicitly request all tools from a server
	if !ag.toolFilter.IsNoFilteringActive() {
		logger.Debug("Tool filtering active",
			loggerv2.Int("selected_tools", len(ag.selectedTools)),
			loggerv2.Int("selected_servers", len(ag.selectedServers)))

		// Build set of custom tool names for category determination
		customToolNames := make(map[string]bool)
		for _, directTool := range ag.directToolSnapshot() {
			customToolNames[directTool.Name] = true
			// Also store category for this tool
			if directTool.DisplayGroup != "" {
				customToolNames[directTool.DisplayGroup+":"+directTool.Name] = true
			}
		}

		// Filter tools using unified ToolFilter
		var filteredTools []llmtypes.Tool
		for _, tool := range toolsToUse {
			if tool.Function == nil {
				continue
			}
			toolName := tool.Function.Name

			// Determine the package/server name and tool type
			serverName, isMCPTool := toolToServer[toolName]
			isCustomTool := customToolNames[toolName]

			// Determine package name for custom tools
			var packageName string
			if isMCPTool {
				packageName = serverName
			} else if isCustomTool {
				// Find the category for this custom tool
				if directTool, ok := ag.lookupDirectTool(toolName); ok && directTool.DisplayGroup != "" {
					packageName = directTool.DisplayGroup
				} else {
					packageName = "custom"
				}
			} else {
				// Virtual tool - always include
				filteredTools = append(filteredTools, tool)
				continue
			}

			// Use unified filter to check if tool should be included
			// Virtual tools are handled above, so isVirtualTool=false here
			if ag.toolFilter.ShouldIncludeTool(packageName, toolName, isCustomTool, false) {
				filteredTools = append(filteredTools, tool)
			}
		}

		logger.Debug("Tool filtering complete",
			loggerv2.Int("selected_tools", len(filteredTools)),
			loggerv2.Int("total_tools", len(toolsToUse)))
		ag.tools = filteredTools
		ag.filteredTools = filteredTools
	} else {
		// No filtering active - use all available tools (already filtered by code execution mode if enabled)
		logger.Debug("Using all available tools (no filtering applied)",
			loggerv2.Int("tool_count", len(toolsToUse)))
		ag.tools = toolsToUse
		ag.filteredTools = toolsToUse
	}

	// Initialize tool registry for code execution
	// Convert custom tools to executor functions
	customToolExecutors := ag.directToolExecutors()

	// Add virtual tools to the LLM tools list
	virtualTools := ag.createVirtualTools()

	// Safety net: Ensure CLI provider code-execution mode is active before virtual tool filtering.
	// The primary pre-detection is above (before MCP tool filtering at allMCPToolDefs).
	// This block is a safety net in case code is reordered in the future.
	if isCodingCLIBridgeProvider(ag.provider, ag.modelID) {
		if !ag.useCodeExecutionMode {
			ag.useCodeExecutionMode = true
			logger.Warn("[BRIDGE_DEBUG] CLI provider UseCodeExecutionMode was not pre-set — enforcing before virtual tool filtering (safety net)",
				loggerv2.String("provider", string(ag.provider)))
		}
	}

	// Filter virtual tools based on mode
	if ag.useCodeExecutionMode {
		// In code execution mode, only include get_api_spec
		var filteredVirtualTools []llmtypes.Tool
		for _, tool := range virtualTools {
			if tool.Function != nil {
				toolName := tool.Function.Name
				if toolName == "get_api_spec" {
					filteredVirtualTools = append(filteredVirtualTools, tool)
				}
			}
		}
		virtualTools = filteredVirtualTools
		logger.Debug("Code execution mode: virtual tools after filtering",
			loggerv2.Int("count", len(virtualTools)))
	} else {
		// In non-code execution mode, exclude get_api_spec
		var filteredVirtualTools []llmtypes.Tool
		for _, tool := range virtualTools {
			if tool.Function != nil {
				toolName := tool.Function.Name
				if toolName != "get_api_spec" {
					filteredVirtualTools = append(filteredVirtualTools, tool)
				}
			}
		}
		virtualTools = filteredVirtualTools
		logger.Debug("Non-code execution mode: Excluded get_api_spec from virtual tools")
	}

	ag.tools = append(ag.tools, virtualTools...)

	logger.Debug("[BRIDGE_DEBUG] Tools after virtual tools appended",
		loggerv2.Int("total_tools", len(ag.tools)),
		loggerv2.Int("virtual_tools_added", len(virtualTools)))

	// Convert virtual tools to executor functions
	// Note: We need to capture the tool name in the closure
	virtualToolExecutors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))
	for _, virtualTool := range virtualTools {
		if virtualTool.Function != nil {
			toolName := virtualTool.Function.Name
			// Create a closure that captures the tool name and agent reference
			virtualToolExecutors[toolName] = func(name string) func(ctx context.Context, args map[string]interface{}) (string, error) {
				return func(ctx context.Context, args map[string]interface{}) (string, error) {
					return ag.handleVirtualTool(ctx, name, args)
				}
			}(toolName)
		}
	}

	// Initialize registry with virtual tools
	codeexec.InitRegistryWithVirtualTools(ag.clients, customToolExecutors, virtualToolExecutors, ag.toolToServer, logger)

	// Also register session-scoped tools to prevent cross-workflow contamination
	if ag.sessionID != "" {
		codeexec.InitRegistryForSession(ag.sessionID, customToolExecutors, logger)
		logger.Info("✅ Session-scoped custom tools registered during initialization",
			loggerv2.String("session_id", ag.sessionID),
			loggerv2.Int("count", len(customToolExecutors)))

		virtualScopeID := ag.virtualToolScopeID()
		codeexec.InitRegistryVirtualToolsForSession(virtualScopeID, virtualToolExecutors, logger)
		logger.Info("✅ Session-scoped virtual tools registered during initialization (newAgent)",
			loggerv2.String("session_id", ag.sessionID),
			loggerv2.String("virtual_scope_id", virtualScopeID),
			loggerv2.Int("virtual_tool_count", len(virtualToolExecutors)),
			loggerv2.Int("custom_tool_count", len(ag.directToolSnapshot())),
			loggerv2.String("agent_ptr", fmt.Sprintf("%p", ag)))
	}

	// In code execution mode, build tool index from agent internal state
	var toolStructureJSON string
	if ag.useCodeExecutionMode {
		toolStructure, err := ag.buildToolIndex()
		if err != nil {
			logger.Warn("Failed to build tool index for system prompt", loggerv2.Error(err))
		} else {
			toolStructureJSON = toolStructure
		}
	}

	// Always rebuild system prompt with the correct agent mode and tool structure
	// This ensures Simple agents get Simple prompts and ReAct agents get ReAct prompts
	// In code execution mode, tool structure is automatically included
	if !ag.hasCustomSystemPrompt {
		ag.systemPrompt = prompt.BuildSystemPromptWithoutTools(ag.prompts, ag.resources, string(ag.agentMode), ag.discoverResource, ag.discoverPrompt, ag.useCodeExecutionMode, toolStructureJSON, ag.logger, ag.enableParallelToolExecution)
	}

	// Initialize the filtered-tool set used by the outgoing LLM call.
	// Conversation allow-list filtering may further trim this slice per turn;
	// until then it mirrors the registered tools.
	ag.filteredTools = ag.tools
	logger.Debug("Initialized filtered tool set",
		loggerv2.Int("tool_count", len(ag.tools)),
		loggerv2.Int("client_count", len(ag.clients)))

	// No more event listeners - events go directly to tracer
	// Langfuse tracing is handled by the tracer itself

	// 🔧 CLAUDE CODE INTEGRATION: Auto-disable incompatible features
	logger.Debug("Checking provider for auto-disable",
		loggerv2.String("current_provider", string(ag.provider)),
		loggerv2.String("claude_code_provider", string(llmproviders.ProviderClaudeCode)),
		loggerv2.Any("match", ag.provider == llmproviders.ProviderClaudeCode))

	if ag.provider == llmproviders.ProviderClaudeCode {
		ag.appendBridgeRoutingInstructions("CRITICAL INSTRUCTION: You are running within a restricted environment. Use only the tool names explicitly declared in the available tool list for this session. Do NOT invent alternate prefixes or namespaces. DO NOT use your built-in tools like `Bash`, `Read`, or `Write` as they are blocked and will fail. If an action is denied, blocked, unavailable, or returns a 404-like error, do not keep retrying the same approach; use another declared tool or stop and explain the blocker clearly.")
		logger.Debug("🔧 [CLAUDE_CODE] Provider detected - silently disabling incompatible features")

		// Code execution mode is pre-set before virtual tool filtering (see pre-detection
		// block above CreateVirtualTools). Enforce as safety net in case that block is
		// bypassed or reordered in the future.
		if !ag.useCodeExecutionMode {
			ag.useCodeExecutionMode = true
			logger.Warn("[BRIDGE_DEBUG] CLAUDE_CODE: UseCodeExecutionMode was not pre-set — enforcing now (safety net)")
		}

		if ag.enableContextEditing {
			ag.enableContextEditing = false
			logger.Debug("🔧 [CLAUDE_CODE] Disabled Context Editing (handled natively by CLI)")
		}

		if ag.enableContextSummarization {
			ag.enableContextSummarization = false
			logger.Debug("🔧 [CLAUDE_CODE] Disabled Context Summarization (handled natively by CLI)")
		}

		if ag.enableContextOffloading {
			ag.enableContextOffloading = false
			logger.Debug("🔧 [CLAUDE_CODE] Disabled Context Offloading (handled natively by CLI)")
		}

		// Auto-enable streaming — required for tool call observability events
		// (ToolCallStart/ToolCallEnd) since the CLI manages its own agentic loop
		if !ag.enableStreaming {
			ag.enableStreaming = true
			logger.Debug("🔧 [CLAUDE_CODE] Auto-enabled streaming (required for tool call observability)")
		}
	}

	// Auto-configure Codex CLI provider (same constraints as Claude Code)
	if ag.provider == llmproviders.ProviderCodexCLI {
		ag.appendBridgeRoutingInstructions("IMPORTANT: Do NOT use your built-in tools — only use the tools declared in this session. Do NOT use provider-native filesystem or shell tools. For filesystem access, use declared bridge tools such as execute_shell_command or diff_patch_workspace_file when available. If a tool call fails or is blocked, try a different declared tool or stop and explain.")
		logger.Debug("🔧 [CODEX_CLI] Provider detected - silently disabling incompatible features")

		if !ag.useCodeExecutionMode {
			ag.useCodeExecutionMode = true
			logger.Debug("🔧 [CODEX_CLI] Auto-enabled Code Execution Mode (CLI manages its own agentic loop)")
		}

		if ag.enableContextEditing {
			ag.enableContextEditing = false
			logger.Debug("🔧 [CODEX_CLI] Disabled Context Editing (handled natively by CLI)")
		}

		if ag.enableContextSummarization {
			ag.enableContextSummarization = false
			logger.Debug("🔧 [CODEX_CLI] Disabled Context Summarization (handled natively by CLI)")
		}

		if ag.enableContextOffloading {
			ag.enableContextOffloading = false
			logger.Debug("🔧 [CODEX_CLI] Disabled Context Offloading (handled natively by CLI)")
		}

		if !ag.enableStreaming {
			ag.enableStreaming = true
			logger.Debug("🔧 [CODEX_CLI] Auto-enabled streaming (required for tool call observability)")
		}
	}

	// Auto-configure Cursor CLI provider.
	//
	// The system prompt below steers cursor away from its built-in
	// editToolCall / shell tools toward the declared MCP bridge tools so
	// state-changing operations are observable (every write produces a
	// tool_call_start/end event the host can see). Empirically verified to
	// work in both transports against cursor-agent v2026.05.20-2b5dd59:
	//
	//   - tmux mode: TestCursorTmuxSystemPromptSteersWritesThroughBridge in
	//     multi-llm-provider-go/pkg/adapters/cursorcli — system prompt is
	//     delivered as a .cursor/rules/*.mdc with alwaysApply:true, which
	//     cursor honors as session-wide guidance.
	//   - structured mode: same nudge works, delivered as inline prefix on
	//     the user message.
	//
	// Cursor stays in default agent mode (no --mode ask) because ask mode
	// refuses natural-language write requests with "Switch to Agent mode".
	// The system prompt is the soft lever; --mode ask is the hard lever we
	// can't use for chat without breaking it.
	if ag.provider == llmproviders.ProviderCursorCLI {
		ag.appendBridgeRoutingInstructions("IMPORTANT: For any file write/edit, shell execution, browser operation, or other side-effecting action, prefer the declared MCP bridge tools (e.g. execute_shell_command, diff_patch_workspace_file, agent_browser) over your built-in equivalents. Use built-in tools only for READ operations where no MCP equivalent is declared. When calling MCP tools, use the EXACT tool name as declared (no namespace prefixes). If a declared tool is unavailable, stop and explain rather than falling back to a built-in.")
		logger.Debug("🔧 [CURSOR_CLI] Provider detected - silently disabling incompatible features")

		if !ag.useCodeExecutionMode {
			ag.useCodeExecutionMode = true
			logger.Debug("🔧 [CURSOR_CLI] Auto-enabled Code Execution Mode (CLI manages its own agentic loop)")
		}

		if ag.enableContextEditing {
			ag.enableContextEditing = false
			logger.Debug("🔧 [CURSOR_CLI] Disabled Context Editing (handled natively by CLI)")
		}

		if ag.enableContextSummarization {
			ag.enableContextSummarization = false
			logger.Debug("🔧 [CURSOR_CLI] Disabled Context Summarization (handled natively by CLI)")
		}

		if ag.enableContextOffloading {
			ag.enableContextOffloading = false
			logger.Debug("🔧 [CURSOR_CLI] Disabled Context Offloading (handled natively by CLI)")
		}

		if !ag.enableStreaming {
			ag.enableStreaming = true
			logger.Debug("🔧 [CURSOR_CLI] Auto-enabled streaming (required for tool call observability)")
		}
	}

	// Auto-configure Pi CLI provider. Pi runs through tmux marker transport with
	// pi-mcp-adapter mounted and built-in tools disabled by the adapter when the
	// bridge config is available.
	if ag.provider == llmproviders.ProviderPiCLI {
		ag.appendBridgeRoutingInstructions("IMPORTANT: You are running inside Pi CLI with built-in tools disabled. Use the MCP bridge through Pi's MCP gateway: call mcp({ search: \"tool words\" }) to discover tools, mcp({ describe: \"api_bridge_execute_shell_command\" }) for schemas when needed, and mcp({ tool: \"api_bridge_execute_shell_command\", args: \"{...}\" }) or the direct api_bridge_* tools when available. If a built-in tool is unavailable, use the declared MCP bridge tools instead of reporting that no MCP server exists.")
		logger.Debug("🔧 [PI_CLI] Provider detected - using tmux marker transport with MCP bridge")

		if !ag.useCodeExecutionMode {
			ag.useCodeExecutionMode = true
			logger.Debug("🔧 [PI_CLI] Auto-enabled Code Execution Mode (CLI manages its own agentic loop)")
		}

		if ag.enableContextEditing {
			ag.enableContextEditing = false
			logger.Debug("🔧 [PI_CLI] Disabled Context Editing (handled natively by CLI)")
		}

		if ag.enableContextSummarization {
			ag.enableContextSummarization = false
			logger.Debug("🔧 [PI_CLI] Disabled Context Summarization (handled natively by CLI)")
		}

		if ag.enableContextOffloading {
			ag.enableContextOffloading = false
			logger.Debug("🔧 [PI_CLI] Disabled Context Offloading (handled natively by CLI)")
		}

		if !ag.enableStreaming {
			ag.enableStreaming = true
			logger.Debug("🔧 [PI_CLI] Auto-enabled streaming (required for terminal observability)")
		}
	}

	// Agent initialization complete

	return ag, nil
}

// StartAgentSession initializes a new session for the agent.
//
// It emits an AgentStartEvent, which marks the beginning of a logical session in the
// observability/tracing system. This creates the root or high-level node in the event tree.
func (a *Agent) startAgentSession(ctx context.Context) {
	// Emit agent start event to create hierarchy
	agentStartEvent := events.NewAgentStartEvent(string(a.agentMode), a.modelID, string(a.provider), a.useCodeExecutionMode)
	a.emitTypedEvent(ctx, agentStartEvent)
}

// StartLLMGeneration marks the start of an LLM generation call.
//
// It emits an LLMGenerationStartEvent to the observability system. This should be called
// immediately before sending a request to the LLM provider.
func (a *Agent) startLLMGeneration(ctx context.Context) {
	// Emit LLM generation start event to create hierarchy
	llmStartEvent := events.NewLLMGenerationStartEvent(0, a.modelID, a.temperature, len(a.filteredTools), 0)
	a.emitTypedEvent(ctx, llmStartEvent)
}

// calculateCostFromTokens calculates the cost for tokens based on model metadata
// Returns cost in USD
func calculateCostFromTokens(tokenCount int, costPer1MTokens float64) float64 {
	if tokenCount <= 0 || costPer1MTokens <= 0 {
		return 0.0
	}
	// Convert from cost per 1M tokens to cost for this token count
	return (float64(tokenCount) / 1_000_000.0) * costPer1MTokens
}

// accumulateTokenUsage accumulates token usage from an LLM call.
// It accepts ContentResponse to use the unified Usage field, with fallback to GenerationInfo.
// Only accumulates if we have actual token values from LLM response (not estimates).
func (a *Agent) accumulateTokenUsage(ctx context.Context, usageMetrics events.UsageMetrics, resp *llmtypes.ContentResponse, turn int) {
	// Check if we have actual token values from LLM response
	// Only accumulate if resp has actual usage data (not estimated).
	//
	// Token fields on GenerationInfo come in two naming conventions
	// depending on provider:
	//   - Modern: PromptTokens / CompletionTokens (claude-code experimental, codex, etc.)
	//   - Legacy: InputTokens / OutputTokens
	// We treat both as authoritative; checking only the legacy pair
	// dropped Claude Code chat ledger entries to zero tokens despite
	// gi.PromptTokens being correctly populated by the adapter.
	hasActualUsage := resp != nil && ((resp.Usage != nil && (resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0)) ||
		(len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil &&
			(resp.Choices[0].GenerationInfo.PromptTokens != nil ||
				resp.Choices[0].GenerationInfo.CompletionTokens != nil ||
				resp.Choices[0].GenerationInfo.InputTokens != nil ||
				resp.Choices[0].GenerationInfo.OutputTokens != nil)))

	// Also check if usageMetrics has actual values (from extractUsageMetrics)
	// If usageMetrics has values but resp is nil, it might be from estimation - skip it
	if !hasActualUsage && (usageMetrics.PromptTokens > 0 || usageMetrics.CompletionTokens > 0) {
		// This means usageMetrics was populated but resp is nil or has no actual values
		// This could be from estimation - don't accumulate
		logger := getLogger(a)
		logger.Debug("Skipping token accumulation - no actual usage data from LLM response",
			loggerv2.Int("turn", turn),
			loggerv2.Int("usage_metrics_prompt", usageMetrics.PromptTokens),
			loggerv2.Int("usage_metrics_completion", usageMetrics.CompletionTokens))
		return
	}

	// If we have actual values, proceed with accumulation
	a.tokenTrackingMutex.Lock()
	defer a.tokenTrackingMutex.Unlock()

	// Use passed-in cache and reasoning tokens from usageMetrics (preferred)
	// Fall back to extraction from resp only if passed values are 0
	// Cache tokens: subset of prompt tokens that were cached (for pricing at lower rate)
	// Reasoning tokens: part of output (total output = completion + reasoning)
	cacheTokens := usageMetrics.CacheTokens
	reasoningTokens := usageMetrics.ReasoningTokens

	// If not passed in usageMetrics, extract from response as fallback
	if cacheTokens == 0 || reasoningTokens == 0 {
		extractedCache, _, extractedReasoning := extractAllTokenTypes(resp)
		if cacheTokens == 0 {
			cacheTokens = extractedCache
		}
		if reasoningTokens == 0 {
			reasoningTokens = extractedReasoning
		}
	}

	// Extract cache discount (only available in GenerationInfo)
	var cacheDiscount float64
	if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		generationInfo := resp.Choices[0].GenerationInfo
		if generationInfo.CacheDiscount != nil {
			cacheDiscount = *generationInfo.CacheDiscount
		}
	}

	// Accumulate tokens (only actual values from LLM response)
	// - PromptTokens: total input tokens (includes cached portion)
	// - CompletionTokens: output tokens (excludes reasoning tokens)
	// - CacheTokens: subset of PromptTokens that were cached (for metrics/billing)
	// - ReasoningTokens: additional output tokens for reasoning (total output = completion + reasoning)
	a.cumulativePromptTokens += usageMetrics.PromptTokens
	a.cumulativeCompletionTokens += usageMetrics.CompletionTokens
	a.cumulativeTotalTokens += usageMetrics.TotalTokens
	a.cumulativeCacheTokens += cacheTokens
	a.cumulativeReasoningTokens += reasoningTokens
	a.cumulativeCacheDiscount += cacheDiscount
	a.llmCallCount++

	if cacheTokens > 0 {
		a.cacheEnabledCallCount++
	}

	// Calculate and accumulate pricing
	// Get model metadata to calculate costs (fetch once and cache context window)
	modelID := a.modelID
	if modelID == "" {
		modelID = a.llmModel.GetModelID()
	}

	// Calculate costs for this turn
	var inputCost, outputCost, reasoningCost, cacheCost float64
	if a.llmModel != nil {
		metadata, err := a.llmModel.GetModelMetadata(modelID)
		if err == nil && metadata != nil {
			// Cache context window if not already cached
			if a.modelContextWindow == 0 {
				a.modelContextWindow = metadata.ContextWindow
			}

			// Calculate input cost (excluding cached tokens which are charged separately)
			// Input tokens = total prompt tokens - cached tokens (cached tokens are charged separately at a different rate)
			inputTokens := usageMetrics.PromptTokens - cacheTokens
			if inputTokens < 0 {
				// Safety check: cache tokens should not exceed prompt tokens
				// This could indicate a data inconsistency, but we'll clamp to 0 to prevent negative costs
				inputTokens = 0
			}
			if inputTokens > 0 {
				inputCost = calculateCostFromTokens(inputTokens, metadata.InputCostPer1MTokens)
			}

			// Calculate output cost
			if usageMetrics.CompletionTokens > 0 {
				outputCost = calculateCostFromTokens(usageMetrics.CompletionTokens, metadata.OutputCostPer1MTokens)
			}

			// Calculate reasoning cost
			// If model has specific reasoning cost, use it; otherwise fallback to input token rate
			if reasoningTokens > 0 {
				if metadata.ReasoningCostPer1MTokens > 0 {
					reasoningCost = calculateCostFromTokens(reasoningTokens, metadata.ReasoningCostPer1MTokens)
				} else {
					// Fallback to input token rate when reasoning cost is not specified
					// Reasoning tokens are part of input processing, so charge at input rate
					reasoningCost = calculateCostFromTokens(reasoningTokens, metadata.InputCostPer1MTokens)
				}
			}

			// Calculate cache cost (cached tokens are charged at a different rate)
			if cacheTokens > 0 && metadata.CachedInputCostPer1MTokens > 0 {
				cacheCost = calculateCostFromTokens(cacheTokens, metadata.CachedInputCostPer1MTokens)
			}
		}
	}

	// Check if the provider reported a direct cost (e.g. Claude Code CLI's total_cost_usd).
	// If so, use that instead of per-token calculations (which would be 0 for providers
	// that don't define per-token pricing in metadata).
	var providerReportedCost float64
	if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		if additional := resp.Choices[0].GenerationInfo.Additional; additional != nil {
			if costVal, ok := additional["cost_usd"]; ok {
				switch c := costVal.(type) {
				case float64:
					providerReportedCost = c
				case json.Number:
					if v, err := c.Float64(); err == nil {
						providerReportedCost = v
					}
				}
			}
		}
	}

	// Accumulate costs
	a.cumulativeInputCost += inputCost
	a.cumulativeOutputCost += outputCost
	a.cumulativeReasoningCost += reasoningCost
	a.cumulativeCacheCost += cacheCost

	// Use provider-reported cost if available and calculated cost is zero
	turnCost := inputCost + outputCost + reasoningCost + cacheCost
	if turnCost == 0 && providerReportedCost > 0 {
		turnCost = providerReportedCost
	}
	a.cumulativeTotalCost += turnCost
	// Exposed for convrecord.TurnRecord.Cost (recordConversationTurn) — the
	// per-call cost, not the running total.
	a.lastTurnCost = turnCost

	// Update context window usage (current input tokens in conversation)
	// Set currentContextWindowUsage to the actual prompt tokens from this LLM call.
	// This represents the actual tokens currently in the context window (the messages sent to LLM).
	// Note: currentContextWindowUsage represents the actual tokens currently in the
	// context window (reset after summarization), while cumulativePromptTokens is
	// truly cumulative across all conversation phases (never reset) for pricing/reporting.
	// Context window is based on input tokens only, not output tokens
	a.currentContextWindowUsage = usageMetrics.PromptTokens

	// Token usage is tracked via events - log at debug level for per-turn, but also log cumulative
	logger := getLogger(a)
	logger.Debug("Turn tokens",
		loggerv2.Int("turn", turn),
		loggerv2.Int("input_tokens", usageMetrics.PromptTokens),
		loggerv2.Int("output_tokens", usageMetrics.CompletionTokens),
		loggerv2.Int("total_tokens", usageMetrics.TotalTokens),
		loggerv2.Int("cache_tokens", cacheTokens),
		loggerv2.Int("reasoning_tokens", reasoningTokens),
		loggerv2.Int("cumulative_total", a.cumulativeTotalTokens))
}

// EndLLMGeneration marks the completion of an LLM generation call.
//
// It captures the result, token usage metrics, and duration, emitting an LLMGenerationEndEvent.
// This matches the corresponding StartLLMGeneration call in the event tree.
//
// Parameters:
//   - ctx: Context for the operation.
//   - result: The text content generated by the LLM.
//   - turn: The conversation turn index.
//   - toolCalls: Number of tool calls generated.
//   - duration: Time taken for the generation.
//   - usageMetrics: Token usage statistics.
//   - resp: The full content response object (optional, for detailed metrics).
func (a *Agent) endLLMGeneration(ctx context.Context, result string, turn int, toolCalls int, duration time.Duration, usageMetrics events.UsageMetrics, resp *llmtypes.ContentResponse) {
	// Accumulate token usage (including cache tokens) - uses unified Usage field
	a.accumulateTokenUsage(ctx, usageMetrics, resp, turn)

	// Extract cache and reasoning tokens to include in UsageMetrics
	// Use unified extraction from multi-llm-provider-go
	cacheTokens, _, reasoningTokens := extractAllTokenTypes(resp)

	// Add cache and reasoning tokens to usage metrics
	usageMetrics.CacheTokens = cacheTokens
	usageMetrics.ReasoningTokens = reasoningTokens

	// Calculate context window usage percentage
	var contextUsagePercent float64
	var fixedThresholdPercent float64
	a.tokenTrackingMutex.RLock()
	currentUsage := a.currentContextWindowUsage
	if a.modelContextWindow > 0 {
		contextUsagePercent = (float64(currentUsage) / float64(a.modelContextWindow)) * 100.0
	}
	// Calculate fixed threshold percentage if enabled
	if a.summarizeOnFixedTokenThreshold && a.fixedTokenThreshold > 0 {
		fixedThresholdPercent = (float64(currentUsage) / float64(a.fixedTokenThreshold)) * 100.0
	}
	a.tokenTrackingMutex.RUnlock()

	// Emit LLM generation end event with complete token information
	llmEndEvent := events.NewLLMGenerationEndEvent(turn, result, toolCalls, duration, usageMetrics)

	// Add context usage percentage to metadata
	if llmEndEvent.Metadata == nil {
		llmEndEvent.Metadata = make(map[string]interface{})
	}
	llmEndEvent.Metadata["context_usage_percent"] = contextUsagePercent
	if a.modelContextWindow > 0 {
		llmEndEvent.Metadata["model_context_window"] = a.modelContextWindow
	}
	if fixedThresholdPercent > 0 {
		llmEndEvent.Metadata["fixed_threshold_percent"] = fixedThresholdPercent
		llmEndEvent.Metadata["fixed_threshold_tokens"] = a.fixedTokenThreshold
	}

	// Propagate provider-specific metadata from GenerationInfo.Additional
	// This captures CLI provider info like resolved model, duration, tool calls, etc.
	if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		for k, v := range resp.Choices[0].GenerationInfo.Additional {
			// Skip session IDs (already handled separately) and nil values
			if v == nil || k == "gemini_session_id" || k == "claude_code_session_id" {
				continue
			}
			llmEndEvent.Metadata[k] = v
		}
	}

	a.emitTypedEvent(ctx, llmEndEvent)
}

// emitTotalTokenUsageEvent emits a total token usage event with all cumulative metrics
func (a *Agent) emitTotalTokenUsageEvent(ctx context.Context, conversationDuration time.Duration) {
	a.tokenTrackingMutex.RLock()
	defer a.tokenTrackingMutex.RUnlock()

	// Calculate context window usage percentage
	var contextUsagePercent float64
	var fixedThresholdPercent float64
	currentUsage := a.currentContextWindowUsage
	if a.modelContextWindow > 0 {
		contextUsagePercent = (float64(currentUsage) / float64(a.modelContextWindow)) * 100.0
	}
	// Calculate fixed threshold percentage if enabled
	if a.summarizeOnFixedTokenThreshold && a.fixedTokenThreshold > 0 {
		fixedThresholdPercent = (float64(currentUsage) / float64(a.fixedTokenThreshold)) * 100.0
	}

	// Create generation info map with cumulative cache information and pricing
	generationInfo := make(map[string]interface{})
	generationInfo["cumulative_prompt_tokens"] = a.cumulativePromptTokens
	generationInfo["cumulative_completion_tokens"] = a.cumulativeCompletionTokens
	generationInfo["cumulative_total_tokens"] = a.cumulativeTotalTokens
	generationInfo["cumulative_cache_tokens"] = a.cumulativeCacheTokens
	generationInfo["cumulative_reasoning_tokens"] = a.cumulativeReasoningTokens
	generationInfo["llm_call_count"] = a.llmCallCount
	generationInfo["cache_enabled_call_count"] = a.cacheEnabledCallCount
	// Also expose cache reads under the raw Anthropic-style key the
	// cost ledger's extractCacheTokens reads off — without this the
	// cumulative cache total (which is correctly tracked above) never
	// reaches the per-turn ledger Entry, leaving cache_read_tokens
	// blank for every chat even though the adapter populated it on
	// the per-call GenerationInfo.
	if a.cumulativeCacheTokens > 0 {
		generationInfo["cache_read_input_tokens"] = a.cumulativeCacheTokens
	}

	// Add pricing information
	generationInfo["cumulative_input_cost"] = a.cumulativeInputCost
	generationInfo["cumulative_output_cost"] = a.cumulativeOutputCost
	generationInfo["cumulative_reasoning_cost"] = a.cumulativeReasoningCost
	generationInfo["cumulative_cache_cost"] = a.cumulativeCacheCost
	generationInfo["cumulative_total_cost"] = a.cumulativeTotalCost

	// Add context window usage information
	generationInfo["current_context_window_usage"] = currentUsage
	generationInfo["model_context_window"] = a.modelContextWindow
	generationInfo["context_usage_percent"] = contextUsagePercent
	if fixedThresholdPercent > 0 {
		generationInfo["fixed_threshold_percent"] = fixedThresholdPercent
		generationInfo["fixed_threshold_tokens"] = a.fixedTokenThreshold
	}

	// Emit total token usage event
	totalTokenEvent := events.NewTokenUsageEventWithCache(
		0, // turn (this is a summary event, not tied to a specific turn)
		"conversation_total",
		a.modelID,
		string(a.provider),
		a.cumulativePromptTokens,
		a.cumulativeCompletionTokens,
		a.cumulativeTotalTokens,
		conversationDuration,
		"conversation_total",
		0.0, // cache discount removed
		a.cumulativeReasoningTokens,
		generationInfo,
	)

	// Set pricing and context window fields directly on the event
	totalTokenEvent.InputCost = a.cumulativeInputCost
	totalTokenEvent.OutputCost = a.cumulativeOutputCost
	totalTokenEvent.ReasoningCost = a.cumulativeReasoningCost
	totalTokenEvent.CacheCost = a.cumulativeCacheCost
	totalTokenEvent.TotalCost = a.cumulativeTotalCost
	totalTokenEvent.ContextWindowUsage = a.currentContextWindowUsage
	totalTokenEvent.ModelContextWindow = a.modelContextWindow
	totalTokenEvent.ContextUsagePercent = contextUsagePercent

	// Set agent mode information
	totalTokenEvent.SetAgentMode(string(a.agentMode), a.useCodeExecutionMode)

	a.emitTypedEvent(ctx, totalTokenEvent)

	// Log total token usage summary at Info level for visibility
	logger := getLogger(a)
	logger.Info("🔧 [TOKEN_USAGE] Conversation total token usage",
		loggerv2.Int("total_tokens", a.cumulativeTotalTokens),
		loggerv2.Int("input_tokens", a.cumulativePromptTokens),
		loggerv2.Int("output_tokens", a.cumulativeCompletionTokens),
		loggerv2.Int("cache_tokens", a.cumulativeCacheTokens),
		loggerv2.Int("reasoning_tokens", a.cumulativeReasoningTokens),
		loggerv2.Int("llm_calls", a.llmCallCount),
		loggerv2.Int("cache_enabled_calls", a.cacheEnabledCallCount),
		loggerv2.Any("duration", conversationDuration))

	// Log pricing information
	if a.cumulativeTotalCost > 0 {
		logger.Info("💰 [PRICING] Conversation total cost",
			loggerv2.Any("total_cost_usd", a.cumulativeTotalCost),
			loggerv2.Any("input_cost_usd", a.cumulativeInputCost),
			loggerv2.Any("output_cost_usd", a.cumulativeOutputCost),
			loggerv2.Any("reasoning_cost_usd", a.cumulativeReasoningCost),
			loggerv2.Any("cache_cost_usd", a.cumulativeCacheCost))
	}

	// Log context window usage
	if a.modelContextWindow > 0 {
		logger.Info("📊 [CONTEXT_WINDOW] Context usage",
			loggerv2.Int("current_usage_tokens", a.currentContextWindowUsage),
			loggerv2.Int("context_window_tokens", a.modelContextWindow),
			loggerv2.Any("usage_percent", contextUsagePercent))
	}

	logger.Info("============================================================")
}

// GetTokenUsage returns the current cumulative token usage metrics
// Returns: promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount
func (a *Agent) getTokenUsage() (promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount int) {
	a.tokenTrackingMutex.RLock()
	defer a.tokenTrackingMutex.RUnlock()

	promptTokens = a.cumulativePromptTokens
	completionTokens = a.cumulativeCompletionTokens
	totalTokens = a.cumulativeTotalTokens
	cacheTokens = a.cumulativeCacheTokens
	reasoningTokens = a.cumulativeReasoningTokens
	llmCallCount = a.llmCallCount
	cacheEnabledCallCount = a.cacheEnabledCallCount
	return
}

// GetTokenUsageWithPricing returns the current cumulative token usage metrics with pricing and context usage
// Returns: promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount,
//
//	inputCost, outputCost, reasoningCost, cacheCost, totalCost, contextUsagePercent
func (a *Agent) getTokenUsageWithPricing() (
	promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount int,
	inputCost, outputCost, reasoningCost, cacheCost, totalCost float64,
	contextUsagePercent float64,
) {
	a.tokenTrackingMutex.RLock()
	defer a.tokenTrackingMutex.RUnlock()

	promptTokens = a.cumulativePromptTokens
	completionTokens = a.cumulativeCompletionTokens
	totalTokens = a.cumulativeTotalTokens
	cacheTokens = a.cumulativeCacheTokens
	reasoningTokens = a.cumulativeReasoningTokens
	llmCallCount = a.llmCallCount
	cacheEnabledCallCount = a.cacheEnabledCallCount

	inputCost = a.cumulativeInputCost
	outputCost = a.cumulativeOutputCost
	reasoningCost = a.cumulativeReasoningCost
	cacheCost = a.cumulativeCacheCost
	totalCost = a.cumulativeTotalCost

	// Calculate context window usage percentage
	if a.modelContextWindow > 0 {
		contextUsagePercent = (float64(a.currentContextWindowUsage) / float64(a.modelContextWindow)) * 100.0
	}

	return
}

// recordConversationTurn builds a convrecord.TurnRecord for one completed LLM
// call and hands it to a.conversationSink, if one is configured via
// withConversationSink. A no-op (and no cost of computing anything) when no
// sink is set — call sites do not need to check first.
//
// messages is the caller's own history slice, already including this turn's
// exchange by the time this is called (see the call site in
// AskWithHistory) — passed straight through rather than reconstructed, since
// that's the same "full history, not a delta" shape both existing consumers
// (agent_go's chat_history_persistence.go and family-server's
// conversation_store.go) already persist.
func (a *Agent) recordConversationTurn(turn int, duration time.Duration, messages []llmtypes.MessageContent, promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens int) {
	if a == nil || a.conversationSink == nil {
		return
	}

	var toolCalls []convrecord.ToolCallRecord
	if sessionID := strings.TrimSpace(a.sessionID); sessionID != "" {
		for _, call := range toolcalllog.Snapshot(sessionID) {
			if call.Status != "done" || !call.CompletedAt.After(a.lastToolCallRecordedAt) {
				continue
			}
			toolCalls = append(toolCalls, convrecord.ToolCallRecord{
				ID:          call.ID,
				Name:        call.Name,
				ArgsJSON:    call.ArgsJSON,
				Result:      call.Result,
				StartedAt:   call.StartedAt,
				CompletedAt: call.CompletedAt,
				DurationMS:  call.CompletedAt.Sub(call.StartedAt).Milliseconds(),
			})
			if call.CompletedAt.After(a.lastToolCallRecordedAt) {
				a.lastToolCallRecordedAt = call.CompletedAt
			}
		}
	}

	rec := convrecord.TurnRecord{
		SessionID:  a.sessionID,
		Turn:       turn,
		Timestamp:  time.Now(),
		Provider:   string(a.provider),
		ModelID:    a.modelID,
		DurationMS: duration.Milliseconds(),
		Messages:   messages,
		ToolCalls:  toolCalls,
		TokenUsage: convrecord.TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			CacheTokens:      cacheTokens,
			ReasoningTokens:  reasoningTokens,
		},
	}

	if err := a.conversationSink.WriteTurn(rec); err != nil && a.logger != nil {
		a.logger.Warn("convrecord: WriteTurn failed", loggerv2.Error(err))
	}
}

// EndAgentSession finalizes the current agent session.
//
// It performs usage reporting, resource cleanup (e.g., temporary tool output files),
// and emits an AgentEndEvent. It should be called when the agent's work is complete.
//
// Parameters:
//   - ctx: Context for the operation.
//   - conversationDuration: The total duration of the session/conversation.
func (a *Agent) endAgentSession(ctx context.Context, conversationDuration time.Duration) {
	// Emit total token usage event before agent end event
	a.emitTotalTokenUsageEvent(ctx, conversationDuration)

	// Read cumulative token metrics for agent_end event
	promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount := a.getTokenUsage()

	// Emit agent end event with token usage information
	agentEndEvent := events.NewAgentEndEventWithTokens(
		string(a.agentMode),
		true,
		"",
		promptTokens,
		completionTokens,
		totalTokens,
		cacheTokens,
		reasoningTokens,
		llmCallCount,
		cacheEnabledCallCount,
	)
	a.emitTypedEvent(ctx, agentEndEvent)

	// Stop periodic cleanup routine
	a.stopCleanupRoutine()
	a.closeStreamingTracers()

	// Cleanup agent-specific generated directory (only in code execution mode)
	if a.useCodeExecutionMode {
		a.cleanupAgentGeneratedDir()
	}

	// Cleanup tool output files
	if a.toolOutputHandler != nil {
		// Clean up old files if retention period is configured
		if a.toolOutputRetentionPeriod > 0 {
			if err := a.toolOutputHandler.CleanupOldFiles(a.toolOutputRetentionPeriod); err != nil {
				if a.logger != nil {
					a.logger.Warn("Failed to cleanup old tool output files", loggerv2.Error(err))
				}
			} else if a.logger != nil {
				a.logger.Info("Cleaned up old tool output files", loggerv2.Any("retention_period", a.toolOutputRetentionPeriod))
			}
		}

		// Clean up current session folder if enabled
		if a.cleanupToolOutputOnSessionEnd {
			if err := a.toolOutputHandler.CleanupCurrentSessionFolder(); err != nil {
				if a.logger != nil {
					a.logger.Warn("Failed to cleanup current session tool output folder", loggerv2.Error(err))
				}
			} else if a.logger != nil {
				a.logger.Info("Cleaned up current session tool output folder")
			}
		}
	}
}

// cleanupAgentGeneratedDir removes the agent-specific generated directory
func (a *Agent) cleanupAgentGeneratedDir() {
	agentDir := a.getAgentGeneratedDir()

	// Check if directory exists
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		// Directory doesn't exist, nothing to clean
		return
	}

	// Remove the entire agent directory
	if err := os.RemoveAll(agentDir); err != nil {
		if a.logger != nil {
			a.logger.Warn("⚠️ Failed to cleanup agent directory", loggerv2.Error(err), loggerv2.String("directory", agentDir))
		}
	} else if a.logger != nil {
		a.logger.Info("🧹 Cleaned up agent directory", loggerv2.String("directory", agentDir))
	}
}

// startCleanupRoutine starts the background cleanup routine for old tool output files.
// It runs periodically (every hour by default) to clean up files older than the retention period.
// This ensures cleanup happens even if sessions don't end properly or agents run for long periods.
func (a *Agent) startCleanupRoutine() {
	// Only start if context offloading is enabled and retention period is set
	if !a.enableContextOffloading || a.toolOutputHandler == nil {
		return
	}

	// If retention period is 0, automatic cleanup is disabled
	if a.toolOutputRetentionPeriod == 0 {
		return
	}

	// Use default retention period if negative (safety check)
	retentionPeriod := a.toolOutputRetentionPeriod
	if retentionPeriod < 0 {
		retentionPeriod = DefaultToolOutputRetentionPeriod
	}

	// Capture lifecycle state in locals so Close never races with the goroutine
	// while detaching the Agent's cleanup fields.
	a.cleanupMu.Lock()
	if a.cleanupTicker != nil {
		a.cleanupMu.Unlock()
		return
	}
	ticker := time.NewTicker(DefaultToolOutputCleanupInterval)
	done := make(chan struct{})
	a.cleanupTicker = ticker
	a.cleanupDone = done
	a.cleanupMu.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				// Perform periodic cleanup
				if a.toolOutputHandler != nil && retentionPeriod > 0 {
					if err := a.toolOutputHandler.CleanupOldFiles(retentionPeriod); err != nil {
						if a.logger != nil {
							a.logger.Warn("Periodic cleanup of old tool output files failed", loggerv2.Error(err))
						}
					} else if a.logger != nil {
						a.logger.Debug("Periodic cleanup of old tool output files completed", loggerv2.Any("retention_period", retentionPeriod))
					}
				}
			case <-done:
				if a.logger != nil {
					a.logger.Debug("Tool output cleanup routine stopped")
				}
				return
			}
		}
	}()
}

// stopCleanupRoutine stops the background cleanup routine.
// This should be called when the agent is closed or session ends to prevent resource leaks.
func (a *Agent) stopCleanupRoutine() {
	a.cleanupMu.Lock()
	ticker := a.cleanupTicker
	done := a.cleanupDone
	a.cleanupTicker = nil
	a.cleanupDone = nil
	a.cleanupMu.Unlock()

	if ticker != nil {
		ticker.Stop()
	}
	if done != nil {
		close(done)
	}
}

func (a *Agent) closeStreamingTracers() {
	for _, tracer := range a.tracers {
		if streamingTracer, ok := tracer.(*streamingTracerImpl); ok {
			_ = streamingTracer.Close()
		}
	}
}

func (a *Agent) addEventListener(listener AgentEventListener) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.listeners == nil {
		a.listeners = make([]AgentEventListener, 0)
	}
	a.listeners = append(a.listeners, listener)

	// Streaming tracers forward events to subscribers; direct listeners remain
	// the integration point used by the builder and server event bridges.
	if _, hasStreaming := a.streamingTracer(); hasStreaming {
		a.logger.Info("🔍 Streaming tracer enabled for event listener", loggerv2.String("listener", listener.Name()))

		// The streaming tracer is already active and will forward events to all listeners
		// No additional setup needed - events automatically flow through the streaming system
	} else {
		a.logger.Warn("Streaming tracer not available, using traditional event listener system")
	}
}

// initializeHierarchyForContext sets the initial hierarchy level based on calling context
func (a *Agent) initializeHierarchyForContext(ctx context.Context) {
	// ✅ SIMPLIFIED APPROACH: Detect context by checking stack trace or other indicators

	// Check if we're in orchestrator context by looking for orchestrator-related context values
	if orchestratorID := ctx.Value("orchestrator_id"); orchestratorID != nil {
		// Orchestrator context: Start at level 2 (orchestrator_start -> orchestrator_agent_start -> system_prompt)
		a.currentHierarchyLevel = 2
		a.currentParentEventID = fmt.Sprintf("orchestrator_agent_start_%d", time.Now().UnixNano())
		return
	}

	// Check if we're in server context (HTTP API call) by looking for session-related context values
	if sessionID := ctx.Value("session_id"); sessionID != nil {
		// Server context: Start at level 0 (system_prompt is root)
		a.currentHierarchyLevel = 0
		a.currentParentEventID = ""
		return
	}

	// ✅ FALLBACK: Always start at level 0 for now
	// This ensures consistent behavior until we implement proper context detection
	a.currentHierarchyLevel = 0
	a.currentParentEventID = ""
}

// EmitTypedEvent sends a typed event to all tracers AND all listeners.
// Thread-safe: uses eventMu to protect hierarchy state (currentParentEventID, currentHierarchyLevel)
// which can be mutated concurrently during parallel tool execution.
func (a *Agent) emitTypedEvent(ctx context.Context, eventData events.EventData) {

	// Lock eventMu to protect hierarchy state reads and writes
	a.eventMu.Lock()

	// ✅ SET HIERARCHY FIELDS ON EVENT DATA FIRST (SINGLE SOURCE OF TRUTH)
	// Use interface-based approach - works for ALL event types that embed BaseEventData
	if baseEventData, ok := eventData.(interface {
		SetHierarchyFields(string, int, string, string)
	}); ok {
		// Use SessionID for event storage (links events to chat sessions)
		// Fall back to TraceID if SessionID is not set (legacy behavior)
		sessionIDForEvents := a.sessionID
		if sessionIDForEvents == "" {
			sessionIDForEvents = string(a.traceID)
		}
		baseEventData.SetHierarchyFields(a.currentParentEventID, a.currentHierarchyLevel, sessionIDForEvents, events.GetComponentFromEventType(eventData.GetEventType()))
	}

	// Create event with correlation ID for start/end event pairs
	event := events.NewAgentEvent(eventData)
	event.TraceID = string(a.traceID)

	// Generate a unique SpanID for this event
	event.SpanID = fmt.Sprintf("span_%s_%d", string(eventData.GetEventType()), time.Now().UnixNano())

	// ✅ COPY HIERARCHY FIELDS FROM EVENT DATA TO WRAPPER (SINGLE SOURCE OF TRUTH)
	// Get hierarchy fields from the event data (which we just set above)
	// Use interface to access BaseEventData fields from any event type
	if baseEventData, ok := eventData.(interface{ GetBaseEventData() *events.BaseEventData }); ok {
		baseData := baseEventData.GetBaseEventData()
		event.ParentID = baseData.ParentID
		event.HierarchyLevel = baseData.HierarchyLevel
		event.SessionID = baseData.SessionID
		event.Component = baseData.Component
	}

	// Update hierarchy for next event based on event type
	eventType := events.EventType(eventData.GetEventType())

	if events.IsStartEvent(eventType) {
		// ✅ SPECIAL HANDLING: conversation_turn should reset to level 2 (child of conversation_start)
		switch eventType {
		case events.ConversationTurn:
			a.currentHierarchyLevel = 2 // Reset to level 2 for new conversation turn
			a.currentParentEventID = event.SpanID
		case events.ToolCallStart:
			// ✅ SPECIAL HANDLING: tool_call_start should be sibling of llm_generation_end
			// Don't increment level - use current level (same as llm_generation_end)
			a.currentParentEventID = event.SpanID
		default:
			// ✅ FIX: Increment level FIRST, then use it for next event
			a.currentHierarchyLevel++
			a.currentParentEventID = event.SpanID
		}
		// ✅ For end events: Level remains unchanged
		// SPECIAL HANDLING: tool_call_end should be sibling of tool_call_start
		// FIX: Don't decrement level immediately - let the next start event handle it
		// This allows token_usage and tool_call_start to be siblings of llm_generation_end
	}

	// Done with hierarchy state - unlock before I/O operations
	a.eventMu.Unlock()

	// Add correlation ID for start/end event pairs
	if isStartOrEndEvent(events.EventType(eventData.GetEventType())) {
		event.CorrelationID = fmt.Sprintf("%s_%d", string(eventData.GetEventType()), time.Now().UnixNano())
	}

	// Collect tool call events for prompt logging across providers.
	if os.Getenv("LOG_AGENT_PROMPTS") == "true" {
		switch e := eventData.(type) {
		case *events.ToolCallStartEvent:
			args := e.ToolParams.Arguments
			if len(args) > 2000 {
				args = args[:2000] + fmt.Sprintf("... (truncated, %d chars)", len(e.ToolParams.Arguments))
			}
			entry := fmt.Sprintf("**Tool Call**: `%s` (server: %s, turn: %d)\n```json\n%s\n```\n", e.ToolName, e.ServerName, e.Turn, args)
			a.toolCallLogMu.Lock()
			a.toolCallLog = append(a.toolCallLog, entry)
			a.toolCallLogMu.Unlock()
		case *events.ToolCallEndEvent:
			result := e.Result
			if len(result) > 3000 {
				result = result[:3000] + fmt.Sprintf("\n... (truncated, %d chars)", len(e.Result))
			}
			entry := fmt.Sprintf("**Tool Result**: `%s` (duration: %v)\n```\n%s\n```\n", e.ToolName, e.Duration, result)
			a.toolCallLogMu.Lock()
			a.toolCallLog = append(a.toolCallLog, entry)
			a.toolCallLogMu.Unlock()
		}
	}

	// Send to all tracers (multiple tracer support)
	// The streaming tracer will automatically forward events to subscribers
	for _, tracer := range a.tracers {
		if err := tracer.EmitEvent(event); err != nil {
			a.logger.Warn("Failed to emit event to tracer", loggerv2.Error(err), loggerv2.String("tracer_type", fmt.Sprintf("%T", tracer)))
		}
	}

	// ALSO send to all event listeners for backward compatibility
	// This ensures existing code continues to work while streaming is available
	a.mu.RLock()
	listeners := make([]AgentEventListener, len(a.listeners))
	copy(listeners, a.listeners)
	a.mu.RUnlock()

	for _, listener := range listeners {
		if err := listener.HandleEvent(ctx, event); err != nil {
			a.logger.Warn("Failed to emit event to listener", loggerv2.Error(err), loggerv2.String("listener_type", fmt.Sprintf("%T", listener)))
		}
	}
}

// isStartOrEndEvent checks if an event type is a start or end event that needs correlation ID
func isStartOrEndEvent(eventType events.EventType) bool {
	return eventType == events.ConversationStart || eventType == events.ConversationEnd ||
		eventType == events.LLMGenerationStart || eventType == events.LLMGenerationEnd ||
		eventType == events.ToolCallStart || eventType == events.ToolCallEnd
}

// GetStreamingTracer returns the streaming tracer if available
func (a *Agent) streamingTracer() (StreamingTracer, bool) {
	if len(a.tracers) > 0 {
		if streamingTracer, ok := a.tracers[0].(StreamingTracer); ok {
			return streamingTracer, true
		}
	}
	return nil, false
}

// GetEventStream returns the event stream channel if streaming is available
func (a *Agent) getEventStream() (<-chan *events.AgentEvent, bool) {
	if streamingTracer, hasStreaming := a.streamingTracer(); hasStreaming {
		return streamingTracer.GetEventStream(), true
	}
	return nil, false
}

// SubscribeToEvents allows external systems to subscribe to agent events
func (a *Agent) subscribeToEvents(ctx context.Context) (<-chan *events.AgentEvent, func(), bool) {
	if streamingTracer, hasStreaming := a.streamingTracer(); hasStreaming {
		eventChan, unsubscribe := streamingTracer.SubscribeToEvents(ctx)
		return eventChan, unsubscribe, true
	}
	return nil, func() {}, false
}

// getClientNames returns a list of client names for debugging
func getClientNames(clients map[string]mcpclient.ClientInterface) []string {
	names := make([]string, 0, len(clients))
	for name := range clients {
		names = append(names, name)
	}
	return names
}

// Close gracefully terminates the agent and closes all underlying resources.
//
// It iterates through all active MCP client connections and closes them.
// This method should be called when the agent is no longer needed to prevent resource leaks.
func (a *Agent) Close() error {
	// Stop periodic cleanup routine
	a.stopCleanupRoutine()
	a.closeStreamingTracers()

	// Connections are shared and managed by the session registry. Do not close
	// them here; they persist until CloseSession(sessionID) is called.
	if a.logger != nil {
		a.logger.Info("Agent closed (connections persist in session registry)",
			loggerv2.String("session_id", a.sessionID),
			loggerv2.Int("client_count", len(a.clients)))
	}

	// IsolatedSessionWorkspace cleanup: rm -rf the tmp dir we created in
	// ensureIsolatedWorkspaceDir. Errors are silently ignored — the OS will
	// eventually clean /tmp on reboot, and the dir name (mlp-cli-session-*)
	// is distinctive enough that a leaked dir is recognizable in `df`/`ls
	// /tmp` output.
	//
	// Session-derived dirs (isolatedWorkspaceStable) are deliberately NOT
	// removed here. A new Agent is constructed for every turn, so this Close
	// runs between turns of a live session; removing the dir would delete the
	// coding CLI's resumable conversation and force the next turn to start
	// fresh. Those are reclaimed by CloseSession at true session end.
	if a.isolatedWorkspacePath != "" && !a.isolatedWorkspaceStable {
		_ = os.RemoveAll(a.isolatedWorkspacePath)
		if a.logger != nil {
			a.logger.Info("IsolatedSessionWorkspace: removed tmp dir " + a.isolatedWorkspacePath)
		}
	} else if wd := strings.TrimSpace(a.codingAgentWorkingDir); wd != "" && llm.IsCodingAgentProvider(a.provider, a.modelID) {
		// Real (non-isolated) workdir: the whole-tree rm -rf above never runs, so
		// skills + the managed system prompt this session projected would otherwise
		// linger in the operator's repo after close (Claude/Codex/Pi don't wipe
		// them; only Cursor's adapter does). Remove exactly what we projected —
		// named skill folders + marker-verified prompt files — leaving operator
		// content intact.
		cleanupProjectedArtifactsOnClose(wd, a.provider, a.attachedSkills)
	}
	return nil
}

// Ask processes a single question from the user and returns the agent's response.
//
// This is a convenience wrapper around AskWithHistory that creates a single-message
// conversation history. It handles the full ReAct loop (Reasoning + Acting), allowing
// the agent to call tools as needed to answer the question.
//
// Parameters:
//   - ctx: Context for the request (can be used for cancellation).
//   - question: The user's input question.
//
// Returns:
//   - string: The final text response from the agent.
//   - error: An error if the interaction fails.
func (a *Agent) ask(ctx context.Context, question string) (string, error) {
	// Create a single user message for the question
	userMessage := llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: question}},
	}

	// Call AskWithHistory with the single message
	answer, _, err := askWithHistory(a, ctx, []llmtypes.MessageContent{userMessage})
	return answer, err
}

// AskWithHistory runs a multi-turn conversation interaction using the provided message history.
//
// It continues an existing conversation, processing the latest user message (last in the slice)
// and generating a response. It handles tool execution, context management, and recursive
// calls for multi-step reasoning.
//
// Parameters:
//   - ctx: Context for the request.
//   - messages: The conversation history, including the new user message.
//
// Returns:
//   - string: The final text response from the agent.
//   - []llmtypes.MessageContent: The updated conversation history (including the new response).
//   - error: An error if the interaction fails.
func (a *Agent) askWithHistory(ctx context.Context, messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	return askWithHistory(a, ctx, messages)
}

// GetServerNames returns the list of connected server names
func (a *Agent) getServerNames() []string {
	return getClientNames(a.clients)
}

// GetConfiguredServerName is retained for legacy chat runtime persistence.
func (a *Agent) getConfiguredServerName() string {
	return a.serverName
}

// GetSelectedTools is retained for legacy chat runtime persistence.
func (a *Agent) getSelectedTools() []string {
	return append([]string(nil), a.selectedTools...)
}

// SetInstructions replaces the base instructions while preserving supplements.
// Dynamic tool instructions are rendered only at the outbound boundary.
func (a *Agent) setInstructions(systemPrompt string) {
	a.systemPrompt = systemPrompt

	if a.logger != nil {
		a.logger.Debug("✅ System prompt overwritten", loggerv2.Int("length_chars", len(systemPrompt)))
	}
	a.hasCustomSystemPrompt = true
}

// appendBridgeRoutingInstructions appends the bridge-tool-routing block for
// the calling provider's default preamble, UNLESS the caller supplied its own
// override via withBridgeRoutingInstructions (an empty-string override
// suppresses the block entirely; a non-empty one replaces both the
// provider-specific preamble AND the shared bridgeRoutingExplicitInstructions
// text with the caller's own).
func (a *Agent) appendBridgeRoutingInstructions(defaultPreamble string) {
	if a.bridgeRoutingInstructionsOverride != nil {
		if *a.bridgeRoutingInstructionsOverride != "" {
			a.appendInstructions(*a.bridgeRoutingInstructionsOverride)
		}
		return
	}
	a.appendInstructions(defaultPreamble, bridgeRoutingExplicitInstructions())
}

// AddInstructions records supplementary instructions. They are composed with
// the current base prompt at the outbound boundary, so changing or clearing the
// list cannot leave stale materialized copies in systemPrompt.
func (a *Agent) appendInstructions(instructions ...string) {
	for _, additionalPrompt := range instructions {
		a.addInstructions(additionalPrompt)
	}
}

func (a *Agent) addInstructions(additionalPrompt string) {
	if additionalPrompt == "" {
		return
	}

	// Avoid duplicating a block already provided by the base prompt or already
	// recorded as a supplement.
	if a.systemPrompt != "" && strings.Contains(a.systemPrompt, additionalPrompt) {
		if a.logger != nil {
			a.logger.Warn("⏭️ AddInstructions: skipped duplicate block already in base instructions",
				loggerv2.Int("length_chars", len(additionalPrompt)),
				loggerv2.Int("appended_count", len(a.appendedSystemPrompts)),
				loggerv2.Int("base_prompt_chars", len(a.systemPrompt)),
				loggerv2.String("block_prefix", systemPromptPreview(additionalPrompt)),
				loggerv2.String("caller", callerChain()))
		}
		return
	}
	for _, existing := range a.appendedSystemPrompts {
		if existing == additionalPrompt {
			if a.logger != nil {
				a.logger.Debug("⏭️ AddInstructions: skipped duplicate supplementary block",
					loggerv2.Int("length_chars", len(additionalPrompt)),
					loggerv2.String("block_prefix", systemPromptPreview(additionalPrompt)))
			}
			return
		}
	}

	// Track appended prompt metadata for inspection and prompt restoration.
	a.appendedSystemPrompts = append(a.appendedSystemPrompts, additionalPrompt)
	a.hasAppendedPrompts = true

	if a.logger != nil {
		a.logger.Debug("✅ Supplementary system prompt recorded",
			loggerv2.Int("length_chars", len(additionalPrompt)),
			loggerv2.Int("appended_count", len(a.appendedSystemPrompts)),
			loggerv2.String("block_prefix", systemPromptPreview(additionalPrompt)))
	}

	// Mark as custom to prevent overwriting
	a.hasCustomSystemPrompt = true
}

// ResetInstructions atomically replaces the base and every supplement.
func (a *Agent) resetInstructions(base string, supplements ...string) {
	a.systemPrompt = base
	a.appendedSystemPrompts = nil
	a.hasAppendedPrompts = false
	for _, supplement := range supplements {
		a.addInstructions(supplement)
	}
	a.hasCustomSystemPrompt = true
}

// callerChain returns a compact "fn:line <- fn:line <- …" trace of the
// callers above this package's frame. Used to pin which external code path
// triggered a suspicious duplicate system-prompt append.
func callerChain() string {
	pcs := make([]uintptr, 10)
	// skip: runtime.Callers, callerChain, and the logging method that called us.
	n := runtime.Callers(3, pcs)
	if n == 0 {
		return "?"
	}
	frames := runtime.CallersFrames(pcs[:n])
	var parts []string
	for i := 0; i < 5; i++ {
		f, more := frames.Next()
		if f.Function == "" {
			break
		}
		name := f.Function
		if idx := strings.LastIndexByte(name, '/'); idx != -1 {
			name = name[idx+1:]
		}
		parts = append(parts, fmt.Sprintf("%s:%d", name, f.Line))
		if !more {
			break
		}
	}
	return strings.Join(parts, " <- ")
}

// systemPromptPreview returns a short single-line prefix of a system-prompt
// block for logging — enough to identify which block (capability snapshot,
// browser pointer, workspace map, …) without dumping the whole thing.
func systemPromptPreview(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		s = s[:idx]
	}
	const max = 80
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// RegisterCustomTool registers a dynamic custom tool with the agent.
//
// This allows adding tools at runtime that are not provided by an MCP server.
// The tool will be available for the LLM to use during interactions.
//
// Parameters:
//   - name: The unique name of the tool.
//   - description: A description of what the tool does (used by LLM).
//   - parameters: JSON schema defining the tool's expected arguments.
//   - executionFunc: The Go function to execute when the tool is called.
//   - category: REQUIRED. The tool's category (e.g., "workspace", "human_tools", "virtual").
//     Compile-time required (was previously variadic with a runtime check; the
//     runtime check turned silent registration failures into a category of bug
//     that only surfaced in server_debug.log — making it required enforces
//     correctness at the call site).
//
// Returns:
//   - error: An error if registration fails (e.g., empty category).
func (a *Agent) registerCustomTool(name string, description string, parameters map[string]interface{}, executionFunc func(ctx context.Context, args map[string]interface{}) (string, error), category string) error {
	return a.registerDirectTool(name, description, parameters, executionFunc, 0, category)
}

func (a *Agent) registerDirectTool(name string, description string, parameters map[string]interface{}, executionFunc func(ctx context.Context, args map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	// Category is required at compile time, but still validate non-empty in case
	// a caller passes an empty literal.
	if category == "" {
		err := fmt.Errorf("tool %s registered with empty category - category is REQUIRED for all tools", name)
		if a.logger != nil {
			a.logger.Error("❌ [DISCOVERY] Tool registered with empty category", err)
		}
		return err
	}
	if name == readSkillToolName && !a.installingSkillReader {
		return fmt.Errorf("tool name %q is reserved for attached skill access", readSkillToolName)
	}
	toolCategory := category

	// Tool names are the model-facing address. Reject collisions before touching
	// any registry so a direct tool cannot replace an MCP tool, and a tool cannot
	// silently move between legacy configuration categories. Re-registering the
	// same direct tool in the same category remains supported during migration;
	// several builder paths use that to refresh a session-aware executor.
	if server, exists := a.toolToServer[name]; exists && server != "custom" {
		return fmt.Errorf("tool name %q is already registered by MCP server %q", name, server)
	}
	if existing, exists := a.lookupDirectTool(name); exists && existing.DisplayGroup != toolCategory {
		return fmt.Errorf(
			"tool name %q is already registered in category %q and cannot be registered in category %q",
			name,
			existing.DisplayGroup,
			toolCategory,
		)
	}

	// Create the tool definition
	tool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  llmtypes.NewParameters(parameters),
		},
	}
	registry, err := a.canonicalRegistry()
	if err != nil {
		return err
	}
	if err := registry.register(registeredTool{
		Name:         name,
		Definition:   tool,
		Kind:         toolImplementationDirect,
		Source:       "direct",
		DisplayGroup: toolCategory,
		Executor:     executionFunc,
		Timeout:      timeout,
	}); err != nil {
		return err
	}

	// 🔧 CRITICAL FIX: Add custom tools to toolToServer mapping with special "custom" marker
	// This ensures they're recognized during tool lookup even when NoServers is used
	if a.toolToServer == nil {
		a.toolToServer = make(map[string]string)
		if a.logger != nil {
			a.logger.Debug("🔧 [TOOL_REGISTRATION] Initialized toolToServer map for custom tools")
		}
	}
	a.toolToServer[name] = "custom"

	// Ensure the tool filter recognises this category for get_api_spec lookups.
	// Custom tools registered after newAgent would otherwise be invisible to IsCategoryDirectory.
	if a.toolFilter != nil && toolCategory != "" {
		a.toolFilter.AddCustomCategory(toolCategory)
	}

	if a.logger != nil {
		a.logger.Debug(fmt.Sprintf("🔧 [TOOL_REGISTRATION] Added custom tool '%s' to toolToServer mapping (category: %s)", name, toolCategory))
	}

	// 🔁 Ensure tool registration is idempotent by name
	// Some higher-level orchestration or decision agents
	// may call RegisterCustomTool with the same name multiple times over the lifetime
	// of a shared Agent. The LLM provider requires unique function names per request,
	// so we must avoid accumulating duplicate entries for the same tool name.
	//
	// Before appending, strip any existing tool with this name from Tools and filteredTools.
	beforeCount := len(a.tools)
	if len(a.tools) > 0 {
		cleanTools := make([]llmtypes.Tool, 0, len(a.tools))
		for _, t := range a.tools {
			if t.Function == nil || t.Function.Name != name {
				cleanTools = append(cleanTools, t)
			}
		}
		a.tools = cleanTools
	}
	if len(a.tools) != beforeCount && a.logger != nil {
		a.logger.Debug(fmt.Sprintf("[BRIDGE_DEBUG] RegisterCustomTool(%s): cleanup removed %d duplicate(s)", name, beforeCount-len(a.tools)))
	}

	if len(a.filteredTools) > 0 {
		cleanFiltered := make([]llmtypes.Tool, 0, len(a.filteredTools))
		for _, t := range a.filteredTools {
			if t.Function == nil || t.Function.Name != name {
				cleanFiltered = append(cleanFiltered, t)
			}
		}
		a.filteredTools = cleanFiltered
	}

	if a.useCodeExecutionMode {
		if a.toolFilter.IsSystemCategory(toolCategory) {
			// System tools (execute_shell_command, workspace tools) stay as direct LLM calls
			a.tools = append(a.tools, tool)
			a.filteredTools = append(a.filteredTools, tool)
			if a.logger != nil {
				a.logger.Debug(fmt.Sprintf("🔧 [CODE_EXECUTION] System-category custom tool '%s' added as direct LLM call (category: %s)", name, toolCategory))
			}
		} else {
			// Non-system direct tools go through HTTP API via the canonical registry.
			if a.logger != nil {
				a.logger.Debug(fmt.Sprintf("🔧 [CODE_EXECUTION] Custom tool '%s' registered for HTTP API access only (category: %s)", name, toolCategory))
			}
		}
	} else {
		// Normal mode: Add to the main Tools array so the LLM can see it
		a.tools = append(a.tools, tool)

		// Also add to filteredTools so the tool is available in the current conversation.
		a.filteredTools = append(a.filteredTools, tool)
	}

	// Tool schemas are cached independently of authorization, but a registration
	// may add or replace a definition. Clear the small per-agent schema cache so
	// the next authorized lookup regenerates from the canonical registration.
	if a.useCodeExecutionMode {
		a.openAPISpecCacheMu.Lock()
		a.openAPISpecCache = make(map[string][]byte)
		a.openAPISpecCacheMu.Unlock()
	}

	// Update registry with new custom tool
	if a.clients != nil {
		customToolExecutors := a.directToolExecutors()
		if a.logger != nil {
			a.logger.Debug("🔧 [CODE_EXECUTION] Updating registry with custom tools",
				loggerv2.Int("count", len(customToolExecutors)),
				loggerv2.String("including", name))
			// Log all custom tool names for debugging
			toolNames := make([]string, 0, len(customToolExecutors))
			for toolName := range customToolExecutors {
				toolNames = append(toolNames, toolName)
			}
			a.logger.Debug("🔧 [CODE_EXECUTION] Custom tools in registry", loggerv2.Any("tools", toolNames))
		}
		codeexec.InitRegistry(a.clients, customToolExecutors, a.toolToServer, a.logger)
		// Also register session-scoped tools
		if a.sessionID != "" {
			codeexec.InitRegistryForSession(a.sessionID, customToolExecutors, a.logger)
		}
		if a.logger != nil {
			a.logger.Debug("🔧 [CODE_EXECUTION] Registry updated successfully for tool", loggerv2.String("tool", name))
		}
	} else {
		if a.logger != nil {
			a.logger.Warn("⚠️ [CODE_EXECUTION] Cannot update registry - a.Clients is nil for tool", loggerv2.String("tool", name))
		}
	}

	// Debug logging
	if a.logger != nil {
		a.logger.Info("🔧 Registered custom tool", loggerv2.String("tool", name), loggerv2.String("category", toolCategory))
		a.logger.Info("🔧 Total custom tools registered", loggerv2.Int("count", len(a.directToolSnapshot())))
		a.logger.Info("🔧 Total tools in agent", loggerv2.Int("count", len(a.tools)))
		a.logger.Info("🔧 Total filtered tools", loggerv2.Int("count", len(a.filteredTools)))
	}

	return nil
}

// RegisterCustomToolWithTimeout registers a dynamic custom tool with a specific per-tool timeout.
//
// This is an extension of RegisterCustomTool that allows specifying a custom timeout for this tool.
// This is useful for tools that may take longer than the default timeout (e.g., sub-agent execution).
//
// Parameters:
//   - name: The unique name of the tool.
//   - description: A description of what the tool does (used by LLM).
//   - parameters: JSON schema defining the tool's expected arguments.
//   - executionFunc: The Go function to execute when the tool is called.
//   - timeout: Per-tool timeout. 0 = no timeout (tool runs indefinitely). -1 = use agent default.
//
// GetCustomToolExecutor returns the current execution function for a custom tool, or nil if not found.
func (a *Agent) getCustomToolExecutor(name string) func(ctx context.Context, args map[string]interface{}) (string, error) {
	if tool, exists := a.lookupDirectTool(name); exists {
		return tool.Executor
	}
	return nil
}

//   - category: REQUIRED. The tool's category (e.g., "workspace", "human_tools", "virtual").
//
// Returns:
//   - error: An error if registration fails (e.g., missing category).
func (a *Agent) registerCustomToolWithTimeout(name string, description string, parameters map[string]interface{}, executionFunc func(ctx context.Context, args map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	err := a.registerDirectTool(name, description, parameters, executionFunc, timeout, category)
	if err != nil {
		return err
	}
	if a.logger != nil {
		if timeout == 0 {
			a.logger.Info("🔧 Custom tool registered with NO timeout (runs indefinitely)", loggerv2.String("tool", name))
		} else if timeout == -1 {
			a.logger.Info("🔧 Custom tool registered with agent default timeout", loggerv2.String("tool", name))
		} else {
			a.logger.Info("🔧 Custom tool registered with custom timeout", loggerv2.String("tool", name), loggerv2.String("timeout", timeout.String()))
		}
	}

	return nil
}

// GetCustomToolCategories returns a list of all unique categories for registered custom tools
func (a *Agent) getCustomToolCategories() []string {
	categorySet := make(map[string]bool)
	for _, tool := range a.directToolSnapshot() {
		if tool.DisplayGroup != "" {
			categorySet[tool.DisplayGroup] = true
		}
	}

	categories := make([]string, 0, len(categorySet))
	for cat := range categorySet {
		categories = append(categories, cat)
	}
	return categories
}

// GetVirtualToolScopeID returns a unique scope key for this agent's virtual tools.
// This prevents parent/child agents sharing the same SessionID from overwriting
// each other's virtual tool handlers (e.g., get_api_spec bound to different agent instances).
// Custom tools continue to use SessionID for sharing, but virtual tools need per-agent scoping
// because they bind to agent-specific state (toolRegistry, toolFilter).
func (a *Agent) virtualToolScopeID() string {
	if a.sessionID == "" {
		return ""
	}
	return a.sessionID + ":vt:" + string(a.traceID)
}

// SetToolAccess is the single public tool-policy operation. Non-empty names
// restrict discovery and execution; nil or empty restores every registered tool.
func (a *Agent) setToolAccess(toolNames []string) {
	a.toolAllowListMu.Lock()
	defer a.toolAllowListMu.Unlock()
	if len(toolNames) == 0 {
		a.toolAllowList = nil
		// Also clear from code exec registry (for HTTP-based tool calls in code exec mode)
		if a.sessionID != "" {
			codeexec.SetSessionToolAllowList(a.sessionID, nil)
		}
		return
	}
	a.toolAllowList = make(map[string]bool, len(toolNames))
	for _, name := range toolNames {
		a.toolAllowList[name] = true
	}
	// Also set on code exec registry so HTTP-based tool calls are blocked too
	if a.sessionID != "" {
		codeexec.SetSessionToolAllowList(a.sessionID, a.toolAllowList)
	}
	if a.logger != nil {
		a.logger.Info("🔒 [TOOL_ALLOW_LIST] Set",
			loggerv2.Int("allowed_count", len(toolNames)),
			loggerv2.Any("allowed_tools", toolNames))
	}
}

// isToolAllowed checks if a tool name passes the allow list filter.
// Returns true if no allow list is set or if the tool is in the list.
func (a *Agent) isToolAllowed(toolName string) bool {
	if a.isIntrinsicIdentityTool(toolName) {
		return true
	}
	a.toolAllowListMu.RLock()
	defer a.toolAllowListMu.RUnlock()
	if a.toolAllowList == nil {
		return true
	}
	return a.toolAllowList[toolName]
}

func (a *Agent) isToolAllowedForContext(ctx context.Context, toolName string) bool {
	if a.isIntrinsicIdentityTool(toolName) {
		return true
	}
	if policy, ok := toolPolicyFromContext(ctx); ok {
		return policy.allows(toolName)
	}
	return a.isToolAllowed(toolName)
}

// applyToolAllowList filters a tool slice to only include tools in the allow list.
// The caller is responsible for including virtual/system tool names in the allow list
// if they should remain available.
func (a *Agent) applyToolAllowList(tools []llmtypes.Tool) []llmtypes.Tool {
	a.toolAllowListMu.RLock()
	defer a.toolAllowListMu.RUnlock()
	if a.toolAllowList == nil {
		return tools
	}
	filtered := make([]llmtypes.Tool, 0, len(tools))
	var blocked []string
	for _, t := range tools {
		if t.Function == nil {
			filtered = append(filtered, t)
			continue
		}
		if a.toolAllowList[t.Function.Name] || a.isIntrinsicIdentityTool(t.Function.Name) {
			filtered = append(filtered, t)
		} else {
			blocked = append(blocked, t.Function.Name)
		}
	}
	if a.logger != nil {
		a.logger.Info("🔒 [TOOL_ALLOW_LIST] Applied",
			loggerv2.Int("total", len(tools)),
			loggerv2.Int("allowed", len(filtered)),
			loggerv2.Int("blocked", len(blocked)))
		if len(blocked) > 0 {
			a.logger.Debug("🔒 [TOOL_ALLOW_LIST] Blocked tools", loggerv2.Any("blocked", blocked))
		}
	}
	return filtered
}

// Instructions returns the exact instruction text the model receives.
func (a *Agent) instructions() string {
	return a.outgoingSystemPrompt()
}

// getGeneratedDir returns the path to the generated/ directory
// Only creates the directory if code execution mode is enabled
func (a *Agent) getGeneratedDir() string {
	// Use shared utility for path calculation (single source of truth)
	path := mcpcache.GetGeneratedDirPath()

	// Only create directory if code execution mode is enabled
	// In simple agent mode, we don't need the generated directory
	if a.useCodeExecutionMode {
		_ = mcpcache.EnsureGeneratedDir(path, a.logger)
	}

	return path
}
