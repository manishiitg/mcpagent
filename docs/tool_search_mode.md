# Tool Search Mode

## Overview

Tool Search Mode is an agent-level feature that enables LLMs to work with large tool catalogs efficiently. Instead of loading all tools upfront, the agent exposes a `search_tools` function that allows the LLM to discover and load tools on-demand.

**Key Benefits:**
- **Context Efficiency**: Only loaded tools consume context tokens
- **Cross-LLM Support**: Works with any LLM provider (Anthropic, OpenAI, etc.)
- **Dynamic Discovery**: Tools are discovered and loaded during conversation
- **Pattern Matching**: Regex-based search for flexible tool discovery

---

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                        Tool Search Flow                         │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. Initial State                                               │
│     ┌─────────────┐                                             │
│     │ LLM sees:   │                                             │
│     │ - search_tools                                            │
│     │ - add_tool                                                │
│     └─────────────┘                                             │
│                                                                 │
│  2. User asks: "Get weather in Tokyo"                           │
│                                                                 │
│  3. LLM calls: search_tools(query: "weather")                   │
│     ┌─────────────────────────────────────────┐                 │
│     │ Agent searches all deferred tools       │                 │
│     │ Returns: get_weather, weather_forecast  │                 │
│     │ (Tools are NOT added to context yet)    │                 │
│     └─────────────────────────────────────────┘                 │
│                                                                 │
│  4. LLM calls: add_tool(tool_names: ["get_weather"])            │
│                                                                 │
│  5. After add_tool returns                                      │
│     ┌─────────────────────┐                                     │
│     │ LLM now sees:       │                                     │
│     │ - search_tools      │                                     │
│     │ - add_tool          │                                     │
│     │ - get_weather       │  ← newly added                      │
│     └─────────────────────┘                                     │
│                                                                 │
│  6. LLM calls: get_weather(location: "Tokyo")                   │
│                                                                 │
│  7. Agent executes tool, returns result                         │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## Usage

### Enabling Tool Search Mode

```go
agent, err := mcpagent.NewAgent(ctx, llm, configPath,
    mcpagent.WithToolSearchMode(true),
)
```

### With Pre-Discovered Tools

Pre-discovered tools are always available without needing to search:

```go
agent, err := mcpagent.NewAgent(ctx, llm, configPath,
    mcpagent.WithToolSearchMode(true),
    mcpagent.WithPreDiscoveredTools([]string{
        "get_weather",      // Always available
        "send_message",     // Always available
        "list_files",       // Always available
    }),
)
```

### Configuration Options

```go
// Enable tool search mode
mcpagent.WithToolSearchMode(true)

// Pre-discover specific tools (always available without searching)
mcpagent.WithPreDiscoveredTools([]string{"tool1", "tool2"})
```

---

## Key Files & Locations

| Component | File Path | Key Functions |
|-----------|-----------|---------------|
| **Agent Core** | `agent/agent.go` | `WithToolSearchMode()`, `initializeToolSearch()`, `getToolsForLLM()` |
| **Tool Definition** | `agent/virtual_tools_definitions.go` | `GetSearchToolsTool()` |
| **Tool Handler** | `agent/virtual_tool_handlers.go` | `handleSearchTools()`, `handleAddTool()` |
| **Conversation Loop** | `agent/conversation.go` | Tool refresh after `add_tool` |
| **Prompt Builder** | `agent/prompt/builder.go` | `GetToolSearchInstructions()` |

---

## The search_tools Function

### Definition

```json
{
  "name": "search_tools",
  "description": "Search for available tools by name or description. Returns matching tools that must be added using add_tool.",
  "parameters": {
    "type": "object",
    "properties": {
      "query": {
        "type": "string",
        "description": "Regex pattern to search tool names and descriptions"
      }
    },
    "required": ["query"]
  }
}
```

### Search Patterns

| Pattern | Matches |
|---------|---------|
| `weather` | Tools with "weather" in name or description |
| `database.*query` | Tools matching "database" followed by "query" |
| `(?i)slack` | Case-insensitive match for "slack" |
| `get_.*` | All tools starting with "get_" |
| `file\|folder` | Tools matching "file" OR "folder" |

### Response Format

```json
{
  "found": 2,
  "tools": [
    {
      "name": "get_weather",
      "description": "Get current weather for a location"
    },
    {
      "name": "weather_forecast",
      "description": "Get weather forecast for the next 7 days"
    }
  ],
  "message": "Found matching tools. Use 'add_tool' to load the ones you need."
}
```

### Fuzzy Search Fallback

When regex search finds no matches, the agent automatically performs fuzzy search:

1. **Substring matching**: Checks if query is contained in tool name/description
2. **Word-level matching**: Matches individual words from the query
3. **Character-level similarity**: Falls back to character overlap scoring

Returns up to 5 best matches sorted by relevance score.

**Example:**
- Query: `"wether"` (typo) → Still finds `get_weather` via fuzzy match
- Query: `"send msg"` → Matches `send_message` via word matching

