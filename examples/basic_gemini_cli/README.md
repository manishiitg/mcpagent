# Basic Gemini CLI Example

Simple example showing how to use `mcpagent` with the Gemini CLI provider and MCP tools exposed through the `mcpbridge` layer.

## What It Does

- Initializes the `gemini` CLI as the model provider
- Uses the faster `flash-lite` model by default
- Starts a local executor API for bridge-backed tool access
- Exposes MCP tools to Gemini CLI through `mcpbridge`
- Asks a basic question: "Get me the documentation for React library"
- Prints cumulative token usage after the final answer

## Requirements

- Gemini CLI installed and available in `PATH`
- Gemini CLI authenticated locally or `GEMINI_API_KEY` configured
- Go 1.24.4+
- Node.js if your configured MCP servers need `npx`

## Run

```bash
cd examples/basic_gemini_cli
go run main.go
```

Custom question:

```bash
go run main.go mcp_servers.json "Get me the documentation for React hooks"
```

## Notes

- If `mcpbridge` is not already installed, this example builds a local copy into `generated/mcpbridge`
- The example uses the same `context7` MCP config shape as the regular `basic` example
- The executor API listens on a random localhost port and is wired into the bridge automatically
- Token usage is printed with prompt, completion, total, cache, reasoning, and LLM call counts
