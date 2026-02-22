# Code Execution Mode with Custom Tools

This example demonstrates how to use **custom tools** in **code execution mode**. In this mode, the agent writes and executes Python code to call tools via HTTP API instead of making direct JSON tool calls. Custom tools are accessible via per-tool HTTP endpoints.

## Key Features

1. **Code Execution Mode**: The agent writes and executes Python code automatically
2. **Custom Tools**: Register custom Go functions as tools callable via HTTP API
3. **HTTP Server**: Local HTTP server handles API calls from Python code
4. **Multi-Turn Conversations**: Uses `AskWithHistory` for maintaining conversation context

## How It Works

### Code Execution Mode

When code execution mode is enabled:
- The agent sees virtual tools: `get_api_spec` and `execute_shell_command`
- MCP tools and custom tools are **NOT** directly exposed to the LLM
- The agent writes Python code that calls tools via per-tool HTTP endpoints
- Python code uses bearer token auth via `MCP_API_TOKEN` environment variable

### Custom Tools in Code Execution Mode

Custom tools work in code execution mode via HTTP API:

1. **Registration**: Custom tools are registered using `agent.RegisterCustomTool()`
2. **Discovery**: LLM can discover custom tools via `get_api_spec`
3. **Execution**: Python code calls custom tools via `POST /tools/custom/{tool}` HTTP endpoint
4. **Access**: Custom tools are accessible through Python HTTP requests, not direct LLM tool calls

### Example Custom Tools

This example includes four custom tools:

1. **calculator**: Performs mathematical operations (add, subtract, multiply, divide, power, sqrt)
2. **format_text**: Formats text (uppercase, lowercase, reverse, title case)
3. **get_weather**: Gets simulated weather data for a location
4. **count_text**: Counts characters, words, sentences, or paragraphs in text

## Setup

### Prerequisites

- Go 1.24.4 or later
- Docker (for MCP servers)
- Python with `uvx` (for time server)
- OpenAI API key

### Environment Variables

Create a `.env` file or set environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export MCP_API_URL="8000"  # Optional: HTTP server port (default: 8000)
```

### Installation

1. Navigate to this directory:
```bash
cd mcpagent/examples/code_execution/custom_tools
```

2. Install dependencies:
```bash
go mod tidy
```

3. Run the example:
```bash
go run main.go
```

## Usage

The example will:
1. Start an HTTP server on port 8000 (or the port specified in `MCP_API_URL`)
2. Register custom tools
3. Process example questions that trigger code execution
4. Generate and execute Python code that uses custom tools via HTTP API

### Example Questions

The example includes questions like:
- "Calculate 15 multiplied by 23, then format the result as text in uppercase"
- "Get the weather for San Francisco in fahrenheit, then count the number of words in the weather description"
- "Calculate the square root of 144, then format 'The answer is' followed by the result in title case"

These questions will cause the agent to:
1. Call `get_api_spec` to discover the custom tool endpoints
2. Write Python code that calls custom tools via HTTP API
3. Execute the code and return the results

## How Custom Tools Work in Code Execution Mode

### Registration

```go
agent.RegisterCustomTool(
    "calculator",
    "Performs basic mathematical operations",
    calculatorParams,  // JSON schema
    calculatorTool,    // Execution function
    "utility",         // Category
)
```

### LLM-Generated Python Code

The agent generates Python code like:

```python
import requests, os, json

url = os.environ["MCP_API_URL"] + "/tools/custom/calculator"
headers = {
    "Authorization": f"Bearer {os.environ['MCP_API_TOKEN']}",
    "Content-Type": "application/json"
}
resp = requests.post(url, json={"operation": "multiply", "a": 15, "b": 23}, headers=headers)
data = resp.json()
print(data["result"] if data.get("success") else f"Error: {data.get('error')}")
```

### Execution Flow

1. Agent receives question
2. Agent calls `get_api_spec` to discover custom tool endpoints
3. Agent writes Python code using `execute_shell_command`
4. Python code calls custom tools via `POST /tools/custom/{tool}`
5. HTTP server routes to custom tool execution function
6. Result is returned to Python code
7. Python code returns final result

## Differences from Normal Mode

| Feature | Normal Mode | Code Execution Mode |
|---------|-------------|---------------------|
| Custom Tools | Directly exposed to LLM | Accessible via Python HTTP requests |
| Tool Calls | JSON tool calls | Python code execution |
| Execution | Synchronous tool calls | Via per-tool HTTP endpoints |
| Discovery | Tools in LLM context | Via `get_api_spec` OpenAPI specs |

## Logging

Logs are written to:
- `logs/llm.log`: LLM API calls and token usage
- `logs/custom-tools-code-execution.log`: Agent operations, code generation, and execution

## Troubleshooting

### Port Already in Use

If port 8000 is already in use, set a different port:
```bash
export MCP_API_URL="8001"
go run main.go
```

### Custom Tool Not Found

Ensure custom tools are registered **before** calling `agent.Ask()` or `agent.AskWithHistory()`.

### Code Execution Errors

Check the agent log file for detailed error messages. Common issues:
- HTTP server not running
- Invalid tool parameters
- Missing bearer token (`MCP_API_TOKEN` not set)

## Next Steps

- Add more custom tools for your specific use case
- Combine custom tools with MCP server tools
- Use custom tools for domain-specific operations
- Implement complex workflows using multiple custom tools
