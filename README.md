# MCPAgent - Go Agent Runtime

[![Go Version](https://img.shields.io/badge/Go-1.24.4-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A production-ready Go library for building tool-using, code-executing agents across frontier, open, and CLI-native model providers. MCP support is built in, but it is only one part of the runtime.

## ⚡ Why People Use It

- Build one agent runtime instead of separate code paths for MCP tools, code execution, and provider switching
- Mix API-native models and CLI-native coding agents like Claude Code, Codex, Cursor, and Pi
- Add production features such as summarization, large-output offloading, parallel tools, tracing, and caching
- Reuse the same runtime from Go applications and from the Node.js SDK

## 🎯 What is MCPAgent?

MCPAgent is a general-purpose Go agent runtime. It gives you one agent abstraction that can:

- **Use MCP tools** across multiple servers and protocols (HTTP, SSE, stdio)
- **Run in multiple execution modes** with direct tool use and code execution
- **Connect to coding-agent CLIs** such as Claude Code, Codex, Cursor, and Pi
- **Route across model ecosystems** including OpenAI, Anthropic, OpenRouter, Bedrock, Vertex, Azure, MiniMax, and open-model gateways
- **Execute tools efficiently** with optional parallel tool calls, caching, and dynamic tool discovery
- **Stay productive in long sessions** with context summarization and large-output offloading
- **Support production workflows** with observability, custom tools, session reuse, and a Node.js SDK

If you only need MCP, the library does that well. If you need a broader agent runtime that can mix MCP, code execution, provider routing, coding agents, and workflow orchestration, that is the larger value of the project.

## ✅ Start Here

If you are evaluating the project for the first time, the Quick Start below is
the smallest working MCP-backed agent. From there, the `agent` package tests are
the maintained reference for real usage — they exercise construction, turns,
tool routing, and coding-agent transports against the current API.

The standalone `examples/` tree was removed: each example pinned its own module
and kept compatibility APIs public purely to stay compiling. Executable
behaviour now lives in tests that run in CI and cannot silently rot.

## 🚀 Quick Start

### Installation

```bash
# Add to your go.mod
go get github.com/manishiitg/mcpagent

# Or use replace directive for local development
replace github.com/manishiitg/mcpagent => ../mcpagent
```

### Basic Usage

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
)

func main() {
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		panic("OPENAI_API_KEY is required")
	}

	llmModel, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderOpenAI,
		ModelID:  "gpt-4o",
		APIKeys: &llm.ProviderAPIKeys{
			OpenAI: &openAIKey,
		},
	})
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	agent, err := mcpagent.NewAgentFromDefinition(ctx,
		mcpagent.AgentDefinition{
			Instructions: "You are a helpful assistant.",
			Tools: mcpagent.ToolSet{
				MCP: []mcpagent.MCPToolSource{{Name: "context7"}},
			},
		},
		mcpagent.RuntimeConfig{
			Model:         llmModel,
			MCPConfigPath: "mcp_servers.json",
		},
	)
	if err != nil {
		panic(err)
	}
	defer agent.Close()

	result, err := agent.Run(ctx, mcpagent.Turn{
		Input: "What tools are available?",
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(result.Text)
	fmt.Printf("tokens: %d\n", result.Usage.TotalTokens)
}
```

### The shape of the API

`NewAgentFromDefinition` is the only public constructor, and it takes exactly
two values:

- **`AgentDefinition`** — the agent's identity: `Instructions`, `Skills`, and
  `Tools`. These are cloned and validated at construction and never change
  afterwards.
- **`RuntimeConfig`** — the infrastructure that operates it: `Model`,
  `MCPConfigPath`, plus grouped `Generation`, `Tools`, `Context`, `Coding`,
  `MCP`, `Workspace`, and `Observability` settings.

Per-request concerns belong to the turn, not the agent. `Turn` carries `Input`,
optional `History`, a `ToolPolicy`, and an optional `StreamingCallback`; `Result`
returns `Text`, `History`, a resumable `Handle`, and structured `Usage`.

`*Agent` has exactly four methods — `Run`, `Start`, `Definition`, and `Close` —
and no exported fields. There are no `With*` option functions; anything that was
once an option is now a named field on `RuntimeConfig`.

### Multi-turn conversations

`Run` is the single-turn convenience path. When history must persist across
turns, open a session — it owns history, continuation, steering, and events:

```go
session, err := agent.Start(ctx)
if err != nil {
	panic(err)
}
defer session.Close()

first, err := session.Run(ctx, mcpagent.Turn{Input: "List the available tools."})
if err != nil {
	panic(err)
}

// History carries forward automatically; no need to thread it yourself.
second, err := session.Run(ctx, mcpagent.Turn{Input: "Now use the first one."})
if err != nil {
	panic(err)
}

fmt.Println(first.Text, second.Text)
```

`Run` calls on one session are serialized. Use separate sessions for
concurrency. `session.Snapshot()` returns a handle you can later pass as
`RuntimeConfig.ResumeHandle` to continue a conversation in a new process.

## 🟢 Node.js SDK

The official Node.js/TypeScript SDK provides a simple interface for building MCP agents in JavaScript/TypeScript applications. The SDK communicates with the Go server via **gRPC over Unix sockets** for low-latency, bidirectional streaming, and it can route through API providers as well as supported CLI-native providers.

### Installation

```bash
npm install @mcpagent/node
```

### Basic Usage

```typescript
import { MCPAgent } from '@mcpagent/node';

const agent = new MCPAgent({
  serverOptions: {
    mcpConfigPath: './mcp_servers.json',
    logLevel: 'info',
  },
});

// Initialize with your LLM provider
await agent.initialize({
  provider: 'codex-cli',
  modelId: 'high',
});

// Ask a question
const response = await agent.ask('What tools do you have available?');
console.log(response.response);

// Streaming responses
for await (const event of agent.askStream('Explain quantum computing')) {
  if (event.type === 'chunk') {
    process.stdout.write(event.text);
  } else if (event.type === 'final' && event.response) {
    console.log(event.response);
  }
}

// Cleanup
await agent.destroy();
```

### Custom Tools

Register JavaScript/TypeScript handlers that the LLM can call:

```typescript
import { MCPAgent } from '@mcpagent/node';

const agent = new MCPAgent({
  serverOptions: { mcpConfigPath: './mcp_servers.json' },
});

// Register a calculator tool
agent.registerTool(
  'calculate',
  'Perform a mathematical calculation',
  {
    type: 'object',
    properties: {
      expression: { type: 'string', description: 'Math expression to evaluate' },
    },
    required: ['expression'],
  },
  async (args) => {
    const result = Function(`"use strict"; return (${args.expression})`)();
    return String(result);
  },
  { timeoutMs: 5000 }
);

await agent.initialize({
  provider: 'vertex',
  modelId: 'gemini-3-flash-preview',
});

// The LLM can now use your custom tool
const response = await agent.ask('What is 15 * 7 + 23?');
// Output: 15 * 7 + 23 = 128
```

### Architecture

The Node.js SDK uses a **gRPC bidirectional streaming** architecture:

```
Node.js SDK ◄══════════════════════════════════► Go Server
            Single bidirectional gRPC stream
            - Client sends: questions, tool results
            - Server sends: text chunks, tool calls, events, final response
```

Benefits:
- **Real-time streaming**: Token-by-token responses via gRPC stream
- **Inline tool callbacks**: Custom tools execute in the same connection (no separate callback server)
- **Low latency**: Unix domain sockets for IPC
- **Type-safe**: Protocol Buffers for all messages

### SDK Examples

For SDK usage and complete examples, see [sdk-node/README.md](sdk-node/README.md).

## 📚 Core Features

### 1. **Standard Tool-Use Agent**

The default mode where the LLM invokes tools directly through native tool calling. Nothing needs to be enabled — it is what you get from a definition with no code-execution flag:

```go
agent, err := mcpagent.NewAgentFromDefinition(ctx,
    mcpagent.AgentDefinition{
        Instructions: "You are a helpful assistant.",
        Tools: mcpagent.ToolSet{
            MCP: []mcpagent.MCPToolSource{{Name: "context7"}},
        },
    },
    mcpagent.RuntimeConfig{Model: llmModel, MCPConfigPath: "config.json"},
)
```

### 2. **Code Execution Mode**

Execute code in **any language** (Python, bash, curl, Go, etc.) instead of JSON tool calls. The LLM discovers MCP tool endpoints via an OpenAPI spec and writes code that makes HTTP requests:

```go
// Generate API token for bearer auth
apiToken := executor.GenerateAPIToken()

// Start HTTP server with per-tool endpoints and auth
handlers := executor.NewExecutorHandlers(configPath, logger)
mux := http.NewServeMux()
mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
// Per-tool wildcard endpoints (used by OpenAPI spec)
mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
    // Route /tools/mcp/{server}/{tool} to handler
    path := strings.TrimPrefix(r.URL.Path, "/tools/mcp/")
    parts := strings.SplitN(path, "/", 2)
    server, tool := parts[0], parts[1]
    handlers.HandlePerToolMCPRequest(w, r, server, tool)
})
authedHandler := executor.AuthMiddleware(apiToken)(mux)

