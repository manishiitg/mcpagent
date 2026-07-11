package mcpagent

import "strings"

// actualMCPToolName converts the LLM-facing name used to disambiguate duplicate
// tools (server__tool) back to the name registered by that MCP server.
// Matching the mapped server prefix avoids corrupting legitimate tool names that
// contain "__" themselves.
func actualMCPToolName(exposedName, serverName string) string {
	if serverName == "" {
		return exposedName
	}

	prefix := serverName + "__"
	if strings.HasPrefix(exposedName, prefix) {
		return strings.TrimPrefix(exposedName, prefix)
	}
	return exposedName
}
