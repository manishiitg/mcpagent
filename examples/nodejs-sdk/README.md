# MCPAgent Node.js SDK Examples

This directory contains working examples for the official Node.js/TypeScript SDK in this repo. The SDK talks to the Go runtime over gRPC on a Unix socket and can auto-start the Go server for you.

## Quick Start

From this directory:

```bash
npm install
npm run dev
```

The examples expect API keys to be available in your environment or in a local `.env` file that the SDK can read.
When using the streaming example, handle both incremental `chunk` events and the final response payload because some providers may only populate `event.response` on the `final` event.

## How It Works

- The SDK package is `@mcpagent/node`
- The SDK auto-starts the Go server from the local `mcpagent` checkout
- Communication uses gRPC over a Unix domain socket, not an HTTP REST API
- MCP server configuration is loaded from `mcp_servers.json` in this example directory

## Example Files

- `src/basic.ts` - Initialize the SDK, stream responses, and inspect token usage
- `src/custom-tools.ts` - Register JavaScript tool handlers that the agent can call
- `src/multi-turn.ts` - Continue a conversation with explicit message history
- `src/multi-mcp-server.ts` - Work with multiple configured MCP servers

## Minimal SDK Example

```typescript
import { MCPAgent } from '@mcpagent/node';
import path from 'path';

const agent = new MCPAgent({
  serverOptions: {
    mcpConfigPath: path.join(__dirname, '..', 'mcp_servers.json'),
    logLevel: 'info',
  },
});

await agent.initialize({
  provider: 'openai',
  modelId: 'gpt-4o',
});

const response = await agent.ask('What tools are available?');
console.log(response.response);

await agent.destroy();
```

## Configuration

Important inputs:

- `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`, or other provider credentials depending on the example you run
- `GEMINI_API_KEY` if you want to use the `gemini-cli` provider through the SDK
- `mcp_servers.json` for MCP server definitions
- Optional `serverOptions.goProjectPath` if you want the SDK to start the Go runtime from a non-default checkout

## Notes

- The first run can be slower because the Go server and MCP servers may need to start
- The SDK examples are local-development oriented and assume the `mcpagent` repo is present
- For package-level SDK docs, see `/Users/mipl/ai-work/mcpagent/sdk-node/README.md`