server := &http.Server{Addr: "127.0.0.1:8000", Handler: authedHandler}
go server.ListenAndServe()
defer server.Shutdown(ctx)

// Create agent with code execution mode
agent, err := mcpagent.NewAgentFromDefinition(ctx,
    definition,
    mcpagent.RuntimeConfig{
        Model:         llmModel,
        MCPConfigPath: "config.json",
        Tools:         mcpagent.ToolRuntimeConfig{CodeExecution: true},
        MCP: mcpagent.MCPRuntimeConfig{
            APIBaseURL: "http://127.0.0.1:8000",
            APIToken:   apiToken,
        },
    },
)
```

The LLM calls `get_api_spec(tool_name)` to discover per-tool HTTP endpoints, then uses `execute_shell_command` to write and run code that calls those endpoints. Custom tools (workspace tools, shell execution) remain as direct tool calls.

`tool_name` accepts a single name or an array, and is the only required argument — the tool name is the address. `server_name` is optional and used solely to disambiguate a real MCP server; built-in tools resolve by name alone.

**Note**: Code execution mode requires an HTTP server with bearer token auth running (configured via `RuntimeConfig.MCP.APIBaseURL` and `APIToken`).

### 3. **Context Offloading**

Context offloading is a context engineering strategy that automatically saves large tool outputs to the filesystem instead of keeping them in the LLM's context window. This implements the **"offload context"** pattern, one of three primary context engineering approaches used in production agents like [Manus](https://rlancemartin.github.io/2025/10/15/manus/).

**Why Context Offloading?**

As agents execute tasks, tool call results accumulate in the context window. Research from [Chroma](https://www.trychroma.com/blog/context-rot) and [Anthropic](https://docs.anthropic.com/claude/docs/context-editing) shows that as context windows fill, LLM performance degrades due to attention budget depletion. Context offloading prevents this by:

- **Saving tokens**: Only file path + preview (~200 chars) instead of full content (potentially 50k+ chars)
- **Preventing context overflow**: Large outputs don't consume context window space
- **Maintaining performance**: LLM attention budget isn't depleted by large payloads
- **Enabling efficient exploration**: Agent can access data incrementally as needed

**How It Works:**

```go
offloading := true

