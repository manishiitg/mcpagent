# Simple Code Execution Example

This example demonstrates **Code Execution Mode** in its simplest form, where the LLM writes and executes Python code to call MCP tools via HTTP API instead of making JSON-based tool calls.

**This is the basic example without folder guards or additional security features.**

## What is Code Execution Mode?

Code Execution Mode is a powerful feature that allows the LLM to:
- **Write Python code** instead of making individual tool calls
- **Discover tools via OpenAPI specs** - tools are exposed as per-tool HTTP endpoints
- **Execute complex logic** - loops, conditionals, data transformations in Python
- **Chain multiple operations** - multiple tool calls in a single script execution

## How It Works

1. **Tool Index**: A JSON index of servers and tools is embedded in the system prompt
2. **Discovery**: LLM calls `get_api_spec(server_name, tool_name)` to get OpenAPI YAML spec
3. **Code Writing**: LLM writes Python code using `requests` library to call per-tool HTTP endpoints
4. **Execution**: LLM calls `execute_shell_command` to run the Python code
5. **Authentication**: Bearer token auth via `MCP_API_TOKEN` environment variable
6. **Results**: Per-tool endpoint executes the MCP tool and returns JSON

## Methods Demonstrated

- `mcpagent.WithCodeExecutionMode(true)` - Enable code execution mode
- `agent.Ask(ctx, question)` - LLM will use code execution tools

## Key Features

### 1. **Per-Tool HTTP Endpoints**

MCP tools are exposed as individual HTTP endpoints:

```
POST /tools/mcp/fetch/fetch_url
POST /tools/mcp/time/get_current_time
POST /tools/custom/calculator
```

### 2. **Virtual Tools**

In code execution mode, only these tools are available to the LLM:
- `get_api_spec` - Discover tool endpoints via OpenAPI specs
- `execute_shell_command` - Execute Python/bash code

MCP tools are NOT directly available - they must be called via Python HTTP requests.

### 3. **Security Features**

- **Bearer Token Auth**: Per-tool endpoints require `Authorization: Bearer <token>` header
- **Environment Variables**: `MCP_API_URL` and `MCP_API_TOKEN` injected into shell environment
- **Timeout Protection**: Code execution has a timeout (default 30s)

## Setup

1. Create `.env` file with your OpenAI API key:
   ```
   OPENAI_API_KEY=your-api-key-here
   ```

2. Install dependencies:
   ```bash
   go mod tidy
   ```

3. Run the example:
   ```bash
   go run main.go
   ```

4. Or with custom questions:
   ```bash
   go run main.go mcp_servers.json "Fetch a URL and save the content"
   ```

## Important: HTTP Server Requirement

**Code execution mode requires an HTTP server running** (default port 8000).

The example automatically starts an HTTP server that handles API calls from Python code:
- **Port**: `8000` (default, configurable via `MCP_API_URL` environment variable)
- **Endpoints**:
  - `/tools/mcp/{server}/{tool}` - For MCP tool execution (bearer token auth)
  - `/tools/custom/{tool}` - For custom tool execution (bearer token auth)
  - `/api/mcp/execute` - Legacy MCP tool execution
  - `/api/custom/execute` - Legacy custom tool execution

**Configuring the Port:**

```bash
# Use a different port
export MCP_API_URL=http://localhost:9000
go run main.go
```

**Note:** If port 8000 is already in use, either stop the service using port 8000 or set `MCP_API_URL` to a different port.

The Python code makes HTTP POST requests to these per-tool endpoints. The server is started automatically when you run the example.

## Example Usage

The example demonstrates several scenarios. **You don't need to ask the agent to write code** - it will automatically use code execution mode when appropriate:

1. **Simple Request**: "Get me the documentation for React library"
   - Agent automatically writes Python code to call context7 API

2. **Complex Thinking**: "Think through the problem: How can I improve performance of a web application?"
   - Agent uses sequential-thinking server via Python HTTP calls

3. **Multi-Server Operations**: "Get React documentation and analyze the key concepts using sequential thinking"
   - Agent combines multiple MCP servers in a single Python script

## Code Execution Flow

### Step 1: LLM Discovers Available Tools

```
LLM calls get_api_spec(server_name="fetch", tool_name="fetch_url")
→ Returns OpenAPI YAML spec with endpoint, parameters, auth details
```

### Step 2: LLM Writes Python Code

```python
import requests, os, json

url = os.environ["MCP_API_URL"] + "/tools/mcp/fetch/fetch_url"
headers = {
    "Authorization": f"Bearer {os.environ['MCP_API_TOKEN']}",
    "Content-Type": "application/json"
}

resp = requests.post(url, json={"url": "https://example.com"}, headers=headers)
data = resp.json()

if data.get("success"):
    print(data["result"])
else:
    print(f"Error: {data.get('error')}")
```

### Step 3: LLM Executes Code

```
LLM calls execute_shell_command with the Python script
→ Python code makes HTTP POST to per-tool endpoint
→ Per-tool endpoint executes the MCP tool
→ Result returned to LLM
```

## Response Format

Per-tool endpoints return:
```json
{
  "success": true,
  "result": "..."
}
```

Or on error:
```json
{
  "success": false,
  "error": "error message"
}
```

## Key Differences from Standard Mode

| Feature | Standard Mode | Code Execution Mode |
|---------|--------------|---------------------|
| **Tool Calls** | JSON tool calls | Python code execution |
| **Available Tools** | All MCP tools directly | Only `get_api_spec`, `execute_shell_command` |
| **MCP Tools** | Direct LLM tool calls | Via Python HTTP requests to per-tool endpoints |
| **Complex Logic** | Limited to tool chaining | Full Python language features |
| **Performance** | Multiple API round-trips | Single script with multiple HTTP calls |

## When to Use Code Execution Mode

**Use Code Execution Mode when:**
- You need complex logic (loops, conditionals, data transformations)
- You want to chain multiple operations efficiently
- You need to process/aggregate data from multiple tool calls
- You want to reduce LLM round-trips (single script vs multiple tool calls)

**Use Standard Mode when:**
- Simple tool calls are sufficient
- You want direct tool visibility to the LLM
- You need faster iteration (no code generation step)
- You prefer JSON-based tool calls

## Logs

The example creates log files in the `logs/` directory:
- `llm.log` - LLM API calls and token usage
- `code-execution.log` - Agent operations, code generation, and execution

## Troubleshooting

### "connection refused"
- Ensure HTTP server is running (started automatically by example)
- Check port number matches `MCP_API_URL`

### "401 Unauthorized"
- Bearer token mismatch — check `MCP_API_TOKEN` env var

### "tool not found"
- Verify server name and tool name in the API URL path
- Check that MCP server is configured and connected

## Documentation

For more details, see:
- [Code Execution Agent Documentation](../../docs/code_execution_agent.md)
- [Folder Guard Documentation](../../docs/folder_guard.md)
