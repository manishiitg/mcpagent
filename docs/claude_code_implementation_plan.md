# Claude Code CLI Integration Implementation Plan

## Goal
Integrate the Claude Code CLI adapter into the MCP Agent system (`mcpagent`), enabling the agent to use the local `claude` CLI as an LLM provider (`ProviderClaudeCode`) with native tool capabilities while disabling conflicting agent-side features.

## Architecture

1.  **Provider Registration**:
    *   Map `ProviderClaudeCode` to "claude-code" in `multi-llm-provider-go` and `mcpagent/llm`.
    *   This allows the system to recognize "claude-code" as a valid provider.

2.  **Configuration Passing**:
    *   The `Claude Code` CLI requires MCP server configuration to be passed as a JSON string via the `--mcp-config` flag.
    *   We implement `GetMCPConfigJSON()` in `mcpagent/agent/agent.go` to serialize the agent's current MCP configuration.
    *   We inject this config into the LLM execution call via `WithMCPConfig` option.

3.  **Feature Auto-Disable**:
    *   The `Claude Code` CLI handles its own context management, tool discovery, and code execution.
    *   The `mcpagent`'s built-in implementations of these features (Tool Search, Code Execution, Context Editing, Context Offloading) conflict or are redundant.
    *   We automatically disable these features in `NewAgent` when `ProviderClaudeCode` is detected.

## Implementation Details

### 1. `multi-llm-provider-go`
*   Modified `pkg/adapters/claudecode/adapter.go` to accept `WithMCPConfig` option.
*   Modified `providers.go` to export `WithMCPConfig`.

### 2. `mcpagent`
*   **`llm/providers.go`**: Added `ProviderClaudeCode`.
*   **`agent/agent.go`**:
    *   Added `GetMCPConfigJSON()` method.
    *   **CRITICAL**: Added auto-disable logic in `NewAgent` (and `NewAgentWithObservability`) to set `UseToolSearchMode`, `UseCodeExecutionMode`, etc., to `false`.
*   **`agent/llm_generation.go`**: Updated `executeLLM` to inject the MCP config.

## Testing Strategy

### Integration Test (`cmd/testing/claude-code`)
*   **Command**: `mcpagent-test test claude-code`
*   **Steps**:
    1.  Create a mock MCP configuration.
    2.  Initialize an agent with `ProviderClaudeCode`.
    3.  Explicitly enable incompatible features (e.g., `UseToolSearchMode = true`) in `NewAgent` call.
    4.  Verify that `NewAgent` automatically disables these features (assert `UseToolSearchMode == false`).
    5.  Verify `GetMCPConfigJSON` returns valid JSON matching the mock config.

## Known Limitations

### HTTP/SSE MCP Servers
Currently, the `claude` CLI cannot dynamically bootstrap remote HTTP/SSE based MCP servers via the `--mcp-config` JSON string argument when running in headless/non-interactive mode (`-p`). 

*   **Behavior**: Injecting an HTTP configuration (e.g. `{"type": "http", "url": "..."}`) into `--mcp-config` causes the non-interactive CLI to hang indefinitely and eventually time out.
*   **Root Cause**: The CLI's internal MCP engine is optimized for spawning local `stdio` command sub-processes (like `npx`). While the CLI supports HTTP MCPs natively, it currently expects them to be configured interactively via the standard interactive prompt (e.g. `claude mcp add --transport http ...`).
*   **Workaround**: Tests and automated usage of the `ProviderClaudeCode` integration must utilize `stdio` based local servers (such as `@modelcontextprotocol/server-memory`) rather than remote HTTP servers.

## Current Status & Issues

### Issue: Test Failure (Resolved)
The integration test previously failed because the auto-disable logic was misplaced in `NewAgentWithObservability` instead of `NewAgent`.

### Root Cause Analysis (Resolved)
*   The logic was inserted in the wrong constructor (`NewAgentWithObservability`).
*   The test calls `mcpagent.NewAgent`.
*   We have now moved the logic to the end of `NewAgent` function.

### Next Steps
1.  Verify the fix by running the test.
2.  Commit and push changes.