agent, err := mcpagent.NewAgentFromDefinition(ctx, definition,
    mcpagent.RuntimeConfig{
        Model:         llmModel,
        MCPConfigPath: "config.json",
        Context: mcpagent.ContextRuntimeConfig{
            Offloading:           &offloading,
            LargeOutputThreshold: 10000, // tokens (default)
        },
    },
)
```

When tool outputs exceed the threshold:

1. **External Storage**: Full content is saved to `tool_output_folder/{session-id}/` with unique filenames
2. **Compact Reference**: LLM receives file path + preview (first 50% of threshold) instead of full content
3. **On-Demand Access**: Agent uses `search_large_output` with `read`, `search`, or `query` operations to access data incrementally.

**Example Token Savings:**

```
Without Context Offloading:
- Tool Output: 50,000 characters (~12,500 tokens)
- Sent to LLM: 50,000 chars (~12,500 tokens)
- Result: Context window overflow, attention budget depletion

With Context Offloading:
- Tool Output: 50,000 characters (~12,500 tokens)
- Saved to filesystem: 50,000 chars
- Sent to LLM: ~200 chars (file path + preview) (~50 tokens)
- Result: 99.6% token reduction, no context overflow

Note: The threshold is measured in tokens (using tiktoken encoding), not characters.
A threshold of 10000 tokens roughly equals ~40,000 characters (assuming ~4 chars per token).
```

**Related Patterns:**

This implementation follows the context engineering strategies outlined in [Manus's approach](https://rlancemartin.github.io/2025/10/15/manus/):

- **Offload Context**: Store tool results externally, access on-demand ✅ **Implemented**
- **Reduce Context**: Compact stale results, summarize when needed ⏳ **Pending**
- **Isolate Context**: Use sub-agents for discrete tasks (multi-agent support)

Similar patterns are used in Claude Code, LangChain, and other production agent systems.

**Pending: Dynamic Context Reduction**

Currently, context offloading only applies to large tool outputs when they're first generated. A future enhancement will implement **dynamic context reduction** to compact stale tool results as the context window fills, even if they weren't initially large.

**What's Pending:**

1. **Compact Stale Results**
   - **Concept**: Replace older tool results with compact references (e.g., file paths) as context fills
   - **Behavior**: Keep recent tool results in full to guide the agent's next decision, while older results are replaced with references
   - **Implementation**: Automatically detect when tool results become "stale" (based on age, relevance, or context usage) and replace them with compact references
   - **Scope**: This would apply to ALL tool results (not just large ones), dynamically compacting them when they become "stale"
   - **Reference**: Similar to [Anthropic's context editing feature](https://docs.anthropic.com/claude/docs/context-editing)
   - **Example**: A 2000-token tool result from 10 turns ago becomes: `"Tool: search_docs returned results (saved to: tool_output_folder/session-123/search_20250101_120000.json)"`

2. **Summarize When Needed**
   - **Concept**: Once compaction reaches diminishing returns, apply schema-based summarization to the full trajectory
   - **Behavior**: Generate consistent summary objects using full tool results, further reducing context while preserving essential information
   - **Implementation**: When compaction alone isn't enough to manage context size, apply structured summarization with predefined schemas for different tool result types
   - **Scope**: Summarize the entire conversation trajectory when individual compaction is insufficient
   - **Example**: Instead of keeping 20 tool calls with full results, create a structured summary:
     ```json
     {
       "tool_calls_summary": [
         {"tool": "search", "count": 5, "key_findings": ["..."], "files": ["..."]},
         {"tool": "read_file", "count": 3, "files_read": ["..."]}
       ]
     }
     ```

**Current Behavior vs. Future Enhancement:**

```
Current (Context Offloading):
- Large output (>10k tokens) → Offloaded immediately
- Small output (<10k tokens) → Stays in context forever
- Result: Context can still fill up with many small tool results

