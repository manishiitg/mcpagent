# Multi-MCP Server Example

This example connects one agent to Sequential Thinking and Context7. It uses a
small allow-list so the agent demonstrates tool selection without exposing every
tool from every configured server.

## Run

1. Set the LLM provider credentials required by `main.go`.
2. Run `go run .` from this directory.
3. Optionally pass a config path and custom task as command-line arguments.

The default task retrieves Kubernetes documentation through Context7 and uses
Sequential Thinking to organize the result.

The key filtering configuration is:

```go
mcpagent.WithSelectedTools([]string{
    "sequential-thinking:sequentialthinking",
    "context7:resolve-library-id",
    "context7:query-docs",
})
```

`mcp_servers.json` contains the matching server definitions.
