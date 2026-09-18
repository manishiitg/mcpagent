package mcpagent

import (
	"context"
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// VirtualTool represents a virtual tool that can be called by the LLM
type VirtualTool struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
	Handler     func(ctx context.Context, args map[string]interface{}) (string, error)
}

// CreateVirtualTools creates virtual tools for prompt access
func (a *Agent) createVirtualTools() []llmtypes.Tool {
	var virtualTools []llmtypes.Tool

	// Add context offloading virtual tools if enabled
	// In code execution mode, context offloading tools are not needed
	// (the LLM writes code that calls HTTP endpoints directly)
	if !a.useCodeExecutionMode {
		largeOutputTools := a.createLargeOutputVirtualTools()
		virtualTools = append(virtualTools, largeOutputTools...)
	}

	// Add get_api_spec tool — returns OpenAPI spec for specific tool(s)
	getAPISpecTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "get_api_spec",
			Description: "Get the OpenAPI specification for specific tool(s). Pass the tool names you want to call — that is the address. The system prompt lists every available tool name.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					// Optional, and deliberately so. Requiring it forced a model to
					// supply a value it frequently cannot know: built-in tools are
					// addressed as $MCP_CUSTOM/{tool_name} with no category segment,
					// so an agent asked for a "server" had nothing correct to say and
					// guessed — 46 failed get_api_spec calls in a single day, walking
					// the category list one rejection at a time. Tool names are the
					// address everywhere else in the contract; asking for a second one
					// only creates a way to be wrong.
					"server_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Only useful to disambiguate a real MCP server (e.g. 'google_sheets'). Omit it for built-in tools — the tool name alone resolves.",
					},
					"tool_name": map[string]interface{}{
						"description": "Tool name(s) to get the API spec for. Pass a single string (e.g., 'search_issues') or an array of strings (e.g., ['create_spreadsheet', 'update_values']).",
					},
				},
				"required": []string{"tool_name"},
			}),
		},
	}
	virtualTools = append(virtualTools, getAPISpecTool)

	return virtualTools
}

// HandleVirtualTool handles virtual tool execution
func (a *Agent) handleVirtualTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	switch toolName {
	case "get_api_spec":
		return a.handleGetAPISpec(ctx, args)
	default:
		// Check if it's a context offloading virtual tool
		if a.enableContextOffloading {
			return a.handleLargeOutputVirtualTool(ctx, toolName, args)
		}
		return "", fmt.Errorf("unknown virtual tool: %s", toolName)
	}
}