Future (Context Reduction):
- Large output (>10k tokens) → Offloaded immediately ✅
- Small output (<10k tokens) → Stays in context initially
- As context fills → Small outputs become "stale" → Compacted to references
- When compaction insufficient → Summarize trajectory
- Result: Context window stays manageable throughout long conversations
```

This enhancement would complete the "Reduce Context" strategy from [Manus's context engineering approach](https://rlancemartin.github.io/2025/10/15/manus/), working alongside context offloading to maintain optimal context window usage.

Context offloading is exercised end-to-end by the `search_large_output` tests in
the `agent` package.

### 4. **Context Summarization**

Automatically summarize conversation history when token usage exceeds a threshold to maintain long-running conversations:

```go
agent, err := mcpagent.NewAgentFromDefinition(ctx, definition,
    mcpagent.RuntimeConfig{
        Model:         llmModel,
        MCPConfigPath: "config.json",
        Context: mcpagent.ContextRuntimeConfig{
            SummarizationEnabled: true,
            // Trigger when token usage reaches 70% of the context window
            SummarizeOnTokenThreshold: true,
            TokenThresholdPercent:     0.7,
            // Keep the last 8 messages intact
            SummaryKeepLastMessages: 8,
        },
    },
)
```

The agent monitors token usage and automatically replaces older messages with a concise LLM-generated summary when the threshold is reached, while preserving recent messages and tool call integrity. This enables "infinite" conversation depth within fixed context windows.

### 5. **MCP Server Caching**

Intelligent caching reduces connection times by 60-85%:

```go
// Caching is enabled by default
// Configure via environment variables:
// MCP_CACHE_DIR=/path/to/cache
// MCP_CACHE_TTL_MINUTES=10080 (7 days)
```

### 6. **Custom Tools**

Register your own tools that work alongside MCP server tools. Custom tools work in both standard mode and code execution mode:

**Standard Mode** (direct tool calls):
```go
// Define tool parameters (JSON schema)
params := map[string]interface{}{
    "type": "object",
    "properties": map[string]interface{}{
        "operation": map[string]interface{}{
            "type": "string",
            "enum": []string{"add", "subtract", "multiply", "divide"},
        },
        "a": map[string]interface{}{"type": "number"},
        "b": map[string]interface{}{"type": "number"},
    },
    "required": []string{"operation", "a", "b"},
}

