package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// ToolSearchResult represents a tool found during search
type ToolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Server      string `json:"server,omitempty"`
}

// handleSearchTools handles the search_tools virtual tool
// It searches through all deferred tools using regex pattern matching
func (a *Agent) handleSearchTools(ctx context.Context, args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "Error: query parameter is required", nil
	}

	// Try to compile as regex pattern (case-insensitive by default)
	pattern, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return fmt.Sprintf("Error: Invalid regex pattern: %v", err), nil
	}

	// Search all deferred tools with regex
	var matches []ToolSearchResult
	for i, tool := range a.allDeferredTools {
		if tool.Function == nil {
			continue
		}
		name := tool.Function.Name
		desc := tool.Function.Description

		if pattern.MatchString(name) || pattern.MatchString(desc) {
			result := ToolSearchResult{
				Name:        name,
				Description: desc,
			}
			// Include server name so the LLM can disambiguate duplicate tool names
			if i < len(a.allDeferredToolServers) {
				result.Server = a.allDeferredToolServers[i]
			}
			matches = append(matches, result)
			// Do NOT add to discovered tools - user must explicitly add them
		}
	}

	return a.formatSearchResults(matches)
}

// handleAddTool handles the add_tool virtual tool
// It adds specific tools from the deferred list to the active discovered tools
// When duplicate tool names exist across servers, tools are renamed to servername__toolname
func (a *Agent) handleAddTool(ctx context.Context, args map[string]interface{}) (string, error) {
	var toolNames []string

	// Check for tool_names array (new standard)
	if names, ok := args["tool_names"].([]interface{}); ok {
		for _, n := range names {
			if s, ok := n.(string); ok {
				toolNames = append(toolNames, s)
			}
		}
	} else if name, ok := args["tool_name"].(string); ok {
		// Fallback to single tool_name
		toolNames = []string{name}
	}

	// Optional server parameter to disambiguate duplicate tool names
	serverFilter, _ := args["server"].(string)

	if len(toolNames) == 0 {
		return "Error: tool_names parameter is required", nil
	}

	var added []string
	var alreadyAvailable []string
	var notFound []string
	var ambiguous []string

	for _, toolName := range toolNames {
		// Normalize tool name: convert PascalCase to snake_case for lookup
		normalizedName := pascalToSnakeCase(toolName)

		// Resolve any aliases (e.g., write_workspace_file -> update_workspace_file)
		aliasedName := resolveToolAlias(normalizedName)

		// Check original, normalized, and aliased names in discovered tools
		if _, exists := a.discoveredTools[toolName]; exists {
			alreadyAvailable = append(alreadyAvailable, toolName)
			continue
		}
		if _, exists := a.discoveredTools[normalizedName]; exists {
			alreadyAvailable = append(alreadyAvailable, toolName)
			continue
		}
		if _, exists := a.discoveredTools[aliasedName]; exists {
			alreadyAvailable = append(alreadyAvailable, toolName)
			continue
		}

		// Find all matching tools (there may be duplicates across servers)
		type toolMatch struct {
			tool       llmtypes.Tool
			serverName string
		}
		var matches []toolMatch
		for i, tool := range a.allDeferredTools {
			if tool.Function != nil {
				if tool.Function.Name == toolName || tool.Function.Name == normalizedName || tool.Function.Name == aliasedName {
					srv := ""
					if i < len(a.allDeferredToolServers) {
						srv = a.allDeferredToolServers[i]
					}
					// If server filter specified, only include matching server
					if serverFilter != "" && srv != "" && srv != serverFilter {
						continue
					}
					matches = append(matches, toolMatch{tool: tool, serverName: srv})
				}
			}
		}

		if len(matches) == 0 {
			notFound = append(notFound, toolName)
			continue
		}

		if len(matches) == 1 {
			// Single match - add with original name
			actualToolName := matches[0].tool.Function.Name
			a.discoveredTools[actualToolName] = matches[0].tool
			added = append(added, actualToolName)
		} else if serverFilter != "" && len(matches) == 1 {
			// Server filter narrowed it down to one
			actualToolName := matches[0].tool.Function.Name
			a.discoveredTools[actualToolName] = matches[0].tool
			added = append(added, actualToolName)
		} else {
			// Multiple matches - rename to servername__toolname for disambiguation
			// Also update toolToServer so tool calls route correctly
			serverNames := make([]string, 0, len(matches))
			for _, m := range matches {
				qualifiedName := m.tool.Function.Name
				if m.serverName != "" {
					qualifiedName = m.serverName + "__" + m.tool.Function.Name
				}
				// Create a copy with the qualified name
				qualifiedTool := m.tool
				qualifiedTool.Function = &llmtypes.FunctionDefinition{
					Name:        qualifiedName,
					Description: fmt.Sprintf("[%s] %s", m.serverName, m.tool.Function.Description),
					Parameters:  m.tool.Function.Parameters,
				}
				a.discoveredTools[qualifiedName] = qualifiedTool
				// Update toolToServer so tool execution routes to the correct server
				if a.toolToServer != nil && m.serverName != "" {
					a.toolToServer[qualifiedName] = m.serverName
				}
				added = append(added, qualifiedName)
				serverNames = append(serverNames, m.serverName)
			}
			ambiguous = append(ambiguous, fmt.Sprintf("%s (found on servers: %s, added as separate tools)", toolName, strings.Join(serverNames, ", ")))
		}
	}

	// Build response message
	var msgs []string
	if len(added) > 0 {
		msgs = append(msgs, fmt.Sprintf("Added tools: %s", strings.Join(added, ", ")))
	}
	if len(alreadyAvailable) > 0 {
		msgs = append(msgs, fmt.Sprintf("Already available: %s", strings.Join(alreadyAvailable, ", ")))
	}
	if len(notFound) > 0 {
		msgs = append(msgs, fmt.Sprintf("Not found: %s", strings.Join(notFound, ", ")))
	}
	if len(ambiguous) > 0 {
		msgs = append(msgs, fmt.Sprintf("Disambiguated: %s", strings.Join(ambiguous, "; ")))
	}

	return strings.Join(msgs, "\n"), nil
}

