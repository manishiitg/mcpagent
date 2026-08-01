package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache/openapi"
)

// handleGetAPISpec handles the get_api_spec virtual tool.
// Returns the full OpenAPI spec for the requested tool(s) on a server.
// tool_name is required — accepts a single string or an array of strings.
// The system prompt already lists all servers and tool names, so no "list only" mode is needed.
func (a *Agent) handleGetAPISpec(ctx context.Context, args map[string]interface{}) (string, error) {
	_ = ctx
	// Optional: an omitted server_name means "resolve by tool name", which is the
	// contract everywhere else. It remains accepted as compatibility input, but
	// routing and authorization never depend on an agent-supplied category/server.
	serverName, _ := args["server_name"].(string)

	// Parse tool_name: accepts string or []string (JSON array)
	var toolNames []string
	if raw, exists := args["tool_name"]; exists && raw != nil {
		switch v := raw.(type) {
		case string:
			if v != "" {
				toolNames = append(toolNames, v)
			}
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					toolNames = append(toolNames, s)
				}
			}
		}
	}
	if len(toolNames) == 0 {
		return "", fmt.Errorf("tool_name parameter is required (string or array of strings)")
	}

	// Tool names are the canonical address. Normalize and sort once so validation,
	// generation, and cache identity all consume the same request.
	sortedNames := make([]string, len(toolNames))
	copy(sortedNames, toolNames)
	sort.Strings(sortedNames)
	cacheKey := "tools:" + strings.Join(sortedNames, ",")
	if serverName != "" && a.Logger != nil {
		a.Logger.Debug("get_api_spec: server_name is compatibility-only; resolving by tool name",
			loggerv2.String("server_name", serverName),
			loggerv2.Any("tool_names", sortedNames))
	}

	// Resolve and authorize every name before consulting the schema cache. A
	// cache hit is metadata reuse, never an authorization decision.
	customToolsForSpec := make(map[string]openapi.CustomToolForOpenAPI)
	mcpToolsByServer := make(map[string][]llmtypes.Tool)
	var unknown, notAllowed []string
	toolSource := a.Tools
	if a.UseCodeExecutionMode && len(a.allMCPToolDefs) > 0 {
		toolSource = a.allMCPToolDefs
	}
	for _, name := range sortedNames {
		if !a.isToolAllowed(name) {
			notAllowed = append(notAllowed, name)
			continue
		}
		if ct, ok := a.customTools[name]; ok {
			customToolsForSpec[name] = openapi.CustomToolForOpenAPI{Definition: ct.Definition, Category: ct.Category}
			continue
		}

		srvName, ok := a.toolToServer[name]
		if !ok || srvName == "custom" {
			unknown = append(unknown, name)
			continue
		}
		if !a.serverIsAvailable(srvName) {
			notAllowed = append(notAllowed, name)
			continue
		}

		var definition llmtypes.Tool
		found := false
		for _, candidate := range toolSource {
			if candidate.Function != nil && candidate.Function.Name == name {
				definition = candidate
				found = true
				break
			}
		}
		if !found {
			unknown = append(unknown, name)
			continue
		}
		normalizedServer := strings.ReplaceAll(srvName, "-", "_")
		mcpToolsByServer[normalizedServer] = append(mcpToolsByServer[normalizedServer], definition)
	}

	if len(unknown) > 0 || len(notAllowed) > 0 {
		return "", fmt.Errorf("tools_unavailable: unknown=%v not_allowed=%v", unknown, notAllowed)
	}

	a.openAPISpecCacheMu.RLock()
	if cached, exists := a.openAPISpecCache[cacheKey]; exists {
		a.openAPISpecCacheMu.RUnlock()
		return string(cached), nil
	}
	a.openAPISpecCacheMu.RUnlock()

	baseURL := a.getCodeExecutionAPIBaseURL()
	var sections []string
	if len(customToolsForSpec) > 0 {
		sections = append(sections, openapi.GenerateCustomToolsCompactSpec("custom", customToolsForSpec, baseURL))
	}
	serverNames := make([]string, 0, len(mcpToolsByServer))
	for name := range mcpToolsByServer {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)
	for _, name := range serverNames {
		sections = append(sections, openapi.GenerateCompactSpec(name, mcpToolsByServer[name], baseURL))
	}

	spec := strings.Join(sections, "\n")
	a.cacheSpec(cacheKey, []byte(spec))
	return spec, nil
}

// serverIsAvailable checks if a server passes the tool filter.
func (a *Agent) serverIsAvailable(serverName string) bool {
	if a.toolFilter.ShouldIncludeServer(serverName) {
		return true
	}
	serverNameWithHyphen := strings.ReplaceAll(serverName, "_", "-")
	return a.toolFilter.ShouldIncludeServer(serverNameWithHyphen)
}

