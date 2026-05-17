# Workflow Model Routing Example

Run the same MCP-backed task against Kimi, MiniMax M2.7, or GLM-5.1.

This example is meant for testing workflow-runtime behavior rather than only checking whether a model can answer a prompt. It prints the final response plus token/call counters so you can compare:

- wall-clock time
- LLM call count
- token usage
- whether the model terminates cleanly
- whether the model repeats tool calls
- output quality for the same task

## Requirements

- Go 1.24.4+
- Node.js if your MCP servers need `npx`
- One or more provider keys:
  - `KIMI_API_KEY`
  - `MINIMAX_API_KEY`
  - `ZAI_API_KEY`

## Run

```bash
cd examples/workflow_model_routing

# Kimi K2.6
go run main.go kimi

# MiniMax M2.7
go run main.go minimax

# GLM-5.1
go run main.go glm
```

Custom prompt:

```bash
go run main.go glm mcp_servers.json "Research React Server Components and return a concise implementation checklist."
```

## Provider Mapping

| Argument | Provider | Default model | API key |
|---|---|---|---|
| `kimi` | `kimi` | `kimi-k2.6` | `KIMI_API_KEY` |
| `minimax` | `minimax` | `MiniMax-M2.7` | `MINIMAX_API_KEY` |
| `glm` / `zai` | `z-ai` | `glm-5.1` | `ZAI_API_KEY` |

## Notes

- The included MCP config uses Context7 over HTTP.
- This is intentionally small. It gives developers a direct place to compare model behavior without setting up the full Runloop product.
- For CLI-native coding-agent flows, see:
  - `../basic_claude_code`
  - `../basic_codex_cli`
  - `../basic_gemini_cli`
  - `../basic_gemini_cli_fallback_claude_code`
