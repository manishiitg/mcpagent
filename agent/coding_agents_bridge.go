package mcpagent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// BridgeToolDef is the serialized tool definition for the MCP bridge binary.
type BridgeToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Server      string          `json:"server,omitempty"` // MCP server name (empty for custom/virtual)
	Type        string          `json:"type"`             // "mcp", "custom", or "virtual"
}

// bridgeTools is the explicit list of tools exposed through the coding-agent MCP
// bridge as native MCP tools. All other MCP/custom tools are discovered via
// get_api_spec and called through HTTP API endpoints.
var bridgeTools = []struct {
	name     string
	toolType string // "custom" or "virtual"
}{
	{"execute_shell_command", "custom"},
	{"diff_patch_workspace_file", "custom"},
	{"agent_browser", "custom"},
	{"get_api_spec", "virtual"},
}

// claudeBridgeAllowedToolIdentifiers returns the full "mcp__api-bridge__<name>"
// identifier for every tool actually exposed through the bridge MCP server —
// the fixed core set (bridgeTools) plus whatever a caller registered via
// WithAdditionalBridgeTools — deduped the same way BuildBridgeMCPConfig itself
// dedupes them. This is the single source of truth for Claude's tool-name
// allowlist, used both for --allowedTools and the enforced-mode PreToolUse
// hook, so an additional bridge tool is never silently unusable in one path
// while working in the other.
func claudeBridgeAllowedToolIdentifiers(additional []string) []string {
	seen := make(map[string]bool, len(bridgeTools)+len(additional))
	names := make([]string, 0, len(bridgeTools)+len(additional))
	for _, want := range bridgeTools {
		if seen[want.name] {
			continue
		}
		seen[want.name] = true
		names = append(names, want.name)
	}
	for _, name := range additional {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	identifiers := make([]string, 0, len(names))
	for _, name := range names {
		identifiers = append(identifiers, "mcp__api-bridge__"+name)
	}
	return identifiers
}

// BuildBridgeMCPConfig creates an MCP config that launches the mcpbridge binary
// as a stdio server, forwarding tool calls to the HTTP API endpoints.
// This is used by CLI-native coding agents to access selected tools natively
// via the bridge.
//
// The bridge exposes a small native tool set: execute_shell_command,
// diff_patch_workspace_file, agent_browser, and get_api_spec. All other MCP
// tools are discovered via get_api_spec and called through HTTP API endpoints.
func (a *Agent) BuildBridgeMCPConfig() (string, error) {
	logger := getLogger(a)

	// 1. Resolve bridge binary path
	bridgePath := os.Getenv("MCP_BRIDGE_BINARY")
	if bridgePath == "" {
		var err error
		bridgePath, err = exec.LookPath("mcpbridge")
		if err != nil {
			// Fallback: check ~/go/bin/mcpbridge (common Go install location)
			if home, hErr := os.UserHomeDir(); hErr == nil {
				candidate := home + "/go/bin/mcpbridge"
				if _, sErr := os.Stat(candidate); sErr == nil {
					bridgePath = candidate
				}
			}
			if bridgePath == "" {
				return "", fmt.Errorf("mcpbridge binary not found in PATH or ~/go/bin/ (set MCP_BRIDGE_BINARY env var): %w", err)
			}
		}
	}

	// 2. Collect the bridge tools by name — the fixed shared set (bridgeTools)
	// plus whatever this agent instance registered via WithAdditionalBridgeTools.
	logger.Debug("BuildBridgeMCPConfig: agent state",
		loggerv2.Int("tools_count", len(a.Tools)),
		loggerv2.Int("custom_tools_count", len(a.customTools)),
		loggerv2.Int("additional_bridge_tools_count", len(a.additionalBridgeTools)),
		loggerv2.Any("use_code_execution_mode", a.UseCodeExecutionMode))

	seen := make(map[string]bool, len(bridgeTools)+len(a.additionalBridgeTools))
	wanted := make([]struct {
		name     string
		toolType string
	}, 0, len(bridgeTools)+len(a.additionalBridgeTools))
	for _, want := range bridgeTools {
		seen[want.name] = true
		wanted = append(wanted, want)
	}
	for _, name := range a.additionalBridgeTools {
		// A caller's additional-tools list may legitimately name one of the
		// fixed defaults above (e.g. it registers its own "agent_browser"
		// executor) — skip the duplicate rather than emitting the same tool
		// name twice in the generated MCP config.
		if seen[name] {
			continue
		}
		seen[name] = true
		wanted = append(wanted, struct {
			name     string
			toolType string
		}{name, "custom"})
	}

	var toolDefs []BridgeToolDef
	for _, want := range wanted {
		def := a.lookupBridgeTool(want.name, want.toolType, logger)
		if def == nil {
			def = defaultBridgeToolDef(want.name, want.toolType, logger)
		}
		if def != nil {
			toolDefs = append(toolDefs, *def)
		} else {
			logger.Warn("Bridge tool not found — skipping",
				loggerv2.String("tool", want.name),
				loggerv2.String("type", want.toolType))
		}
	}

	// 3. Resolve API URL and token for the bridge process
	// The bridge binary runs on the host (not in Docker), so it needs a host-reachable URL.
	// MCP_BRIDGE_API_URL overrides MCP_API_URL for this purpose.
	apiURL := os.Getenv("MCP_BRIDGE_API_URL")
	if apiURL == "" {
		apiURL = a.APIBaseURL
	}
	if apiURL == "" {
		apiURL = os.Getenv("MCP_API_URL")
	}
	apiToken := a.APIToken
	if apiToken == "" {
		apiToken = os.Getenv("MCP_API_TOKEN")
	}
	if apiURL == "" {
		return "", fmt.Errorf("API base URL not configured (set APIBaseURL or MCP_API_URL)")
	}
	apiURL, err := normalizeBridgeAPIURL(apiURL)
	if err != nil {
		return "", err
	}
	if apiToken == "" {
		return "", fmt.Errorf("API token not configured (set APIToken or MCP_API_TOKEN)")
	}

	// In Approach C, we do NOT embed the session ID in the API URL path.
	// Instead, the MCP_API_URL remains static, and we pass the session ID via
	// the MCP_SESSION_ID environment variable. This ensures that the generated
	// mcp-config JSON can be easily normalized or kept identical across sessions.
	// We no longer append session ID path prefixes here.

	// 4. Build MCP config JSON
	toolsJSON, err := json.Marshal(toolDefs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool definitions: %w", err)
	}

	bridgeEnv := map[string]string{
		"MCP_API_URL":   apiURL,
		"MCP_API_TOKEN": apiToken,
		"MCP_TOOLS":     string(toolsJSON),
	}
	if a.SessionID != "" {
		bridgeEnv["MCP_SESSION_ID"] = a.SessionID
	}
	// Route mcpbridge stderr to a log file for debugging startup/crash issues.
	// Claude Code swallows the subprocess stderr, so without this there is no
	// record of why the bridge failed (e.g. empty MCP_TOOLS, parse errors, crashes).
	bridgeEnv["MCP_BRIDGE_LOG"] = os.TempDir() + "/mcpbridge.log"
	// Allocate a fresh, unique readiness-marker path for THIS launch. The bridge
	// creates it on tools/list (tools connected); the adapter waits for it before
	// a cold session's first prompt. A unique temp path per call (removed here so
	// only this launch's bridge can create it) means a stale marker from a prior
	// session in the same workspace can never falsely satisfy the gate.
	a.bridgeReadyFile = ""
	if f, tmpErr := os.CreateTemp("", "mcpbridge-ready-*.marker"); tmpErr == nil {
		readyPath := f.Name()
		_ = f.Close()
		_ = os.Remove(readyPath)
		a.bridgeReadyFile = readyPath
		bridgeEnv["MCP_READY_FILE"] = readyPath
	} else {
		logger.Warn("Failed to allocate MCP readiness marker; cold-turn tool-connect gate disabled for this launch",
			loggerv2.Error(tmpErr))
	}
	// Pass per-agent virtual tool scope so the bridge can route get_api_spec
	// to the correct agent's handler (prevents parent/child overwrite)
	if virtualScopeID := a.GetVirtualToolScopeID(); virtualScopeID != "" {
		bridgeEnv["MCP_VIRTUAL_SCOPE_ID"] = virtualScopeID
	}

	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"api-bridge": map[string]interface{}{
				"command": bridgePath,
				"args":    []string{},
				"env":     bridgeEnv,
				"trust":   true, // Auto-trust: bypass confirmation dialogs for non-interactive usage
			},
		},
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal MCP config: %w", err)
	}

	logger.Info("Built bridge MCP config",
		loggerv2.Int("tool_count", len(toolDefs)),
		loggerv2.String("bridge_path", bridgePath))

	return string(configJSON), nil
}

func normalizeBridgeAPIURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("API base URL not configured (set APIBaseURL or MCP_API_URL)")
	}
	if strings.HasPrefix(trimmed, "[") {
		if close := strings.Index(trimmed, "]("); close > 1 && strings.HasSuffix(trimmed, ")") {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed[close+2:], ")"))
		}
	}
	trimmed = strings.Trim(trimmed, "<>")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid MCP bridge API URL %q; expected plain http(s) URL", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid MCP bridge API URL %q; expected http(s) URL", raw)
	}
	return trimmed, nil
}

func defaultBridgeToolDef(name, toolType string, logger loggerv2.Logger) *BridgeToolDef {
	if toolType != "custom" {
		return nil
	}
	switch name {
	case "execute_shell_command":
		return marshalBridgeToolDef(name, codeexec.ShellCommandDescription, codeexec.ShellCommandParams, toolType, logger)
	case "diff_patch_workspace_file":
		return marshalBridgeToolDef(name, "Apply a unified diff patch to a workspace file.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"filepath": map[string]interface{}{
					"type":        "string",
					"description": "Workspace-relative path or absolute path under the workspace docs root.",
				},
				"diff": map[string]interface{}{
					"type":        "string",
					"description": "Unified diff content to apply.",
				},
			},
			"required": []string{"filepath", "diff"},
		}, toolType, logger)
	case "agent_browser":
		return marshalBridgeToolDef(name, "Control a browser agent session.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Browser command such as get, snapshot, click, type, wait, or tab.",
				},
			},
			"required": []string{"command"},
		}, toolType, logger)
	default:
		return nil
	}
}