// cacheSpec stores a generated spec in the cache.
func (a *Agent) cacheSpec(key string, specBytes []byte) {
	a.openAPISpecCacheMu.Lock()
	if a.openAPISpecCache == nil {
		a.openAPISpecCache = make(map[string][]byte)
	}
	a.openAPISpecCache[key] = specBytes
	a.openAPISpecCacheMu.Unlock()
}

// buildPreDiscoveredToolSpecs generates compact API specs for pre-discovered tools.
// When pre-discovered tools are configured, their full specs (endpoint + parameter schema)
// are included inline in the system prompt so the agent doesn't need to call get_api_spec.
// Returns empty string if no pre-discovered tools are configured or found.
func (a *Agent) buildPreDiscoveredToolSpecs() string {
	if len(a.preDiscoveredTools) == 0 {
		return ""
	}

	// Build a set of pre-discovered tool names for fast lookup
	preDiscoveredSet := make(map[string]bool, len(a.preDiscoveredTools))
	for _, name := range a.preDiscoveredTools {
		preDiscoveredSet[name] = true
	}

	baseURL := a.getCodeExecutionAPIBaseURL()

	// Collect MCP tool definitions for pre-discovered tools
	var mcpToolsByServer = make(map[string][]llmtypes.Tool)
	toolSource := a.allMCPToolDefs
	if len(toolSource) == 0 {
		toolSource = a.Tools
	}

	for _, tool := range toolSource {
		if tool.Function == nil {
			continue
		}
		if !preDiscoveredSet[tool.Function.Name] {
			continue
		}
		if !a.isToolAllowed(tool.Function.Name) {
			continue
		}
		// Find the server for this tool
		serverName, ok := a.toolToServer[tool.Function.Name]
		if !ok || serverName == "custom" {
			continue
		}
		normalized := strings.ReplaceAll(serverName, "-", "_")
		mcpToolsByServer[normalized] = append(mcpToolsByServer[normalized], tool)
	}

	// Collect custom tool definitions for pre-discovered tools
	customToolsByCategory := make(map[string]map[string]openapi.CustomToolForOpenAPI)
	for toolName, ct := range a.customTools {
		if !preDiscoveredSet[toolName] {
			continue
		}
		if !a.isToolAllowed(toolName) {
			continue
		}
		category := ct.Category
		if category == "" {
			continue
		}
		if customToolsByCategory[category] == nil {
			customToolsByCategory[category] = make(map[string]openapi.CustomToolForOpenAPI)
		}
		customToolsByCategory[category][toolName] = openapi.CustomToolForOpenAPI{
			Definition: ct.Definition,
			Category:   ct.Category,
		}
	}

	if len(mcpToolsByServer) == 0 && len(customToolsByCategory) == 0 {
		return ""
	}

	// Generate compact specs for each server's pre-discovered tools
	var sb strings.Builder
	sb.WriteString("\n\n<pre_discovered_tool_specs>\n")
	sb.WriteString("**PRE-LOADED TOOL SPECS** (no need to call get_api_spec for these):\n\n")

	// Sort servers for deterministic output
	serverNames := make([]string, 0, len(mcpToolsByServer))
	for name := range mcpToolsByServer {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	for _, serverName := range serverNames {
		tools := mcpToolsByServer[serverName]
		spec := openapi.GenerateCompactSpec(serverName, tools, baseURL)
		sb.WriteString(spec)
		sb.WriteString("\n")
	}

	// Sort categories for deterministic output
	categoryNames := make([]string, 0, len(customToolsByCategory))
	for name := range customToolsByCategory {
		categoryNames = append(categoryNames, name)
	}
	sort.Strings(categoryNames)

	for _, category := range categoryNames {
		tools := customToolsByCategory[category]
		spec := openapi.GenerateCustomToolsCompactSpec(category, tools, baseURL)
		sb.WriteString(spec)
		sb.WriteString("\n")
	}

	sb.WriteString("</pre_discovered_tool_specs>\n")

	if a.Logger != nil {
		totalPreDiscovered := 0
		for _, tools := range mcpToolsByServer {
			totalPreDiscovered += len(tools)
		}
		for _, tools := range customToolsByCategory {
			totalPreDiscovered += len(tools)
		}
		a.Logger.Info("Built pre-discovered tool specs for system prompt",
			loggerv2.Int("pre_discovered_tools", totalPreDiscovered),
			loggerv2.Int("configured", len(a.preDiscoveredTools)))
	}

	return sb.String()
}

func (a *Agent) getCodeExecutionAPIBaseURL() string {
	baseURL := a.APIBaseURL
	if baseURL == "" {
		baseURL = os.Getenv("MCP_API_URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}

	if a.SessionID == "" {
		return strings.TrimRight(baseURL, "/")
	}

	sessionPrefix := "/s/" + a.SessionID
	if strings.Contains(baseURL, sessionPrefix) {
		return strings.TrimRight(baseURL, "/")
	}

	return strings.TrimRight(baseURL, "/") + sessionPrefix
}

// buildToolIndex returns a JSON index of available servers and their tool names.
// This is included in the system prompt so the LLM knows what's available.
// It builds the index purely from agent internal state (no filesystem scanning).
func (a *Agent) buildToolIndex() (string, error) {
	type ServerInfo struct {
		Tools []string `json:"tools"`
	}

	index := make(map[string]ServerInfo)

	// Build MCP server tool index from toolToServer mapping
	serverToolsMap := make(map[string]map[string]bool)
	for toolName, serverName := range a.toolToServer {
		if serverName == "custom" {
			continue // Custom tools are handled separately
		}
		if !a.isToolAllowed(toolName) {
			continue
		}

		// Apply server-level filtering
		shouldInclude := a.toolFilter.ShouldIncludeServer(serverName)
		if !shouldInclude {
			normalized := strings.ReplaceAll(serverName, "-", "_")
			shouldInclude = a.toolFilter.ShouldIncludeServer(normalized)
		}
		if !shouldInclude {
			continue
		}

		normalized := strings.ReplaceAll(serverName, "-", "_")
		if serverToolsMap[normalized] == nil {
			serverToolsMap[normalized] = make(map[string]bool)
		}
		serverToolsMap[normalized][toolName] = true
	}

	for serverName, toolsSet := range serverToolsMap {
		tools := make([]string, 0, len(toolsSet))
		for toolName := range toolsSet {
			tools = append(tools, toolName)
		}
		sort.Strings(tools)
		index[serverName] = ServerInfo{Tools: tools}
	}

	// Add custom tools grouped by category to the tool index.
	// Even in code execution mode, custom tools must appear here so that Claude Code
	// (which uses the MCP bridge and can only discover tools via get_api_spec) can
	// find and call them via HTTP API. For non-Claude-Code providers, the tools are
	// also available as direct LLM calls — having them in the index is harmless.
	// Respect toolAllowList: if set, only include allowed custom tools in the index.
	customToolsByCategory := make(map[string][]string)
	var blockedCustomTools []string
	for toolName, ct := range a.customTools {
		category := ct.Category
		if category == "" {
			continue
		}
		if !a.isToolAllowed(toolName) {
			blockedCustomTools = append(blockedCustomTools, toolName)
			continue
		}
		customToolsByCategory[category] = append(customToolsByCategory[category], toolName)
	}
	if a.Logger != nil && len(blockedCustomTools) > 0 {
		sort.Strings(blockedCustomTools)
		a.Logger.Info("🔒 [TOOL_ALLOW_LIST] buildToolIndex blocked custom tools",
			loggerv2.Int("blocked_count", len(blockedCustomTools)),
			loggerv2.Any("blocked", blockedCustomTools))
	}
	for category, tools := range customToolsByCategory {
		sort.Strings(tools)
		index[category] = ServerInfo{Tools: tools}
	}

	jsonData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool index: %w", err)
	}

	if a.Logger != nil {
		totalTools := 0
		for _, pkg := range index {
			totalTools += len(pkg.Tools)
		}
		a.Logger.Info("Built tool index",
			loggerv2.Int("servers", len(index)),
			loggerv2.Int("total_tools", totalTools))
	}

	return string(jsonData), nil
}

// getAgentGeneratedDir returns the agent-specific generated directory
// Format: generated/agents/<trace_id>/
// Only creates the directory if code execution mode is enabled
func (a *Agent) getAgentGeneratedDir() string {
	baseDir := a.getGeneratedDir()
	agentDir := filepath.Join(baseDir, "agents", string(a.TraceID))

	if a.UseCodeExecutionMode {
		if err := os.MkdirAll(agentDir, 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for user-accessible directories
			if a.Logger != nil {
				a.Logger.Warn("Failed to create agent generated directory", loggerv2.String("agent_dir", agentDir), loggerv2.Error(err))
			}
		}
	}

	return agentDir
}

// BuildSafeEnvironment creates a minimal, safe environment for shell commands.
// Only includes essential variables, excludes all secrets.
// Exported so it can be used by workspace security and other packages.
func BuildSafeEnvironment() []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"USER=agent",
		"SHELL=/bin/sh",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
}
