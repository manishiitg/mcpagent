# Multi-MCP Server Example

This example demonstrates how to use the MCP Agent with multiple MCP servers working together in a focused, easy-to-follow workflow.

The default path is intentionally biased toward a reliable three-server setup: `playwright`, `sequential-thinking`, and `context7`.
It also exposes only a small subset of tools so the run stays fast and deterministic.

## Features

- **Multiple MCP Servers**: Uses multiple MCP servers simultaneously, with a reliable default trio:
  - **Playwright**: Browser automation for web browsing and data extraction
  - **Sequential Thinking**: Advanced reasoning and step-by-step analysis
  - **Context7**: Library documentation and code reference access
- **Minimal Tool Surface**: Restricts the example to the exact tools needed for the demo
- **Cross-Server Collaboration**: Demonstrates how different servers can work together
- **Complex Task Execution**: Performs tasks that require multiple capabilities
- **Default Task**: Opens the CNCF homepage, identifies cloud-native themes, organizes them with sequential thinking, and supports the write-up with Context7 documentation for Kubernetes

## Setup

1. Create `.env` file with your OpenAI API key:
   ```
   OPENAI_API_KEY=your-api-key-here
   ```

2. Prepare the example module:
   ```bash
   cd examples/multi-mcp-server
   go mod tidy
   ```

3. Ensure required tools are installed:
   - **Node.js** (v18 or higher) - required for `npx` to run Playwright and Sequential Thinking servers
   ```bash
   node --version  # Should be v18 or higher
   ```

## Usage

```bash
# Run with the default focused multi-server task
go run main.go

# Run with custom config file
go run main.go mcp_servers.json

# Run with custom task
go run main.go mcp_servers.json "Open a public product page, analyze the main themes, and support the summary with Context7 docs"
```

## Default Task

The default task demonstrates a clean multi-server collaboration flow:

1. **Browser Navigation** (Playwright): Opens the CNCF homepage
2. **Page Snapshot** (Playwright): Captures a single structured snapshot of the page
3. **Analysis** (Sequential Thinking): Organizes the visible themes into a concise summary
4. **Technical Support** (Context7): Pulls supporting Kubernetes documentation as a representative cloud-native technology
5. **Reporting**: Presents the final answer as key insights plus practical takeaways

## How It Works

1. The agent initializes with the selected MCP servers
2. The example intentionally exposes only a minimal tool set:
   - **Playwright**: `browser_navigate`, `browser_snapshot`
   - **Sequential Thinking**: `sequentialthinking`
   - **Context7**: `resolve-library-id`, `query-docs`
3. The LLM orchestrates tools from different servers to complete the task
4. Results are compiled from multiple sources and presented as a concise analysis

## Configuration

The bundled `mcp_servers.json` file keeps the example focused on the three default servers:

```json
{
  "mcpServers": {
    "playwright": {
      "command": "npx",
      "args": ["@playwright/mcp@latest"]
    },
    "sequential-thinking": {
      "command": "npx",
      "args": ["--yes", "@modelcontextprotocol/server-sequential-thinking"]
    },
    "context7": {
      "command": "",
      "args": null,
      "protocol": "http",
      "url": "https://mcp.context7.com/mcp"
    }
  }
}
```

## Example Use Cases

- **Research & Analysis**: Use browser automation to gather source material, sequential thinking to analyze it, and Context7 to support technical claims
- **Development Workflows**: Use Context7 for library docs, Playwright for testing, and sequential thinking for synthesis
- **Market Research**: Combine web research with analytical reasoning and cloud cost analysis
- **Technical Analysis**: Gather information from multiple sources and synthesize insights

## Requirements

- Go 1.24.4+
- Node.js 18+ (for npx-based MCP servers)
- OpenAI API key in `.env` file
- Internet connection (for web browsing and API access)

## Notes

- Multi-server tasks may take longer than single-server operations
- The default timeout is set to 15 minutes to allow for complex multi-step operations
- Each server runs independently and can be used in parallel when appropriate
- You can expand the config with more servers later, but the bundled example is intentionally narrow for a more reliable first run
- The agent automatically routes requests to the appropriate tool based on the task

## Server Details

### Playwright
- Provides browser automation capabilities
- Can navigate websites, extract content, interact with elements
- Useful for web scraping and research

### Sequential Thinking
- Provides advanced reasoning capabilities
- Breaks down complex problems into steps
- Useful for analysis and problem-solving

### Context7
- Provides access to library documentation
- Can search and retrieve code examples
- Useful for development and technical reference

## Output

At the end of the run, the example prints:

- The final synthesized response
- Prompt tokens
- Completion tokens
- Total tokens
- Cache tokens
- Reasoning tokens
- LLM call count
- Cache-enabled call count
