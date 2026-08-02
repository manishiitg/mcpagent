package mcpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache/openapi"
	"github.com/manishiitg/mcpagent/mcpclient"
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
	if serverName != "" && a.logger != nil {
		a.logger.Debug("get_api_spec: server_name is compatibility-only; resolving by tool name",
			loggerv2.String("server_name", serverName),
			loggerv2.Any("tool_names", sortedNames))
	}

	// Resolve and authorize every name before consulting the schema cache. A
	// cache hit is metadata reuse, never an authorization decision.
	registry, err := a.canonicalRegistry()
	if err != nil {
		return "", err
	}
	customToolsForSpec := make(map[string]openapi.CustomToolForOpenAPI)
	mcpToolsByServer := make(map[string][]llmtypes.Tool)
	var unknown, notAllowed []string
	for _, name := range sortedNames {
		if !a.isToolAllowedForContext(ctx, name) {
			notAllowed = append(notAllowed, name)
			continue
		}
		registered, ok := registry.lookup(name)
		if !ok || registered.Kind == toolImplementationVirtual {
			unknown = append(unknown, name)
			continue
		}
		if registered.Kind == toolImplementationDirect {
			customToolsForSpec[name] = openapi.CustomToolForOpenAPI{
				Definition: registered.Definition,
				Category:   registered.DisplayGroup,
			}
			continue
		}

		srvName := registered.Source
		if !a.serverIsAvailable(srvName) {
			notAllowed = append(notAllowed, name)
			continue
		}

		normalizedServer := strings.ReplaceAll(srvName, "-", "_")
		mcpToolsByServer[normalizedServer] = append(mcpToolsByServer[normalizedServer], registered.Definition)
	}

	if len(unknown) > 0 || len(notAllowed) > 0 {
		return "", a.unavailableToolsError(registry, serverName, unknown, notAllowed)
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

// unavailableToolsError explains why a get_api_spec request could not be
// answered. The distinction it draws is the whole point: a name that resolves
// nowhere reads identically whether the model invented it or whether the server
// that owns it never came up, and the old wording asserted the first
// unconditionally. On 2026-08-01 a run asked google_sheets for five correct
// tool names while that server was failing to connect, was told
// "unknown=[batch_update get_sheet_formulas ...]", concluded its names were
// wrong, and spent the next call guessing new ones. No sequence of names could
// have worked, so the error has to say so.
//
// requestedServer is the model-supplied server_name. It never affects routing —
// only whether this message can name the failing server outright.
func (a *Agent) unavailableToolsError(registry *canonicalToolRegistry, requestedServer string, unknown, notAllowed []string) error {
	if len(unknown) == 0 {
		// Permission denials are already attributed correctly; leave them byte-identical.
		return fmt.Errorf("tools_unavailable: unknown=%v not_allowed=%v", unknown, notAllowed)
	}

	missing := a.serversMissingTools(registry)
	blamed := a.blameMissingServer(registry, requestedServer, missing)

	var b strings.Builder
	if len(blamed) > 0 {
		fmt.Fprintf(&b, "tools_unavailable: server_unavailable=%v requested=%v not_allowed=%v: ", blamed, unknown, notAllowed)
		fmt.Fprintf(&b, "MCP server(s) %v are configured for this agent but currently have zero registered tools, so %s. ",
			blamed, a.missingServerCause(blamed))
		b.WriteString("The requested tool names are most likely correct — retrying with different or guessed tool names will NOT help. ")
		b.WriteString("Treat this as a server outage: report it and continue without those tools.")
		return errors.New(b.String())
	}

	fmt.Fprintf(&b, "tools_unavailable: unknown=%v not_allowed=%v: ", unknown, notAllowed)
	b.WriteString("these names are not registered by any currently connected server. ")
	b.WriteString("Use the exact tool names from the tool index in your system prompt — do not guess variants. ")
	if len(missing) > 0 {
		fmt.Fprintf(&b, "Note: MCP server(s) %v are configured but currently have zero registered tools (failed to start or connect); "+
			"if a requested tool belongs to one of them, no tool name will work.", missing)
	} else {
		b.WriteString("An MCP server that fails to start produces this same symptom, " +
			"so a tool you expected to exist may be missing for reasons unrelated to its name.")
	}
	return errors.New(b.String())
}

// blameMissingServer decides whether a server outage can be named as the cause
// rather than merely mentioned. Two cases are certain enough to assert:
// the model named a server that is configured yet holds no tools, or no MCP
// server registered a single tool, in which case no MCP name could resolve.
// Everything else stays a note, because a hallucinated name and a down server
// are genuinely indistinguishable once other servers are serving tools.
func (a *Agent) blameMissingServer(registry *canonicalToolRegistry, requestedServer string, missing []string) []string {
	if len(missing) == 0 {
		return nil
	}
	if requested := normalizeServerKey(requestedServer); requested != "" {
		for _, name := range missing {
			if normalizeServerKey(name) == requested {
				return []string{name}
			}
		}
	}
	if !registryHasMCPTools(registry) {
		return missing
	}
	return nil
}

// missingServerCause distinguishes "never connected" from "connected and
// advertised nothing". Both are server-availability problems, but naming the
// wrong one sends a human debugging in the wrong direction.
func (a *Agent) missingServerCause(missing []string) string {
	for _, name := range missing {
		if !a.hasMCPClient(name) {
			return "they failed to start or failed to connect"
		}
	}
	return "they connected but advertised no tools"
}

// serversMissingTools returns the MCP servers this agent was configured to use
// that own zero tools in the canonical registry. Connection results are not
// usable for this: a server that fails to connect is dropped from both
// a.clients and a.servers, so only the configured request still remembers it.
func (a *Agent) serversMissingTools(registry *canonicalToolRegistry) []string {
	withTools := make(map[string]struct{})
	for _, tool := range registry.snapshot() {
		if tool.Kind != toolImplementationMCP || tool.Source == "" {
			continue
		}
		withTools[normalizeServerKey(tool.Source)] = struct{}{}
	}

	var missing []string
	for _, name := range a.configuredMCPServers() {
		if _, ok := withTools[normalizeServerKey(name)]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// configuredMCPServers lists the MCP servers this agent asked for, as spelled by
// the caller. serverName holds the constructor's comma-separated request and
// selectedServers holds an explicit restriction; neither is rewritten by the
// connection result, which is what makes a failed server still visible here.
// "all" is deliberately not expanded — resolving it means re-reading the config
// from an error path, and asserting an outage from a stale read is worse than
// staying quiet.
func (a *Agent) configuredMCPServers() []string {
	var names []string
	seen := make(map[string]struct{})
	add := func(raw string) {
		name := strings.TrimSpace(raw)
		if name == "" || name == "all" || name == mcpclient.NoServers {
			return
		}
		key := normalizeServerKey(name)
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	for _, name := range strings.Split(a.serverName, ",") {
		add(name)
	}
	for _, name := range a.selectedServers {
		add(name)
	}
	sort.Strings(names)
	return names
}

func (a *Agent) hasMCPClient(serverName string) bool {
	key := normalizeServerKey(serverName)
	for name := range a.clients {
		if normalizeServerKey(name) == key {
			return true
		}
	}
	return false
}

func registryHasMCPTools(registry *canonicalToolRegistry) bool {
	for _, tool := range registry.snapshot() {
		if tool.Kind == toolImplementationMCP {
			return true
		}
	}
	return false
}

// normalizeServerKey collapses the spellings the same server travels under:
// config uses hyphens, HTTP routes and the tool index use underscores.
func normalizeServerKey(serverName string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(serverName), "-", "_"))
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

func (a *Agent) getCodeExecutionAPIBaseURL() string {
	baseURL := a.apiBaseURL
	if baseURL == "" {
		baseURL = os.Getenv("MCP_API_URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:8000"
	}

	if a.sessionID == "" {
		return strings.TrimRight(baseURL, "/")
	}

	sessionPrefix := "/s/" + a.sessionID
	if strings.Contains(baseURL, sessionPrefix) {
		return strings.TrimRight(baseURL, "/")
	}

	return strings.TrimRight(baseURL, "/") + sessionPrefix
}

// buildToolIndex returns a JSON index of available servers and their tool names.
// This is included in the system prompt so the LLM knows what's available.
// It builds the index purely from agent internal state (no filesystem scanning).
func (a *Agent) buildToolIndex() (string, error) {
	return a.buildToolIndexForContext(context.Background())
}

func (a *Agent) buildToolIndexForContext(ctx context.Context) (string, error) {
	type ServerInfo struct {
		Tools []string `json:"tools"`
	}

	index := make(map[string]ServerInfo)

	registry, err := a.canonicalRegistry()
	if err != nil {
		return "", err
	}

	// Build the index from one canonical registry. Categories are display-only;
	// every callable address remains the globally unique tool name.
	serverToolsMap := make(map[string]map[string]bool)
	customToolsByCategory := make(map[string][]string)
	var blockedCustomTools []string
	for _, registered := range registry.snapshot() {
		toolName := registered.Name
		if registered.Kind == toolImplementationVirtual {
			continue
		}
		if !a.isToolAllowedForContext(ctx, toolName) {
			if registered.Kind == toolImplementationDirect {
				blockedCustomTools = append(blockedCustomTools, toolName)
			}
			continue
		}
		if registered.Kind == toolImplementationDirect {
			if registered.DisplayGroup != "" {
				customToolsByCategory[registered.DisplayGroup] = append(customToolsByCategory[registered.DisplayGroup], toolName)
			}
			continue
		}

		serverName := registered.Source

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

	// Add direct tools grouped by optional display metadata.
	// Even in code execution mode, custom tools must appear here so that Claude Code
	// (which uses the MCP bridge and can only discover tools via get_api_spec) can
	// find and call them via HTTP API. For non-Claude-Code providers, the tools are
	// also available as direct LLM calls — having them in the index is harmless.
	// Respect toolAllowList: if set, only include allowed custom tools in the index.
	if a.logger != nil && len(blockedCustomTools) > 0 {
		sort.Strings(blockedCustomTools)
		a.logger.Info("🔒 [TOOL_ALLOW_LIST] buildToolIndex blocked custom tools",
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

	if a.logger != nil {
		totalTools := 0
		for _, pkg := range index {
			totalTools += len(pkg.Tools)
		}
		a.logger.Info("Built tool index",
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
	agentDir := filepath.Join(baseDir, "agents", string(a.traceID))

	if a.useCodeExecutionMode {
		if err := os.MkdirAll(agentDir, 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for user-accessible directories
			if a.logger != nil {
				a.logger.Warn("Failed to create agent generated directory", loggerv2.String("agent_dir", agentDir), loggerv2.Error(err))
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
