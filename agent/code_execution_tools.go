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
// Returns an OpenAPI 3.0 YAML spec for a specific server's tools, or a single tool.
// When tool_name is provided, returns only that tool's spec (smaller, faster).
// Specs are generated on-demand and cached on the agent.
func (a *Agent) handleGetAPISpec(ctx context.Context, args map[string]interface{}) (string, error) {
	serverName, ok := args["server_name"].(string)
	if !ok || serverName == "" {
		return "", fmt.Errorf("server_name parameter is required")
	}

	// Optional: specific tool name for single-tool spec
	toolNameFilter, _ := args["tool_name"].(string)

	// Normalize: hyphens to underscores
	serverName = strings.ReplaceAll(serverName, "-", "_")

	// Cache key: "server" for full spec, "server:tool" for single-tool spec
	cacheKey := serverName
	if toolNameFilter != "" {
		cacheKey = serverName + ":" + toolNameFilter
	}

	// Check cache
	a.openAPISpecCacheMu.RLock()
	if a.openAPISpecCache != nil {
		if cached, exists := a.openAPISpecCache[cacheKey]; exists {
			a.openAPISpecCacheMu.RUnlock()
			return string(cached), nil
		}
	}
	a.openAPISpecCacheMu.RUnlock()

	// Determine the API base URL
	baseURL := a.APIBaseURL
	if baseURL == "" {
		baseURL = os.Getenv("MCP_API_URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}

	// Check if this is a custom tool category
	isCustomCategory := a.toolFilter.IsCategoryDirectory(serverName) ||
		a.toolFilter.IsCategoryDirectory(serverName+"_tools")

	if isCustomCategory {
		// Build custom tools map for this category
		customToolsForSpec := make(map[string]openapi.CustomToolForOpenAPI)
		for toolName, ct := range a.customTools {
			if ct.Category == serverName || openapi.GetPackageName(ct.Category) == serverName+"_tools" {
				// If tool_name filter is set, only include that tool
				if toolNameFilter != "" && toolName != toolNameFilter {
					continue
				}
				customToolsForSpec[toolName] = openapi.CustomToolForOpenAPI{
					Definition: ct.Definition,
					Category:   ct.Category,
				}
			}
		}

		if len(customToolsForSpec) == 0 {
			if toolNameFilter != "" {
				return "", fmt.Errorf("tool %q not found in category %s", toolNameFilter, serverName)
			}
			return "", fmt.Errorf("no custom tools found for category: %s", serverName)
		}

		specBytes, err := openapi.GenerateCustomToolsOpenAPISpec(serverName, customToolsForSpec, baseURL)
		if err != nil {
			return "", fmt.Errorf("failed to generate OpenAPI spec for %s: %w", serverName, err)
		}

		// Cache
		a.openAPISpecCacheMu.Lock()
		if a.openAPISpecCache == nil {
			a.openAPISpecCache = make(map[string][]byte)
		}
		a.openAPISpecCache[cacheKey] = specBytes
		a.openAPISpecCacheMu.Unlock()
		return string(specBytes), nil
	}

	// MCP server — collect tools for this server
	var serverTools []llmtypes.Tool

	// Check if server should be included
	shouldInclude := a.toolFilter.ShouldIncludeServer(serverName)
	if !shouldInclude {
		// Try hyphen format
		serverNameWithHyphen := strings.ReplaceAll(serverName, "_", "-")
		shouldInclude = a.toolFilter.ShouldIncludeServer(serverNameWithHyphen)
	}
	if !shouldInclude {
		return "", fmt.Errorf("server %s is not available or filtered out", serverName)
	}

	// Collect tools that belong to this server
	// In code execution mode, MCP tools are excluded from a.Tools but stored in allMCPToolDefs
	toolSource := a.Tools
	if a.UseCodeExecutionMode && len(a.allMCPToolDefs) > 0 {
		toolSource = a.allMCPToolDefs
	}

	for toolName, srvName := range a.toolToServer {
		normalizedSrv := strings.ReplaceAll(srvName, "-", "_")
		if normalizedSrv == serverName && srvName != "custom" {
			// If tool_name filter is set, only include that tool
			if toolNameFilter != "" && toolName != toolNameFilter {
				continue
			}
			// Find the tool definition
			for _, t := range toolSource {
				if t.Function != nil && t.Function.Name == toolName {
					serverTools = append(serverTools, t)
					break
				}
			}
		}
	}

	if len(serverTools) == 0 {
		if toolNameFilter != "" {
			return "", fmt.Errorf("tool %q not found on server %s", toolNameFilter, serverName)
		}
		return "", fmt.Errorf("no tools found for server: %s", serverName)
	}

	specBytes, err := openapi.GenerateServerOpenAPISpec(serverName, serverTools, baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to generate OpenAPI spec for %s: %w", serverName, err)
	}

	// Cache
	a.openAPISpecCacheMu.Lock()
	if a.openAPISpecCache == nil {
		a.openAPISpecCache = make(map[string][]byte)
	}
	a.openAPISpecCache[cacheKey] = specBytes
	a.openAPISpecCacheMu.Unlock()

	if a.Logger != nil {
		a.Logger.Info("Generated OpenAPI spec",
			loggerv2.String("server", serverName),
			loggerv2.String("tool_filter", toolNameFilter),
			loggerv2.Int("tools_count", len(serverTools)),
			loggerv2.Int("spec_bytes", len(specBytes)))
	}

	return string(specBytes), nil
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

	// Add custom tools grouped by category
	// In code execution mode, custom tools are already available as direct tool calls,
	// so they should NOT appear in the tool index (which is for API-only tools).
	if !a.UseCodeExecutionMode {
		customToolsByCategory := make(map[string][]string)
		for toolName, ct := range a.customTools {
			category := ct.Category
			if category == "" {
				continue
			}
			customToolsByCategory[category] = append(customToolsByCategory[category], toolName)
		}
		for category, tools := range customToolsByCategory {
			sort.Strings(tools)
			index[category] = ServerInfo{Tools: tools}
		}
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
