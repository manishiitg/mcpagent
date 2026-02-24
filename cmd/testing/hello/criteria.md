# Hello Test - Log Analysis Criteria

This test validates basic Claude Code integration and multi-MCP server usage.

## Running the Test

```bash
# IMPORTANT: If running inside a Claude Code session, unset the env var first:
unset CLAUDECODE

# Basic run with claude-code provider
mcpagent-test hello --provider claude-code

# With debug logging
mcpagent-test hello --provider claude-code --log-level debug

# Save logs to file
mcpagent-test hello --provider claude-code --log-file logs/hello-test.log

# Hello only (skip multi-MCP)
mcpagent-test hello --provider claude-code --skip-mcp
```

### CLAUDECODE Environment Variable

When running inside a Claude Code session (e.g. during development), the `CLAUDECODE` environment variable is set. The Claude CLI detects this and refuses to start a nested session:

```
Error: Claude Code cannot be launched inside another Claude Code session.
Nested sessions share runtime resources and will crash all active sessions.
To bypass this check, unset the CLAUDECODE environment variable.
```

**Fix:** `unset CLAUDECODE` before running the test.

## What This Test Does

### Test 1: Hello World
1. Creates an LLM with the specified provider (default: claude-code)
2. Creates a minimal agent (no MCP servers)
3. If provider is claude-code, verifies auto-disable logic ran
4. Sends "Say hello in one short sentence."
5. Validates response is non-empty and contains a greeting

### Test 2: Multi-MCP Server
1. Creates an LLM with the specified provider
2. Creates a temp MCP config with 2 servers:
   - `sequential-thinking` (npx, stdio)
   - `context7` (HTTP)
3. Creates an agent with those MCP servers
4. Sends a question requiring sequential thinking tool usage
5. Validates response length and content

## Log Analysis Checklist

### Hello Test
- [ ] LLM created with correct provider
- [ ] Agent created successfully
- [ ] Auto-disable check ran (if claude-code provider)
- [ ] `Got response` log shows non-empty response
- [ ] Response contains greeting ("hello", "hi", "hey")
- [ ] `Hello test passed` appears

### Multi-MCP Test
- [ ] MCP config created with 2 servers
- [ ] Agent created with MCP servers
- [ ] `Got response` shows response > 50 chars
- [ ] Response contains MCP-related content
- [ ] `Multi-MCP test passed` appears

### What to look for in logs
```
# Success indicators:
"Agent created successfully"
"Hello test passed"
"Agent created with MCP servers"
"Multi-MCP test passed"
"All hello tests passed"

# Auto-disable (claude-code only):
"[CLAUDE_CODE] Disabled Context Offloading"

# Failure indicators:
"Claude Code cannot be launched inside another Claude Code session"  -> unset CLAUDECODE
"response is empty"
"agent.Ask failed"
```

## Expected Test Outcome

Both tests pass, final output shows:
```
All hello tests passed
```

## Troubleshooting

| Issue | Fix |
|-------|-----|
| "Cannot be launched inside another Claude Code session" | `unset CLAUDECODE` before running |
| "OPENAI_API_KEY is required" | Use `--provider claude-code` or set the key |
| MCP server connection timeout | Check that `npx` and network access are available |
| Response too short | May indicate MCP tools weren't called; check server logs |
