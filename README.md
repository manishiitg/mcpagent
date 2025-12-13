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
- **Structured Output**: Get structured data from LLM responses using fixed conversion or tool-based methods
- **Custom Tools**: Register your own tools with the agent for extended functionality
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

See [examples/](examples/) for complete working examples:

- **[basic/](examples/basic/)** - Basic agent setup with single MCP server
- **[multi-turn/](examples/multi-turn/)** - Multi-turn conversations with history
- **[multi-mcp-server/](examples/multi-mcp-server/)** - Connect to multiple MCP servers
- **[browser-automation/](examples/browser-automation/)** - Browser automation with Playwright
- **[structured_output/](examples/structured_output/)** - Structured output examples
  - **[fixed/](examples/structured_output/fixed/)** - Fixed conversion model (2 LLM calls)
  - **[tool/](examples/structured_output/tool/)** - Tool-based model (1 LLM call)
- **[custom_tools/](examples/custom_tools/)** - Register and use custom tools
- **[code_execution/](examples/code_execution/)** - Code execution mode examples
  - **[simple/](examples/code_execution/simple/)** - Basic code execution (no folder guards)
  - **[browser-automation/](examples/code_execution/browser-automation/)** - Code execution with browser automation
  - **[multi-mcp-server/](examples/code_execution/multi-mcp-server/)** - Code execution with tool filtering
  - **[custom_tools/](examples/code_execution/custom_tools/)** - Custom tools in code execution mode

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
// Start HTTP server for tool execution (required)
handlers := executor.NewExecutorHandlers(configPath, logger)
mux := http.NewServeMux()
mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)

server := &http.Server{Addr: "127.0.0.1:8000", Handler: mux}
go server.ListenAndServe()
defer server.Shutdown(ctx)

// Create agent with code execution mode
agent, err := mcpagent.NewAgent(
    ctx, llmModel, "", "config.json", "model-id",
    nil, "", nil,
    mcpagent.WithCodeExecutionMode(true),
    mcpagent.SetFolderGuardPaths([]string{"/workspace"}, []string{"/workspace"}),
)
```

The LLM can write Go programs that import and use MCP tools and custom tools as native functions. Custom tools are automatically generated as Go packages and accessible via HTTP API.

**Note**: Code execution mode requires an HTTP server running (default port 8000, configurable via `MCP_API_URL` environment variable).

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

### 6. **Structured Output**

Get structured data from LLM responses in two ways:

**Fixed Conversion Model** (2 LLM calls - reliable):
```go
type Person struct {
    Name  string `json:"name"`
    Age   int    `json:"age"`
    Email string `json:"email"`
}

person, err := agent.AskStructured[Person](
    ctx,
    "Create a person profile for John Doe, age 30, email john@example.com",
    Person{},
    schemaString,
)
```

**Tool-Based Model** (1 LLM call - faster):
```go
result, err := agent.AskWithHistoryStructuredViaTool[Order](
    ctx,
    messages,
    "submit_order",
    "Submit an order with items",
    orderSchema,
)
if result.HasStructuredOutput {
    order := result.StructuredResult
    // Use structured order data
}
```

See [examples/structured_output/](examples/structured_output/) for complete examples.

### 7. **Custom Tools**

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

// Register the tool
err := agent.RegisterCustomTool(
    "calculator",
    "Performs mathematical operations",
    params,
    calculatorFunction,
    "utility", // category (required)
)

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

**Code Execution Mode** (via generated Go code):
```go
// In code execution mode, custom tools are automatically:
// 1. Generated as Go packages (e.g., data_tools, utility_tools)
// 2. Accessible via HTTP API endpoint (/api/custom/execute)
// 3. Included in tool structure JSON for LLM discovery

// Register custom tool (same API)
err := agent.RegisterCustomTool(
    "get_weather",
    "Gets weather data for a location",
    weatherParams,
    weatherFunction,
    "data", // category
)

// LLM can use it in generated Go code:
// import "data_tools"
// result := data_tools.GetWeather(data_tools.GetWeatherParams{
//     Location: "San Francisco",
//     Unit: "fahrenheit",
// })
```

See [examples/custom_tools/](examples/custom_tools/) for standard mode examples and [examples/code_execution/custom_tools/](examples/code_execution/custom_tools/) for code execution mode examples.

### 8. **Observability**

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

## 📝 Examples

Complete working examples are available in the [examples/](examples/) directory:

### Basic Examples
- **[basic/](examples/basic/)** - Simple agent setup with a single MCP server
- **[multi-turn/](examples/multi-turn/)** - Multi-turn conversations with conversation history

### Advanced Examples
- **[multi-mcp-server/](examples/multi-mcp-server/)** - Connect to multiple MCP servers simultaneously
- **[browser-automation/](examples/browser-automation/)** - Browser automation using Playwright MCP server

### Structured Output Examples
- **[structured_output/fixed/](examples/structured_output/fixed/)** - Fixed conversion model for structured output
  - Uses `AskStructured()` method
  - 2 LLM calls (text response + JSON conversion)
  - More reliable, works with complex schemas
  
- **[structured_output/tool/](examples/structured_output/tool/)** - Tool-based model for structured output
  - Uses `AskWithHistoryStructuredViaTool()` method
  - 1 LLM call (tool call during conversation)
  - Faster and more cost-effective

### Custom Tools Example
- **[custom_tools/](examples/custom_tools/)** - Register and use custom tools
  - Register multiple custom tools with different categories
  - Tools work alongside MCP server tools
  - Examples: calculator, text formatter, weather simulator, text counter

### Code Execution Examples
- **[code_execution/simple/](examples/code_execution/simple/)** - Basic code execution mode
  - LLM writes and executes Go code instead of JSON tool calls
  - MCP tools are auto-generated as Go packages
  - Supports complex logic: loops, conditionals, data transformations
  - Security features: AST validation, isolated execution
  - Examples: discover code files, use multiple MCP servers together
  - No folder guards (simplest example)
  - HTTP server required (default port 8000)

- **[code_execution/browser-automation/](examples/code_execution/browser-automation/)** - Code execution with browser automation
  - Combines code execution mode with Playwright MCP server
  - Complex multi-step browser automation tasks
  - Example: IPO analysis with web scraping and data collection

- **[code_execution/multi-mcp-server/](examples/code_execution/multi-mcp-server/)** - Code execution with tool filtering
  - Demonstrates tool filtering in code execution mode
  - Uses `WithSelectedTools()` and `WithSelectedServers()` to filter available tools
  - Example: Selective tool access across multiple MCP servers

- **[code_execution/custom_tools/](examples/code_execution/custom_tools/)** - Custom tools in code execution mode
  - Register custom tools that work in code execution mode
  - Custom tools are auto-generated as Go packages
  - Accessible via HTTP API endpoint (`/api/custom/execute`)
  - Example: Weather tool accessible via generated Go code

Each example includes:
- Complete working code
- README with detailed documentation
- MCP server configuration
- Setup instructions

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
    
    // Custom tool registration (after agent creation)
    // agent.RegisterCustomTool(name, description, params, execFunc, category)
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