// lookupBridgeTool finds a tool by name from the agent's registered tools.
// "custom" tools are in a.customTools, "virtual" tools are in a.Tools.
func (a *Agent) lookupBridgeTool(name, toolType string, logger loggerv2.Logger) *BridgeToolDef {
	switch toolType {
	case "custom":
		ct, ok := a.customTools[name]
		if !ok {
			return nil
		}
		description := ""
		var parameters interface{}
		if ct.Definition.Function != nil {
			description = ct.Definition.Function.Description
			parameters = ct.Definition.Function.Parameters
		}
		return marshalBridgeToolDef(name, description, parameters, toolType, logger)

	case "virtual":
		for _, tool := range a.Tools {
			if tool.Function != nil && tool.Function.Name == name {
				return marshalBridgeToolDef(name, tool.Function.Description, tool.Function.Parameters, toolType, logger)
			}
		}
		// Coding-agent bridges must always be able to discover HTTP-backed tools,
		// even when the current provider path filtered virtual tools out of a.Tools.
		// Recreate virtual definitions here so get_api_spec remains available while
		// advanced/custom tools stay behind API discovery.
		for _, tool := range a.CreateVirtualTools() {
			if tool.Function != nil && tool.Function.Name == name {
				return marshalBridgeToolDef(name, tool.Function.Description, tool.Function.Parameters, toolType, logger)
			}
		}
		return nil

	default:
		return nil
	}
}

// marshalBridgeToolDef creates a BridgeToolDef, marshalling parameters to JSON.
func marshalBridgeToolDef(name, description string, parameters interface{}, toolType string, logger loggerv2.Logger) *BridgeToolDef {
	var inputSchema json.RawMessage
	if parameters != nil {
		schemaBytes, err := json.Marshal(parameters)
		if err != nil {
			logger.Warn("Failed to marshal tool parameters, skipping",
				loggerv2.String("tool", name), loggerv2.Error(err))
			return nil
		}
		inputSchema = schemaBytes
	}
	return &BridgeToolDef{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
		Type:        toolType,
	}
}
