package mcpclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PrintTools displays tools in a detailed, human-readable format (Debug level only)
func PrintTools(tools []mcp.Tool, logger loggerv2.Logger) {
	logger.Debug("Available tools", loggerv2.Int("count", len(tools)))
	for i, tool := range tools {
		logger.Debug("Tool",
			loggerv2.Int("index", i+1),
			loggerv2.String("name", tool.Name),
			loggerv2.String("description", tool.Description))
	}
}

// PrintToolResult displays a tool result in a human-readable format (Debug level only)
func PrintToolResult(result *mcp.CallToolResult, logger loggerv2.Logger) {
	if result == nil {
		logger.Debug("Tool execution completed but no result returned")
		return
	}

	logger.Debug("Tool Result", loggerv2.Any("is_error", result.IsError))

	// Join all content parts
	var parts []string
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, c.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[Image: %s]", c.Data))
		case *mcp.EmbeddedResource:
			parts = append(parts, fmt.Sprintf("[Resource: %s]", formatResourceContents(c.Resource)))
		default:
			// For any other content type, try to marshal to JSON
			if jsonBytes, err := json.Marshal(content); err == nil {
				parts = append(parts, string(jsonBytes))
			} else {
				parts = append(parts, fmt.Sprintf("[Unknown content type: %T]", content))
			}
		}
	}

	joined := strings.Join(parts, "\n")
	logger.Debug("Tool result content", loggerv2.String("content", joined))
}
