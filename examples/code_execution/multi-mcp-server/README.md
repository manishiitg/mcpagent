# Code Execution with Multiple MCP Servers

This example enables code execution mode while restricting which MCP servers and
tools generated scripts may call.

It demonstrates:

- selecting individual Gmail tools with `WithSelectedTools`;
- allowing all tools from Sequential Thinking and AWS Knowledge with
  `WithSelectedServers`;
- exposing selected tools through authenticated per-tool HTTP endpoints; and
- using `MCP_API_URL` and `MCP_API_TOKEN` from generated code.

Run `go run .` after configuring the provider and MCP credentials expected by
`main.go`. Review the example source for the filtering and HTTP-server setup.