// Declare the tool as part of the agent's identity
definition := mcpagent.AgentDefinition{
    Instructions: "You are a helpful assistant.",
    Tools: mcpagent.ToolSet{
        Direct: []mcpagent.ToolDefinition{{
            Name:         "calculator",
            Description:  "Performs mathematical operations",
            InputSchema:  params,
            Execute:      calculatorFunction,
            DisplayGroup: "utility", // optional presentation metadata
        }},
    },
}

// Tool execution function
func calculatorFunction(ctx context.Context, args map[string]interface{}) (string, error) {
    // Extract and validate arguments
    operation := args["operation"].(string)
    a := args["a"].(float64)
    b := args["b"].(float64)
    
    // Perform calculation
    var result float64
    switch operation {
    case "add": result = a + b
    case "subtract": result = a - b
    // ...
    }
    
    return fmt.Sprintf("Result: %.2f", result), nil
}
```

**Code Execution Mode** (direct tool calls + HTTP API):
```go
// In code execution mode, custom tools are:
// 1. Exposed as direct LLM tool calls (e.g., execute_shell_command, workspace tools)
// 2. MCP server tools are accessed via HTTP API endpoints (discovered via get_api_spec)
// 3. Custom tools can also be accessed via /api/custom/execute endpoint

// Declared exactly the same way — there is no separate code-execution API
definition := mcpagent.AgentDefinition{
    Tools: mcpagent.ToolSet{
        Direct: []mcpagent.ToolDefinition{{
            Name:        "get_weather",
            Description: "Gets weather data for a location",
            InputSchema: weatherParams,
            Execute:     weatherFunction,
        }},
    },
}

