# Backlog

Known gaps and design questions that are understood but not yet acted on. Each
entry states what is true today (with the code that makes it true), why it
matters, and what changing it would involve — so picking one up doesn't start
from re-investigation.

---

## Adding MCP servers beyond the bridge, for coding agents

**Question:** how do we give a coding-agent session more MCP servers than
`api-bridge`?

### What's true today

There are two distinct paths, and they behave very differently:

**1. Non-coding (API) agents — already supports N servers.**
`NewAgent(ctx, llm, configPath, ...)` takes a general MCP config file
(`mcp_servers.json`). It supports any number of servers, merges a base config
with the user's, and connects them lazily on first tool call. Nothing to build
here.

**2. Coding agents (Claude Code / Codex / Cursor / Pi) — exactly ONE server,
hardcoded.**
`BuildBridgeMCPConfig` (`agent/coding_agents_bridge.go`) generates the config
handed to the CLI, and it is a fixed single-entry map:

```go
config := map[string]interface{}{
    "mcpServers": map[string]interface{}{
        "api-bridge": map[string]interface{}{ /* ... */ },
    },
}
```

There is no parameter, option, or merge step that can add a second server. So a
coding-agent session natively sees one MCP server, always.

This is a deliberate design, not an oversight — the bridge exposes a small
native tool set (`execute_shell_command`, `diff_patch_workspace_file`,
`agent_browser`, `get_api_spec`), and **every other MCP tool is reached
indirectly**: the agent calls `get_api_spec` to discover a server's schema, then
invokes it over HTTP through `execute_shell_command`. Keeping the native tool
list tiny is what makes bridge-only containment enforceable and keeps large tool
schemas out of the CLI's context.

### Why it may still need to change

The indirect path costs a round trip and real prompt complexity (the agent must
discover, then hand-construct an HTTP call), and it is noticeably weaker for
servers whose value is a rich typed schema. A caller who genuinely wants a
first-class native MCP server in a coding session currently cannot have one.

### What changing it would involve

- Extend `BuildBridgeMCPConfig` to merge caller-supplied server entries into the
  generated `mcpServers` map, behind an explicit option (e.g.
  `WithAdditionalMCPServers`), rather than widening the default.
- Decide the containment story first. Bridge-only enforcement assumes the
  bridge is the ONLY route to the host — a second MCP server with any
  code-execution capability silently defeats it. This is not hypothetical: it is
  exactly how Codex escaped `--disable shell_tool` (it used an available
  `node_repl` MCP to `child_process` out). Any additional-server option needs to
  state plainly whether the session is still contained.
- Per-provider config plumbing already differs (Codex takes a TOML profile,
  Cursor/Pi take `.cursor/mcp.json`-shaped JSON, Claude takes `--mcp-config`),
  so the merge has to happen before that per-provider shaping, not after.

**Status:** not started. Raised 2026-07-25.
