# Code Execution Mode with Browser Automation

This example demonstrates how to use the MCP Agent with **code execution mode** and **browser automation** capabilities. The agent automatically writes and executes Python code to perform web research and analysis tasks using the Playwright MCP server.

## Features

- **Code Execution Mode**: The agent writes Python code instead of making JSON tool calls
- **Browser Automation**: Uses `@playwright/mcp` for web browsing and data extraction
- **OpenAPI Discovery**: The agent discovers browser tools via `get_api_spec`
- **Web Research**: Automatically navigates websites, searches, and extracts information
- **Analysis Tasks**: Performs complex research and analysis using browser tools
- **Multi-turn Conversations**: Uses `AskWithHistory` for maintaining conversation context

## How It Works

1. **Code Execution Mode**: When enabled, the LLM writes Python code instead of making direct JSON tool calls
2. **Tool Discovery**: The agent calls `get_api_spec` to get OpenAPI specs for browser tools
3. **Python Execution**: Agent writes Python code using `requests` to call per-tool HTTP endpoints
4. **HTTP Server**: A local HTTP server handles API calls from the Python code
5. **Browser Tools**: The Python code can use Playwright tools to:
   - Navigate to URLs
   - Search the web
   - Extract page content
   - Take snapshots
   - Interact with web elements

## Setup

1. Create `.env` file with your OpenAI API key:
   ```
   OPENAI_API_KEY=your-api-key-here
   ```

2. Initialize the Go module:
   ```bash
   cd examples/code_execution/browser-automation
   go mod tidy
   ```

3. Ensure Node.js is installed (required for `npx` to run the Playwright MCP server):
   ```bash
   node --version  # Should be v18 or higher
   ```

4. (Optional) Configure HTTP server port:
   ```bash
   export MCP_API_URL=8000  # or http://localhost:8000
   ```
   The default port is 8000. The HTTP server is required for code execution mode.

## Usage

```bash
# Run with default IPO analysis task
go run main.go

# Run with custom config file
go run main.go mcp_servers.json

# Run with custom task
go run main.go mcp_servers.json "Search for the latest AI news and summarize the top 5 articles"
```

## Default Task

The default task performs a comprehensive analysis of the last 10 popular IPOs in India:

1. **Search**: Uses browser automation to search for recent Indian IPOs
2. **Data Collection**: Navigates to relevant websites and extracts IPO information
3. **Analysis**: Identifies patterns such as sector distribution, pricing trends, performance metrics
4. **Reporting**: Presents findings in a structured format

**You don't need to ask the agent to write code** - it will automatically use code execution mode when appropriate.

## Code Execution Mode

In code execution mode:

- The agent **writes Python code** to accomplish tasks
- Python code calls MCP tools via **per-tool HTTP endpoints** (`/tools/mcp/{server}/{tool}`)
- Code uses **bearer token auth** (`MCP_API_TOKEN` environment variable)
- An **HTTP server** handles API calls from the Python code
- All code and execution logs are saved for debugging

## Example Use Cases

- **Market Research**: Analyze trends in specific markets or industries
- **Competitive Analysis**: Research competitors and market positioning
- **Data Collection**: Gather information from multiple sources
- **Content Analysis**: Extract and analyze content from websites
- **Web Scraping**: Automatically extract structured data from websites

## Configuration

The `mcp_servers.json` file configures the Playwright MCP server:

```json
{
  "mcpServers": {
    "playwright": {
      "command": "npx",
      "args": ["@playwright/mcp@latest"]
    }
  }
}
```

## HTTP Server

Code execution mode requires a local HTTP server to handle API calls from Python code:

- **Default Port**: 8000
- **Configurable**: Set `MCP_API_URL` environment variable to change port
- **Endpoints**:
  - `/tools/mcp/{server}/{tool}` - Execute MCP tool calls (bearer token auth)
  - `/tools/custom/{tool}` - Execute custom tool calls (bearer token auth)

The server starts automatically when you run the example.

## Requirements

- Go 1.24.4+
- Node.js 18+ (for npx-based MCP servers)
- OpenAI API key in `.env` file
- Internet connection (for web browsing)

## Differences from Regular Browser Automation

| Feature | Regular Browser Automation | Code Execution Mode |
|---------|---------------------------|---------------------|
| Tool Calls | Direct JSON tool calls | Python code via HTTP API |
| Execution | Immediate tool execution | Code written then executed |
| Debugging | Tool call logs | Full code + execution logs |
| Flexibility | Limited to tool capabilities | Can combine tools with custom Python logic |
| Performance | Faster for simple tasks | Better for complex workflows |

## Notes

- Browser automation tasks may take longer than simple API calls
- The default timeout is set to 15 minutes to allow for complex research
- The browser MCP server will automatically handle web interactions
- Some websites may have rate limiting or require authentication

## Logging

The example includes comprehensive logging:

- **LLM Logs**: `logs/llm.log` - API calls, token usage, responses
- **Agent Logs**: `logs/browser-automation-code-execution.log` - Code execution, tool calls, errors