// handleRemoveTool handles the remove_tool virtual tool
// It removes tools from the active discovered tools set
func (a *Agent) handleRemoveTool(ctx context.Context, args map[string]interface{}) (string, error) {
	var toolNames []string

	// Check for tool_names array
	if names, ok := args["tool_names"].([]interface{}); ok {
		for _, n := range names {
			if s, ok := n.(string); ok {
				toolNames = append(toolNames, s)
			}
		}
	} else if name, ok := args["tool_name"].(string); ok {
		toolNames = []string{name}
	}

	if len(toolNames) == 0 {
		return "Error: tool_names parameter is required", nil
	}

	var removed []string
	var notActive []string

	for _, toolName := range toolNames {
		// Don't allow removing virtual tools (search_tools, add_tool, etc.)
		if isVirtualTool(toolName) || toolName == "remove_tool" {
			notActive = append(notActive, fmt.Sprintf("%s (cannot remove virtual tool)", toolName))
			continue
		}

		// Try exact name first
		if _, exists := a.discoveredTools[toolName]; exists {
			delete(a.discoveredTools, toolName)
			// Clean up qualified name from toolToServer if it was a disambiguated tool
			if strings.Contains(toolName, "__") && a.toolToServer != nil {
				delete(a.toolToServer, toolName)
			}
			removed = append(removed, toolName)
			continue
		}

		// Try normalized name
		normalizedName := pascalToSnakeCase(toolName)
		if _, exists := a.discoveredTools[normalizedName]; exists {
			delete(a.discoveredTools, normalizedName)
			removed = append(removed, normalizedName)
			continue
		}

		notActive = append(notActive, toolName)
	}

	var msgs []string
	if len(removed) > 0 {
		msgs = append(msgs, fmt.Sprintf("Removed tools: %s", strings.Join(removed, ", ")))
	}
	if len(notActive) > 0 {
		msgs = append(msgs, fmt.Sprintf("Not in active tools: %s", strings.Join(notActive, ", ")))
	}

	return strings.Join(msgs, "\n"), nil
}