// LLM can call custom tools directly as tool calls,
// or use get_api_spec to discover HTTP endpoints for MCP tools
```

Custom tools behave the same in standard and code-execution mode: they are
registered on the `AgentDefinition` and are addressed by their globally unique
tool name. In code-execution mode they are additionally reachable over the HTTP
API described above.

### 7. **Parallel Tool Execution**

When the LLM returns multiple tool calls in a single response, they can be executed concurrently using goroutines (fork-join pattern) instead of sequentially:

```go
agent, err := mcpagent.NewAgentFromDefinition(ctx, definition,
    mcpagent.RuntimeConfig{
        Model:         llmModel,
        MCPConfigPath: "config.json",
        Tools:         mcpagent.ToolRuntimeConfig{ParallelExecution: true},
    },
)
```

**How it works:**
1. LLM returns N tool calls in one response
2. All tool calls are prepared sequentially (argument parsing, client resolution)
3. Tool calls execute concurrently via goroutines
4. Results are collected in deterministic order matching the original tool call order

**Observability:** `ToolCallStartEvent` includes an `IsParallel` field (`true` when the tool call is part of a parallel batch, `false` for sequential execution) so event listeners and tracers can distinguish between parallel and sequential tool calls.

### 8. **Observability**

Built-in tracing with Langfuse support:

```go
tracer, err := observability.NewLangfuseTracerWithLogger(logger)
if err != nil {
    return err
}
agent, err := mcpagent.NewAgentFromDefinition(ctx, definition,
    mcpagent.RuntimeConfig{
        Model:         llmModel,
        MCPConfigPath: "config.json",
        Observability: mcpagent.ObservabilityRuntimeConfig{
            Tracers: []observability.Tracer{tracer},
            TraceID: "trace-id",
            Logger:  logger,
        },
    },
)
```

## 📖 Documentation

Comprehensive documentation is available in the [docs/](docs/) directory:

- **[OAuth Authentication](docs/oauth.md)** - OAuth 2.0 authentication for MCP servers
- **[Code Execution Agent](docs/code_execution_agent.md)** - Execute code in any language via OpenAPI spec
- **[Tool-Use Agent](docs/tool_use_agent.md)** - Standard tool calling mode
- **[Context Summarization](docs/context_summarization.md)** - Automatic history summarization
- **[Context Offloading](docs/large_output_handling.md)** - Offload large tool outputs to filesystem (offload context pattern)
  - Implements the "offload context" strategy from [Manus's context engineering approach](https://rlancemartin.github.io/2025/10/15/manus/)
  - Prevents context window overflow and reduces token costs
  - Enables efficient on-demand data access via virtual tools
- **[MCP Cache System](docs/mcp_cache_system.md)** - Server metadata caching
- **[Folder Guard](docs/folder_guard.md)** - Fine-grained file access control
- **[LLM Resilience](docs/llm_resilience.md)** - Error handling and fallbacks
- **[Event System](docs/event_type_generation.md)** - Event architecture
- **[Parallel Tool Execution](docs/parallel_tool_execution.md)** - Concurrent tool call execution
- **[Token Tracking](docs/token-usage-tracking.md)** - Usage monitoring

## 📝 Reference Usage

The standalone `examples/` tree has been removed. Each example was its own Go
module, and keeping them compiling required holding constructors and option
functions public long after the library itself had stopped needing them — the
demonstrations were dictating the API surface.

Maintained usage now lives in the `agent` package tests, which run in CI against
the current public API:

- agent construction through `NewAgentFromDefinition` and `AgentDefinition`
- multi-turn conversations, history, and context summarization
- custom tool registration and tool filtering
- code-execution mode, including `get_api_spec` resolution and the HTTP tool API
- coding-agent providers (Claude Code, Codex CLI) over the MCP bridge
- context offloading via `search_large_output`

For the Node.js SDK, see [sdk-node/README.md](sdk-node/README.md).

## 🔧 Configuration

### MCP Server Configuration

Create a JSON file with your MCP servers:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "./demo"]
    },
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"]
    }
  }
}
```

### Runtime Configuration

Runtime settings are named fields on `RuntimeConfig`, grouped by purpose. There
are no functional options — the grouping is what replaced them, so configuration
is one explicit value rather than an order-sensitive list:

```go
offloading := true

agent, err := mcpagent.NewAgentFromDefinition(ctx, definition,
    mcpagent.RuntimeConfig{
        Model:         llmModel,
        MCPConfigPath: "config.json",

        Generation: mcpagent.GenerationRuntimeConfig{
            MaxTurns:    30,
            Temperature: 0.7,
            ToolChoice:  "auto",
        },

        Tools: mcpagent.ToolRuntimeConfig{
            CodeExecution:     true,
            ParallelExecution: true,
            SelectedTools:     []string{"tool1", "tool2"},
            SelectedServers:   []string{"server1", "server2"},
        },

        Context: mcpagent.ContextRuntimeConfig{
            Offloading:                &offloading,
            LargeOutputThreshold:      10000,
            SummarizationEnabled:      true,
            SummarizeOnTokenThreshold: true,
            TokenThresholdPercent:     0.7,
        },

        Observability: mcpagent.ObservabilityRuntimeConfig{
            Tracers: []observability.Tracer{tracer},
            TraceID: traceID,
            Logger:  logger,
        },
    },
)
```

Custom tools are part of the definition, not registered afterwards — an agent's
tools are fixed once it exists:

```go
definition := mcpagent.AgentDefinition{
    Instructions: "You are a helpful assistant.",
    Tools: mcpagent.ToolSet{
        Direct: []mcpagent.ToolDefinition{{
            Name:        "calculate",
            Description: "Evaluate a arithmetic expression",
            InputSchema: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "expression": map[string]interface{}{"type": "string"},
                },
                "required": []string{"expression"},
            },
            Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
                return evaluate(args["expression"].(string))
            },
        }},
        MCP: []mcpagent.MCPToolSource{{Name: "context7"}},
    },
}
```

Tools are addressed by their globally unique `Name`. `DisplayGroup` is optional
presentation metadata only — it takes no part in addressing or authorization.