---

## The add_tool Function

### Definition

```json
{
  "name": "add_tool",
  "description": "Add one or more tools to your available tools. Use this after finding tools with search_tools.",
  "parameters": {
    "type": "object",
    "properties": {
      "tool_names": {
        "type": "array",
        "items": { "type": "string" },
        "description": "Array of exact names of the tools to add (e.g., ['read_file', 'weather_get'])."
      }
    },
    "required": ["tool_names"]
  }
}
```

---

## Comparison with Other Modes

| Aspect | Normal Mode | Code Execution Mode | Tool Search Mode |
|--------|-------------|---------------------|------------------|
| **Tools visible** | All tools | `discover_code_files`, `write_code` | `search_tools`, `add_tool` + discovered |
| **Tool calling** | Direct JSON | Go code | Direct JSON |
| **Discovery method** | N/A | Via Go packages | Via regex search |
| **Context usage** | High (all tools) | Low (2 tools) | Dynamic (grows) |
| **Best for** | <30 tools | Complex logic, chaining | Large tool catalogs |

---

## System Prompt

When tool search mode is enabled, the system prompt includes:

```markdown
## Tool Search Mode

You have access to a large catalog of tools, but they are not loaded by default.
Use the search_tools function to discover tools, and then add_tool to load them.

**How to search & add:**
- Call search_tools with a regex pattern
- Review returned tools
- Call add_tool with the names of the tools you want to use
- Use the discovered tools to complete your task

**Available tool categories:**
- google-sheets (spreadsheet operations)
- github (repository management)
- slack (messaging)

**Workflow:**
1. Understand what the user needs
2. Search for relevant tools using search_tools
3. Add the tools you need using add_tool(tool_names=["..."])
4. Use the discovered tools to complete the task
```

---

## Implementation Details

### Agent State

```go
type Agent struct {
    // ... existing fields

    UseToolSearchMode bool                      // Enable tool search mode
    discoveredTools   map[string]llmtypes.Tool  // Tools discovered this session
    allDeferredTools  []llmtypes.Tool           // All available tools (hidden)
}
```

### Tool Discovery Flow

1. **Initialization**: All MCP and virtual tools stored in `allDeferredTools`
2. **Search**: `handleSearchTools()` matches query against deferred tools
3. **Discovery**: Matching tools added to `discoveredTools`
4. **Refresh**: `getToolsForLLM()` returns `search_tools` + all discovered tools
5. **Execution**: LLM can now call discovered tools

### Deferred Tools

When tool search mode is enabled:

```go
func (ag *Agent) initializeToolSearch() {
    // All MCP tools become deferred
    for _, tools := range ag.mcpServerTools {
        llmTools, _ := mcpclient.ToolsAsLLM(tools)
        ag.allDeferredTools = append(ag.allDeferredTools, llmTools...)
    }

    // Virtual tools become deferred (except search_tools)
    for _, tool := range ag.virtualTools {
        if tool.Function.Name != "search_tools" {
            ag.allDeferredTools = append(ag.allDeferredTools, tool)
        }
    }
}
```

---

## Best Practices

### When to Use Tool Search Mode

**Good use cases:**
- 30+ tools available
- Multiple MCP servers connected
- Tools from different domains (slack, github, database, etc.)
- User requests vary widely

**When normal mode is better:**
- Less than 30 tools
- All tools frequently used
- Simple, focused use cases

### Optimizing Tool Descriptions

Good tool descriptions improve search accuracy:

```go
// Good - searchable keywords
"Get current weather conditions for a city or location"

// Bad - vague
"Returns data about conditions"
```

### Guiding the LLM

Include tool categories in the system prompt so the LLM knows what to search for:

```go
categories := []string{
    "google-sheets (spreadsheet operations)",
    "github (repository and PR management)",
    "slack (messaging and channels)",
}
prompt := GetToolSearchInstructions(categories)
```

---

## Error Handling

### Invalid Regex Pattern

```json
{
  "error": "invalid regex pattern: missing closing )"
}
```

### No Matches Found

```json
{
  "found": 0,
  "tools": [],
  "message": "No tools found matching the pattern"
}
```

---

## Example

See the complete working example at [`examples/tool_search/`](../examples/tool_search/):

```bash
cd examples/tool_search
export $(grep -v '^#' ../../.env | xargs)
go run main.go
```

The example demonstrates:
- Enabling tool search mode with `WithToolSearchMode(true)`
- LLM discovering documentation tools via `search_tools`
- Using discovered tools to get React library information

---

## Related Documentation

- [`docs/code_execution_agent.md`](./code_execution_agent.md) - Code execution mode
- [`examples/tool_search/`](../examples/tool_search/) - Complete working example
- [`agent/agent.go`](../agent/agent.go) - Agent implementation
- [`agent/prompt/builder.go`](../agent/prompt/builder.go) - System prompt construction
