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
	for _, tool := range a.allDeferredTools {
		if tool.Function == nil {
			continue
		}
		name := tool.Function.Name
		desc := tool.Function.Description

		if pattern.MatchString(name) || pattern.MatchString(desc) {
			matches = append(matches, ToolSearchResult{
				Name:        name,
				Description: desc,
			})
			// Do NOT add to discovered tools - user must explicitly add them
		}
	}

	return a.formatSearchResults(matches)
}

// handleAddTool handles the add_tool virtual tool
// It adds specific tools from the deferred list to the active discovered tools
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

	if len(toolNames) == 0 {
		return "Error: tool_names parameter is required", nil
	}

	var added []string
	var alreadyAvailable []string
	var notFound []string

	for _, toolName := range toolNames {
		if _, exists := a.discoveredTools[toolName]; exists {
			alreadyAvailable = append(alreadyAvailable, toolName)
			continue
		}

		var foundTool *llmtypes.Tool
		for _, tool := range a.allDeferredTools {
			if tool.Function != nil && tool.Function.Name == toolName {
				foundTool = &tool
				break
			}
		}

		if foundTool == nil {
			notFound = append(notFound, toolName)
			continue
		}

		a.discoveredTools[toolName] = *foundTool
		added = append(added, toolName)
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

	message := "Found matching tools. Use 'add_tool' to load the ones you need."

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