To narrow which tools a single request may use without rebuilding the agent, set
`Turn.ToolPolicy.AllowedTools`. An empty slice means every tool in the definition
is allowed.

// Folder guard paths are set on the created agent instance
agent.SetFolderGuardPaths(allowedRead, allowedWrite)
```

## 🧪 Testing

The package includes comprehensive testing utilities:

```bash
# Run all tests
cd cmd/testing
go test ./...

# Run specific test
go run testing.go agent-mcp --log-file logs/test.log
go run testing.go code-exec --log-file logs/test.log
go run testing.go parallel-tool-exec --provider vertex --model gemini-3-flash-preview
```

See [cmd/testing/README.md](cmd/testing/README.md) for details.

## 📁 Package Structure

```
mcpagent/
├── agent/              # Core agent implementation
│   ├── agent.go       # Core Agent struct and runtime
│   ├── definition.go  # AgentDefinition, RuntimeConfig, NewAgentFromDefinition()
│   ├── turn_session.go # Turn/Result, Session, and the four Agent methods
│   ├── conversation.go # Conversation loop and tool execution
│   ├── connection_session.go # Session-scoped MCP connection management
│   └── ...
├── grpcserver/        # gRPC server (for SDK communication)
│   ├── server.go      # gRPC server setup
│   ├── service.go     # AgentService implementation
│   ├── stream_handler.go # Bidirectional stream handling
│   └── pb/            # Generated protobuf code
├── mcpclient/         # MCP client implementations
│   ├── client.go       # Client interface and implementations
│   ├── stdio_manager.go # stdio protocol
│   ├── sse_manager.go  # SSE protocol
│   └── http_manager.go # HTTP protocol
├── mcpcache/          # Caching system
│   ├── manager.go     # Cache manager
│   └── openapi/       # OpenAPI spec generation for code execution mode
├── llm/               # LLM provider integration
│   ├── providers.go   # Provider implementations
│   └── types.go       # LLM types
├── events/            # Event system
│   ├── data.go        # Event data structures
│   └── types.go       # Event types
├── logger/             # Logging
│   └── v2/            # Logger v2 interface
├── observability/     # Tracing and observability
│   ├── tracer.go      # Tracer interface
│   └── langfuse_tracer.go # Langfuse implementation
├── executor/          # Tool execution handlers
├── sdk-node/          # Node.js/TypeScript SDK
│   ├── src/           # SDK source code
│   │   ├── agent.ts   # MCPAgent class
│   │   ├── grpc-client.ts # gRPC client
│   │   └── stream-handler.ts # Stream management
│   └── README.md      # SDK documentation
├── proto/             # Protocol Buffer definitions
│   └── agent.proto    # gRPC service definitions
└── docs/              # Documentation
```

## 🔌 Supported LLM Providers

- **OpenAI**: GPT-4.1, GPT-4o, reasoning models, and compatible tool-calling models
- **Anthropic**: Claude models through direct provider integration
- **OpenRouter**: Access to open and frontier models behind a unified API
- **AWS Bedrock**: Claude, Llama, Mistral, and other Bedrock-served models
- **Google Vertex AI**: Gemini and related Vertex-hosted models
- **Azure**: Azure-hosted OpenAI and related model deployments
- **Claude Code / Codex / Cursor / Pi CLI providers**: Coding-agent integrations through provider abstractions
- **MiniMax**: MiniMax chat and coding-plan providers
- **Custom Providers**: Extensible provider interface

## 🔌 Supported MCP Protocols

MCP remains an important integration layer in the runtime, with support for:

- **stdio**: Standard input/output (most common)
- **SSE**: Server-Sent Events
- **HTTP**: REST API

## 🤝 Contributing

Contributions are welcome! Please see the [Documentation Writing Guide](docs/doc_writing_guide.md) for standards.

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- **MCP Protocol**: Built on the [Model Context Protocol](https://modelcontextprotocol.io/)
- **multi-llm-provider-go**: LLM provider abstraction layer
- **mcp-go**: MCP protocol implementation
- **Context Engineering**: Context offloading implementation inspired by [Manus's context engineering strategies](https://rlancemartin.github.io/2025/10/15/manus/)

---

**Made with ❤️ for the AI community**