// handleShowAllTools returns all available tool names
func (a *Agent) handleShowAllTools(ctx context.Context, args map[string]interface{}) (string, error) {
	var toolNames []string

	// Add discovered tool names
	for name := range a.discoveredTools {
		toolNames = append(toolNames, name)
	}

	// Add deferred tool names (not yet discovered)
	discoveredSet := make(map[string]bool)
	for name := range a.discoveredTools {
		discoveredSet[name] = true
	}

	for _, tool := range a.allDeferredTools {
		if tool.Function != nil && !discoveredSet[tool.Function.Name] {
			toolNames = append(toolNames, tool.Function.Name)
		}
	}

	// Sort for consistent output
	sort.Strings(toolNames)

	result := map[string]interface{}{
		"total": len(toolNames),
		"tools": toolNames,
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

// formatSearchResults formats the search results as JSON
func (a *Agent) formatSearchResults(matches []ToolSearchResult) (string, error) {
	if len(matches) == 0 {
		return "No tools found matching the pattern. Try a different search query.", nil
	}

	// Check if any results have duplicate tool names
	nameCount := make(map[string]int)
	for _, m := range matches {
		nameCount[m.Name]++
	}
	hasDuplicates := false
	for _, count := range nameCount {
		if count > 1 {
			hasDuplicates = true
			break
		}
	}

	message := "Found matching tools. Use 'add_tool' to load the ones you need."
	if hasDuplicates {
		message += " Some tools exist on multiple servers - use the 'server' parameter in add_tool to pick a specific one, or add without server to get both (renamed as servername__toolname)."
	}

	result, err := json.MarshalIndent(map[string]interface{}{
		"found":   len(matches),
		"tools":   matches,
		"message": message,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal search results: %w", err)
	}
	return string(result), nil
}

// initializeToolSearch sets up tool search mode by moving all tools to deferred
// and pre-discovering configured tools
func (a *Agent) initializeToolSearch() {
	if !a.UseToolSearchMode {
		return
	}

	// Ensure maps are initialized
	if a.discoveredTools == nil {
		a.discoveredTools = make(map[string]llmtypes.Tool)
	}
	if a.allDeferredTools == nil {
		a.allDeferredTools = []llmtypes.Tool{}
	}

	a.Logger.Info("Initializing tool search mode")

	// Pre-discover configured tools
	preDiscoveredSet := make(map[string]bool)
	for _, name := range a.preDiscoveredTools {
		preDiscoveredSet[name] = true
	}

	for _, tool := range a.allDeferredTools {
		if tool.Function == nil {
			continue
		}
		if preDiscoveredSet[tool.Function.Name] {
			a.discoveredTools[tool.Function.Name] = tool
			a.Logger.Debug("Pre-discovered tool",
				loggerv2.String("name", tool.Function.Name))
		}
	}

	a.Logger.Info("Tool search mode initialized",
		loggerv2.Int("total_deferred", len(a.allDeferredTools)),
		loggerv2.Int("pre_discovered", len(a.discoveredTools)))
}

// getToolsForToolSearchMode returns the tools available to the LLM in tool search mode
// This includes search_tools, pre-discovered tools, and dynamically discovered tools
func (a *Agent) getToolsForToolSearchMode() []llmtypes.Tool {
	// Start with search_tools
	tools := CreateToolSearchTools()

	// Add discovered tools (includes pre-discovered)
	for _, tool := range a.discoveredTools {
		tools = append(tools, tool)
	}

	return tools
}

// GetDiscoveredToolCount returns the number of tools discovered in this session
func (a *Agent) GetDiscoveredToolCount() int {
	if a.discoveredTools == nil {
		return 0
	}
	return len(a.discoveredTools)
}

// GetDeferredToolCount returns the total number of deferred tools
func (a *Agent) GetDeferredToolCount() int {
	return len(a.allDeferredTools)
}

// pascalToSnakeCase converts PascalCase to snake_case
// Example: "ReadWorkspaceFile" -> "read_workspace_file"
func pascalToSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// toolAliases maps common alternative tool names to actual tool names
// This handles cases where LLMs use common conventions that differ from actual tool names
var toolAliases = map[string]string{
	// write is commonly used as an alias for update/create
	"write_workspace_file":  "update_workspace_file",
	"create_workspace_file": "update_workspace_file",
}

// resolveToolAlias returns the actual tool name if an alias exists, otherwise returns the input
func resolveToolAlias(toolName string) string {
	if actual, exists := toolAliases[toolName]; exists {
		return actual
	}
	return toolName
}
