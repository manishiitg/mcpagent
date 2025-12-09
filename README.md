# MCP Agent - Go Library

[![Go Version](https://img.shields.io/badge/Go-1.24.4-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A **production-ready Go library** for building MCP (Model Context Protocol) agents that connect to multiple MCP servers and execute tools using LLMs. This is a fully independent package that can be used in any Go application.

## 🎯 What is MCP Agent?

MCP Agent is a Go library that provides a complete framework for building AI agents that interact with MCP servers. It handles:

- **Multi-Server MCP Connections**: Connect to multiple MCP servers simultaneously (HTTP, SSE, stdio protocols)
- **LLM Integration**: Works with OpenAI, AWS Bedrock, Google Vertex AI, and other LLM providers
- **Tool Execution**: Automatic tool discovery, execution, and result handling
- **Code Execution Mode**: Execute Go code instead of JSON tool calls for complex workflows
- **Smart Routing**: Dynamically filter tools based on conversation context
- **Large Output Handling**: Automatically handle tool outputs that exceed context limits
- **Observability**: Built-in tracing with Langfuse support
- **Caching**: Intelligent caching of MCP server metadata and tool definitions

## 🚀 Quick Start

### Installation

```bash
# Add to your go.mod
go get mcpagent

# Or use replace directive for local development
replace mcpagent => ../mcpagent
```

### Basic Usage

```go
package main

import (
    "context"
    "time"
    
    mcpagent "mcpagent/agent"
    "mcpagent/llm"
)

func main() {
    // Initialize LLM
    llmModel, err := llm.InitializeLLM(llm.Config{
        Provider: llm.ProviderOpenAI,
        ModelID:  "gpt-4.1",
        APIKeys: &llm.ProviderAPIKeys{
            OpenAI: &openAIKey,
        },
    })
    
    // Create agent
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    
    agent, err := mcpagent.NewAgent(
        ctx,
        llmModel,
        "",              // server name (empty = all servers)
        "mcp_servers.json", // MCP config path
        "gpt-4.1",       // model ID
        nil,             // tracer (optional)
        "",              // trace ID
        nil,             // logger (optional)
    )
    
    // Ask a question
    response, err := agent.Ask(ctx, "What tools are available?")
    fmt.Println(response)
}
```

See [examples/](examples/) for complete working examples.

## 📚 Core Features

### 1. **Standard Tool-Use Agent**

The default mode where the LLM invokes tools directly through native tool calling:

```go
agent, err := mcpagent.NewAgent(
    ctx, llmModel, "", "config.json", "model-id",
    nil, "", nil,
    mcpagent.WithMode(mcpagent.SimpleAgent),
)
```

### 2. **Code Execution Mode**

Execute Go code instead of JSON tool calls for complex logic:

```go
agent, err := mcpagent.NewAgent(
    ctx, llmModel, "", "config.json", "model-id",
    nil, "", nil,
    mcpagent.WithCodeExecutionMode(true),
    mcpagent.SetFolderGuardPaths([]string{"/workspace"}, []string{"/workspace"}),
)
```

The LLM can write Go programs that import and use MCP tools as native functions.

### 3. **Smart Routing**

Dynamically filter tools based on conversation context to reduce token usage:

```go
agent, err := mcpagent.NewAgent(
    ctx, llmModel, "", "config.json", "model-id",
    nil, "", nil,
    mcpagent.WithSmartRouting(true),
    mcpagent.WithSmartRoutingThresholds(20, 3), // max tools, max servers
)
```

### 4. **Large Output Handling**

Automatically handle tool outputs that exceed context limits:

```go
agent, err := mcpagent.NewAgent(
    ctx, llmModel, "", "config.json", "model-id",
    nil, "", nil,
    mcpagent.WithLargeOutputVirtualTools(true),
    mcpagent.WithLargeOutputThreshold(20000), // characters
)
```

### 5. **MCP Server Caching**

Intelligent caching reduces connection times by 60-85%:

```go
// Caching is enabled by default
// Configure via environment variables:
// MCP_CACHE_DIR=/path/to/cache
// MCP_CACHE_TTL_MINUTES=10080 (7 days)
```

### 6. **Observability**

Built-in tracing with Langfuse support:

```go
tracer := observability.NewLangfuseTracer(...)
agent, err := mcpagent.NewAgent(
    ctx, llmModel, "", "config.json", "model-id",
    tracer, "trace-id", logger,
)
```

## 📖 Documentation

Comprehensive documentation is available in the [docs/](docs/) directory:

- **[Code Execution Agent](docs/code_execution_agent.md)** - Execute Go code with MCP tools
- **[Tool-Use Agent](docs/tool_use_agent.md)** - Standard tool calling mode
- **[Smart Routing](docs/smart_routing.md)** - Dynamic tool filtering
- **[Large Output Handling](docs/large_output_handling.md)** - Handle large tool outputs
- **[MCP Cache System](docs/mcp_cache_system.md)** - Server metadata caching
- **[Folder Guard](docs/folder_guard.md)** - Fine-grained file access control
- **[LLM Resilience](docs/llm_resilience.md)** - Error handling and fallbacks
- **[Event System](docs/event_type_generation.md)** - Event architecture
- **[Token Tracking](docs/token-usage-tracking.md)** - Usage monitoring

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

### Agent Options

The agent supports extensive configuration via functional options:

```go
agent, err := mcpagent.NewAgent(
    ctx, llmModel, "", "config.json", "model-id",
    tracer, traceID, logger,
    // Agent mode
    mcpagent.WithMode(mcpagent.SimpleAgent),
    
    // Conversation settings
    mcpagent.WithMaxTurns(30),
    mcpagent.WithTemperature(0.7),
    mcpagent.WithToolChoice("auto"),
    
    // Code execution
    mcpagent.WithCodeExecutionMode(true),
    mcpagent.SetFolderGuardPaths(allowedRead, allowedWrite),
    
    // Smart routing
    mcpagent.WithSmartRouting(true),
    mcpagent.WithSmartRoutingThresholds(20, 3),
    
    // Large output handling
    mcpagent.WithLargeOutputVirtualTools(true),
    mcpagent.WithLargeOutputThreshold(20000),
    
    // Custom tools
    mcpagent.WithCustomTools(customTools),
    
    // Tool selection
    mcpagent.WithSelectedTools([]string{"server1:tool1", "server2:*"}),
    mcpagent.WithSelectedServers([]string{"server1", "server2"}),
)
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
go run testing.go smart-routing --log-file logs/test.log
```

See [cmd/testing/README.md](cmd/testing/README.md) for details.

## 📁 Package Structure

```
mcpagent/
├── agent/              # Core agent implementation
│   ├── agent.go       # Main Agent struct and NewAgent()
│   ├── conversation.go # Conversation loop and tool execution
│   ├── connection.go   # MCP server connection management
│   └── ...
├── mcpclient/         # MCP client implementations
│   ├── client.go       # Client interface and implementations
│   ├── stdio_manager.go # stdio protocol
│   ├── sse_manager.go  # SSE protocol
│   └── http_manager.go # HTTP protocol
├── mcpcache/          # Caching system
│   ├── manager.go     # Cache manager
│   └── codegen/       # Code generation for tools
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
├── examples/          # Example applications
└── docs/              # Documentation
```

## 🔌 Supported LLM Providers

- **OpenAI**: GPT-4, GPT-3.5, and other models
- **AWS Bedrock**: Claude Sonnet, Claude Haiku, and other models
- **Google Vertex AI**: Gemini, PaLM, and other models
- **Custom Providers**: Extensible provider interface

## 🔌 Supported MCP Protocols

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

---

**Made with ❤️ for the AI community**

