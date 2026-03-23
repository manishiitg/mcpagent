# Gemini CLI to Claude Code Fallback Example

This example shows how to start with `gemini-cli` and configure `claude-code` as a cross-provider fallback.

## What It Does

- Uses `gemini-cli` as the primary provider
- Configures `claude-code` as the fallback provider with `claude-haiku-4-5`
- Starts a local executor API for bridge-backed tool access
- Exposes MCP tools through `mcpbridge`
- Prints the final provider/model used after the answer
- Prints cumulative token usage

## Requirements

- Gemini CLI installed and available in `PATH`
- Claude Code CLI installed and available in `PATH`
- Gemini CLI authenticated locally or `GEMINI_API_KEY` configured
- Claude Code authenticated locally or `ANTHROPIC_API_KEY` configured
- Go 1.24.4+
- Node.js if your configured MCP servers need `npx`

## Run

```bash
cd examples/basic_gemini_cli_fallback_claude_code
go run main.go
```

Custom question:

```bash
go run main.go mcp_servers.json "Get me the documentation for React hooks"
```

Force fallback for testing:

```bash
FORCE_FALLBACK=1 go run main.go
```

When `FORCE_FALLBACK=1` is set, the example uses an intentionally invalid Gemini model name so the request falls through to Claude Code.

## Notes

- If `mcpbridge` is not already installed, this example builds a local copy into `generated/mcpbridge`
- The example uses the same `context7` MCP config shape as the regular `basic` example
- The executor API listens on a random localhost port and is wired into the bridge automatically
- The final provider/model section lets you confirm whether the answer came from Gemini CLI or the Claude Code fallback
