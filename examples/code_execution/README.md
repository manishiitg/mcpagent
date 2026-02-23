# Code Execution Mode Examples

This directory contains multiple examples demonstrating **Code Execution Mode**, where the LLM writes and executes Python/bash code to call MCP tools via HTTP API instead of making JSON-based tool calls.

## Available Examples

### 1. **[simple/](simple/)** - Basic Code Execution

The simplest code execution example without folder guards or additional security features.

**Features:**
- Basic code execution mode setup
- Multiple MCP servers (playwright, sequential-thinking, context7, etc.)
- Python code generation and execution via `execute_shell_command`
- OpenAPI spec discovery via `get_api_spec`

**Use this when:**
- You want to understand the basics of code execution mode
- You don't need file system restrictions
- You want to see how MCP tools become HTTP API endpoints

### 2. **[browser-automation/](browser-automation/)** - Code Execution with Browser Automation

Code execution mode combined with browser automation using Playwright MCP server.

**Features:**
- Code execution mode with browser automation
- Playwright MCP server for web browsing
- Automatic Python code generation for web tasks
- Multi-turn conversations with `AskWithHistory`
- Default task: IPO analysis from Indian financial websites

**Use this when:**
- You want to combine code execution with web automation
- You need to perform complex web research tasks
- You want to see how browser tools work via Python HTTP calls
- You're building web scraping or research automation

### 3. **[multi-mcp-server/](multi-mcp-server/)** - Code Execution with Tool Filtering

Code execution mode with multiple MCP servers and selective tool filtering.

**Features:**
- Code execution mode with tool filtering
- Multiple MCP servers (playwright, sequential-thinking, context7, aws-knowledge-mcp, google-sheets, gmail, everything)
- Selective tool access:
  - Specific tools from servers (e.g., only `read_email` and `search_emails` from gmail)
  - All tools from other servers
- Multi-turn conversations with `AskWithHistory`
- Demonstrates how filters work in code execution mode

**Use this when:**
- You want to restrict which tools are available to the agent
- You need to use multiple MCP servers with selective access
- You want to understand how tool filtering works in code execution mode
- You're building applications that need controlled tool access

### 4. **[custom_tools/](custom_tools/)** - Code Execution with Custom Tools

Code execution mode with custom Go functions registered as tools.

**Features:**
- Code execution mode with custom tools
- Register custom Go functions as tools
- Custom tools accessible via per-tool HTTP endpoints
- HTTP API for custom tool execution
- Example custom tools: calculator, text formatter, weather simulator, text counter
- Multi-turn conversations with `AskWithHistory`

**Use this when:**
- You want to add domain-specific tools not available in MCP servers
- You need custom business logic as tools
- You want to see how custom tools work in code execution mode
- You're building applications with specialized tool requirements

## What is Code Execution Mode?

Code Execution Mode allows the LLM to:
- **Write Python code** instead of making individual tool calls
- **Discover tools via OpenAPI specs** using `get_api_spec`
- **Execute complex logic** - loops, conditionals, data transformations in Python
- **Chain multiple operations** - multiple tool calls in a single script execution
- **Call MCP tools via HTTP** - per-tool endpoints at `/tools/mcp/{server}/{tool}`

## How It Works

1. **Tool Index**: A JSON index of available servers and tools is embedded in the system prompt
2. **Discovery**: LLM calls `get_api_spec(server_name, tool_name)` to get OpenAPI YAML spec for a tool
3. **Code Writing**: LLM writes Python code using `requests` to call per-tool HTTP endpoints
4. **Execution**: LLM calls `execute_shell_command` to run the Python code
5. **Authentication**: Python code uses `MCP_API_TOKEN` env var for bearer token auth
6. **Results**: Per-tool endpoint executes the MCP tool and returns JSON results

## Key Features

### Per-Tool HTTP Endpoints

MCP tools are exposed as individual HTTP endpoints:

```
POST /tools/mcp/context7/resolve_library_id
POST /tools/mcp/google_sheets/create_spreadsheet
POST /tools/custom/calculator
```

### Virtual Tools

In code execution mode, only these tools are directly available to the LLM:
- `get_api_spec` - Discover tool endpoints via OpenAPI specs
- `execute_shell_command` - Execute Python/bash code

MCP tools are accessed via Python HTTP requests to per-tool endpoints (not direct tool calls).

### Security Features

- **Bearer Token Auth**: Per-tool endpoints require `Authorization: Bearer <token>` header
- **Environment Variables**: `MCP_API_URL` and `MCP_API_TOKEN` injected into shell environment
- **Timeout Protection**: Code execution has a timeout (default 30s)

## Quick Start

1. Choose an example directory (e.g., `simple/`, `custom_tools/`)
2. Create `.env` file with your OpenAI API key:
   ```
   OPENAI_API_KEY=your-api-key-here
   ```
3. Install dependencies:
   ```bash
   cd simple  # or custom_tools, etc.
   go mod tidy
   ```
4. Run the example:
   ```bash
   go run main.go
   ```

## Example Code Pattern (LLM-Generated Python)

```python
import requests, os, json

url = os.environ["MCP_API_URL"] + "/tools/mcp/context7/resolve_library_id"
headers = {
    "Authorization": f"Bearer {os.environ['MCP_API_TOKEN']}",
    "Content-Type": "application/json"
}
resp = requests.post(url, json={"library_name": "react"}, headers=headers)
data = resp.json()
if data.get("success"):
    print("Result:", data["result"])
else:
    print("Error:", data.get("error"))
```

## Documentation

For more details, see:
- [Code Execution Agent Documentation](../../docs/code_execution_agent.md)
- [Folder Guard Documentation](../../docs/folder_guard.md)

## Next Steps

- Review logs for detailed execution traces
- Try writing more complex Python programs that chain multiple MCP tools
- Combine multiple MCP servers in a single Python script
