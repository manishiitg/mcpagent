package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/toolcalllog"
)

func isLongRunningDelegationTool(tool string) bool {
	switch tool {
	case "call_sub_agent", "call_generic_agent":
		return true
	default:
		return false
	}
}

func resolveCustomToolTimeout(tool string) time.Duration {
	toolTimeout := 10 * time.Minute
	if envVal := os.Getenv("TOOL_EXECUTION_TIMEOUT"); envVal != "" {
		if d, err := time.ParseDuration(envVal); err == nil && d > 0 {
			toolTimeout = d
		}
	}
	if isLongRunningDelegationTool(tool) && toolTimeout < 90*time.Minute {
		return 90 * time.Minute
	}
	return toolTimeout
}

// --- REQUEST/RESPONSE TYPES ---

// MCPExecuteRequest represents a request to execute an MCP tool
type MCPExecuteRequest struct {
	Server    string                 `json:"server"`               // MCP server name (e.g., "aws", "gdrive")
	Tool      string                 `json:"tool"`                 // Tool name (e.g., "list_buckets")
	Args      map[string]interface{} `json:"args"`                 // Tool arguments
	SessionID string                 `json:"session_id,omitempty"` // Optional: MCP session ID for connection reuse
}

// MCPExecuteResponse represents the response from an MCP tool execution
type MCPExecuteResponse struct {
	Success bool   `json:"success"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

// CustomExecuteRequest represents a request to execute a custom tool
type CustomExecuteRequest struct {
	Tool      string                 `json:"tool"`                 // Tool name (e.g., "read_workspace_file")
	Args      map[string]interface{} `json:"args"`                 // Tool arguments
	SessionID string                 `json:"session_id,omitempty"` // Optional: Session ID for scoping custom tools (prevents cross-workflow contamination)
}

// CustomExecuteResponse represents the response from a custom tool execution
type CustomExecuteResponse struct {
	Success bool   `json:"success"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

// VirtualExecuteRequest represents a request to execute a virtual tool
type VirtualExecuteRequest struct {
	Tool      string                 `json:"tool"`                 // Tool name (e.g., "discover_code_structure")
	Args      map[string]interface{} `json:"args"`                 // Tool arguments
	SessionID string                 `json:"session_id,omitempty"` // Optional: Session ID for scoping virtual tools (prevents cross-workflow contamination)
}

// VirtualExecuteResponse represents the response from a virtual tool execution
type VirtualExecuteResponse struct {
	Success bool   `json:"success"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

// --- EXECUTOR HANDLERS ---

// ExecutorHandlers provides HTTP handlers for tool execution endpoints.
// Use NewExecutorHandlers to create and attach to your HTTP mux.
type ExecutorHandlers struct {
	configPath string
	logger     loggerv2.Logger
	// toolArgTransformers maps tool names to functions that mutate their arguments in-place
	// before execution. This is the HTTP handler path (backup) — the primary interception
	// happens in agent/conversation.go for agent-internal tool calls.
	// Example: resolving workspace-relative file paths to absolute for Playwright MCP.
	toolArgTransformers map[string]func(args map[string]interface{})
}

// SetToolArgTransformer registers a function that mutates tool arguments in-place
// before the tool is executed. This covers the HTTP /api/mcp/execute path.
// For agent-internal tool calls (the primary path), use Agent.SetToolArgTransformer instead.
func (h *ExecutorHandlers) SetToolArgTransformer(toolName string, fn func(args map[string]interface{})) {
	if h.toolArgTransformers == nil {
		h.toolArgTransformers = make(map[string]func(args map[string]interface{}))
	}
	h.toolArgTransformers[toolName] = fn
}

// NewExecutorHandlers creates a new ExecutorHandlers instance.
// configPath: path to the MCP servers configuration file
// logger: logger instance for logging
func NewExecutorHandlers(configPath string, logger loggerv2.Logger) *ExecutorHandlers {
	if logger == nil {
		logger = loggerv2.NewNoop()
	}
	return &ExecutorHandlers{
		configPath: configPath,
		logger:     logger,
	}
}

// HandleMCPExecute handles the /api/mcp/execute endpoint
// POST /api/mcp/execute
// Body: {"server": "aws", "tool": "list_buckets", "args": {...}}
// Response: {"success": true, "result": "..."}
func (h *ExecutorHandlers) HandleMCPExecute(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request
	var req MCPExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Failed to decode MCP execute request", loggerv2.Error(err))
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	h.logger.Info("🔧 MCP Execute request",
		loggerv2.String("server", req.Server),
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))

	// Validate request
	if req.Server == "" {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "server parameter is required",
		})
		return
	}

	if req.Tool == "" {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "tool parameter is required",
		})
		return
	}

	// Long-running workflow delegation tools must not inherit cancellation from the
	// caller's HTTP connection. A bridge/client timeout should not kill the delegated
	// workflow after it has already started.
	toolTimeout := resolveCustomToolTimeout(req.Tool)
	baseCtx := r.Context()
	if isLongRunningDelegationTool(req.Tool) {
		baseCtx = context.Background()
		h.logger.Info("⏱️ Using detached long-running context for delegation custom tool",
			loggerv2.String("tool", req.Tool),
			loggerv2.String("timeout", toolTimeout.String()))
	}
	ctx, cancel := context.WithTimeout(baseCtx, toolTimeout)
	defer cancel()

	// Apply tool argument transformers before ANY execution path (session registry, codeexec, mcpcache).
	// This mutates req.Args in-place so all downstream paths see the transformed values.
	// Example: resolves "Downloads/file.pdf" → "/abs/path/workspace-docs/_users/default/Downloads/file.pdf"
	// for Playwright's browser_file_upload tool which requires absolute host paths.
	if h.toolArgTransformers != nil {
		if transformer, ok := h.toolArgTransformers[req.Tool]; ok {
			h.logger.Info("[BROWSER_UPLOAD] Applying tool arg transformer before execution",
				loggerv2.String("tool", req.Tool))
			transformer(req.Args)
		}
	}

	// 🛑 STOPPED SESSION GUARD: If this session was stopped (workflow ended/failed),
	// refuse requests for browser-scoped servers immediately. This prevents in-flight
	// curl calls from code-exec agents (still running in Docker) from spawning new
	// browser processes after the session's connections have been torn down.
	if req.SessionID != "" && mcpclient.IsBrowserScopedServer(req.Server) {
		registry := mcpclient.GetSessionRegistry()
		if registry.IsSessionStopped(req.SessionID) {
			h.logger.Info("🛑 [STOPPED SESSION] Refusing browser tool call for stopped session",
				loggerv2.String("session_id", req.SessionID),
				loggerv2.String("server", req.Server),
				loggerv2.String("tool", req.Tool))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Session %s was stopped — refusing to create new %s connection", req.SessionID, req.Server),
			})
			return
		}
	}

	// 🔧 STRATEGY: Try multiple connection sources in priority order
	// 1. Session registry (if session_id provided) - enables Playwright browser reuse
	// 2. Codeexec global registry - has session-aware connections from agent initialization
	// 3. mcpcache - creates new connection as fallback (NOT for browser-scoped servers)

	var client mcpclient.ClientInterface
	var err error

	// PRIORITY 1: If session_id is provided, try session registry first
	// This is the primary mechanism for connection reuse (e.g., Playwright browser sharing)
	if req.SessionID != "" {
		registry := mcpclient.GetSessionRegistry()
		connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)

		// Debug: List all available sessions
		allSessions := registry.ListSessions()
		h.logger.Info("🔍 [SESSION DEBUG] Available sessions in registry",
			loggerv2.String("requested_session_id", req.SessionID),
			loggerv2.String("resolved_connection_session_id", connSessionID),
			loggerv2.Any("all_sessions", allSessions),
			loggerv2.Int("session_count", len(allSessions)))

		sessionConns := registry.GetSessionConnections(connSessionID)

		// Debug: List all connections for this session
		var connServers []string
		for serverName := range sessionConns {
			connServers = append(connServers, serverName)
		}
		h.logger.Info("🔍 [SESSION DEBUG] Connections for session",
			loggerv2.String("session_id", connSessionID),
			loggerv2.Any("available_servers", connServers),
			loggerv2.String("requested_server", req.Server))

		if existingClient, exists := sessionConns[req.Server]; exists {
			h.logger.Info("✅ Using session registry connection (session-aware)",
				loggerv2.String("session_id", req.SessionID),
				loggerv2.String("server", req.Server))
			client = existingClient
		} else if serverConfig, hasConfig := registry.GetServerConfig(req.SessionID, req.Server); hasConfig {
			// Lazy connect: first actual tool call to this server — connect now
			h.logger.Info("⚡ [LAZY] First tool call to "+req.Server+" — connecting now",
				loggerv2.String("session_id", req.SessionID))
			lazyClient, _, lazyErr := registry.GetOrCreateConnection(ctx, connSessionID, req.Server, serverConfig, h.logger)
			if lazyErr != nil {
				h.logger.Error("Lazy connect failed for server "+req.Server, lazyErr)
			} else {
				client = lazyClient
			}
		} else if connSessionID != req.SessionID {
			// Fallback: config may be stored under the shared browser session ID
			// (e.g. chat session calling playwright — config was primed under browser session)
			if serverConfig, hasConfig := registry.GetServerConfig(connSessionID, req.Server); hasConfig {
				h.logger.Info("⚡ [LAZY] First tool call to "+req.Server+" — connecting via shared browser session config",
					loggerv2.String("session_id", req.SessionID),
					loggerv2.String("browser_session_id", connSessionID))
				lazyClient, _, lazyErr := registry.GetOrCreateConnection(ctx, connSessionID, req.Server, serverConfig, h.logger)
				if lazyErr != nil {
					h.logger.Error("Lazy connect failed for server "+req.Server+" (shared browser config)", lazyErr)
				} else {
					client = lazyClient
				}
			} else {
				h.logger.Info("🔄 Session registry miss, trying codeexec registry",
					loggerv2.String("session_id", req.SessionID),
					loggerv2.String("server", req.Server),
					loggerv2.Any("available_servers", connServers))
			}
		} else {
			h.logger.Info("🔄 Session registry miss, trying codeexec registry",
				loggerv2.String("session_id", req.SessionID),
				loggerv2.String("server", req.Server),
				loggerv2.Any("available_servers", connServers))
		}
	} else {
		h.logger.Info("⚠️ No session_id provided in request, skipping session registry")
	}

	// PRIORITY 2: Try codeexec global registry if no session connection found
	if client == nil {
		resultStr, callErr := codeexec.CallMCPTool(ctx, req.Tool, req.Args)
		if callErr == nil {
			h.logger.Info("✅ Tool executed via codeexec registry",
				loggerv2.String("tool", req.Tool),
				loggerv2.Int("result_length", len(resultStr)))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
				Success: true,
				Result:  resultStr,
			})
			return
		}
		h.logger.Info("🔄 Codeexec registry miss, falling back to mcpcache",
			loggerv2.String("tool", req.Tool),
			loggerv2.String("server", req.Server),
			loggerv2.String("registry_error", callErr.Error()))

		// 🔒 SCOPE ENFORCEMENT: If the session registry is active (has MCP clients) but
		// the requested server is not in scope, deny the request instead of spawning a
		// new browser/process. This prevents agents from reaching MCP servers that are
		// not configured for the current workflow (e.g. playwright in a non-browser workflow).
		if req.SessionID != "" && !codeexec.IsServerInScope(req.Server) {
			availableServers := codeexec.ScopedServerNames()
			h.logger.Warn("🔒 [SCOPE DENIED] Server not in session scope, refusing mcpcache fallback",
				loggerv2.String("server", req.Server),
				loggerv2.String("session_id", req.SessionID),
				loggerv2.Any("available_servers", availableServers))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Server '%s' is not available in this session's scope. The workflow does not have access to this MCP server. Available servers: %v", req.Server, availableServers),
			})
			return
		}
	}

	// PRIORITY 3: Fall back to mcpcache (creates new connection)
	// 🛑 BLOCK for browser-scoped servers: playwright must ONLY be created via
	// the session registry (Priority 1). mcpcache creates standalone connections with
	// default config (wrong --output-dir, no session tracking), causing extra browsers.
	if client == nil && mcpclient.IsBrowserScopedServer(req.Server) {
		h.logger.Warn("🛑 [BROWSER BLOCK] Refusing mcpcache fallback for browser-scoped server — must use session registry",
			loggerv2.String("server", req.Server),
			loggerv2.String("session_id", req.SessionID))
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("No session connection found for browser server %s (session=%s). Browser servers can only be accessed through their owning session.", req.Server, req.SessionID),
		})
		return
	}
	if client == nil {
		h.logger.Warn("⚠️ [SESSION MISS] Falling back to mcpcache - creating new connection",
			loggerv2.String("server", req.Server),
			loggerv2.String("session_id", req.SessionID))
		client, err = GetOrCreateMCPClient(ctx, req.Server, h.configPath, h.logger)
		if err != nil {
			h.logger.Error("Failed to get MCP client", err, loggerv2.String("server", req.Server))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
				Success: false,
				Error:   fmt.Sprintf("Failed to connect to server %s: %v", req.Server, err),
			})
			return
		}
		h.logger.Warn("⚠️ [SESSION MISS] Created new connection via mcpcache",
			loggerv2.String("server", req.Server))
		// Store the new connection in the session registry so future requests reuse it
		if req.SessionID != "" {
			registry := mcpclient.GetSessionRegistry()
			connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
			registry.StoreConnection(connSessionID, req.Server, client)
			h.logger.Info("✅ [SESSION MISS] Stored mcpcache connection in session registry for reuse",
				loggerv2.String("server", req.Server),
				loggerv2.String("session_id", connSessionID))
		}
	}

	// Execute tool
	var argsJSON []byte
	var toolCallID string
	if req.SessionID != "" {
		argsJSON, _ = json.Marshal(req.Args)
		toolCallID = toolcalllog.RecordStart(req.SessionID, req.Tool, string(argsJSON))
	}
	h.logger.Info("🚀 Executing tool via direct connection",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("server", req.Server))
	mcpToolStartTime := time.Now()
	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION START - Time: %s, Tool: %s, Server: %s", mcpToolStartTime.Format(time.RFC3339), req.Tool, req.Server))
	result, err := client.CallTool(ctx, req.Tool, req.Args)

	// 🔧 BROKEN PIPE DETECTION AND RETRY
	// When a workflow is stopped, MCP connections are closed which causes "transport closed"
	// errors. We must NOT retry in that case — otherwise sub-agents continue as zombies.
	if err != nil && mcpclient.IsBrokenPipeError(err) {
		// Guard: skip retry if context is canceled (intentional stop, not transient error)
		if ctx.Err() != nil {
			h.logger.Info("🔧 [BROKEN PIPE] Skipping retry — context canceled (intentional stop)",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server),
				loggerv2.String("ctx_err", ctx.Err().Error()))
		} else if req.SessionID != "" && mcpclient.GetSessionRegistry().IsSessionStopped(req.SessionID) {
			// Guard: skip retry if the session was stopped via CloseHTTPSession
			h.logger.Info("🔧 [BROKEN PIPE] Skipping retry — session stopped (zombie prevention)",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server),
				loggerv2.String("session_id", req.SessionID))
		} else {
			h.logger.Info("🔧 [BROKEN PIPE] Detected, closing old connection and getting fresh one...",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server))

			// Close the old broken connection first to kill the subprocess (prevents zombie browsers)
			if client != nil {
				h.logger.Info("🔧 [BROKEN PIPE] Closing old broken connection",
					loggerv2.String("server", req.Server))
				_ = client.Close()
			}

			// Also close via session registry if session-scoped (stateful servers like playwright)
			if req.SessionID != "" {
				registry := mcpclient.GetSessionRegistry()
				connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
				registry.CloseSessionServer(connSessionID, req.Server)
			}

			// For browser-scoped servers, use session registry to recreate the connection.
			// This ensures the correct runtime overrides (--output-dir) are applied and
			// the connection is tracked. GetFreshConnection creates standalone connections
			// with default config which spawns extra browsers with wrong output paths.
			var freshClient mcpclient.ClientInterface
			var freshErr error
			if req.SessionID != "" && mcpclient.IsBrowserScopedServer(req.Server) {
				registry := mcpclient.GetSessionRegistry()
				connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
				serverConfig, hasConfig := registry.GetServerConfig(req.SessionID, req.Server)
				if !hasConfig && connSessionID != req.SessionID {
					serverConfig, hasConfig = registry.GetServerConfig(connSessionID, req.Server)
				}
				if hasConfig {
					freshClient, _, freshErr = registry.GetOrCreateConnection(ctx, connSessionID, req.Server, serverConfig, h.logger)
				} else {
					h.logger.Warn("🔧 [BROKEN PIPE] No stored config for browser server, cannot retry via registry",
						loggerv2.String("server", req.Server),
						loggerv2.String("session_id", req.SessionID))
					freshErr = fmt.Errorf("no stored config for browser server %s in session %s", req.Server, req.SessionID)
				}
			} else {
				// Non-browser servers: use mcpcache as before
				freshClient, freshErr = mcpcache.GetFreshConnection(ctx, req.Server, h.configPath, h.logger)
			}
			if freshErr == nil {
				h.logger.Info("🔧 [BROKEN PIPE] Retrying with fresh connection...",
					loggerv2.String("tool", req.Tool))
				result, err = freshClient.CallTool(ctx, req.Tool, req.Args)
				if err == nil {
					h.logger.Info("🔧 [BROKEN PIPE] Retry successful",
						loggerv2.String("tool", req.Tool))
				} else {
					h.logger.Error("🔧 [BROKEN PIPE] Retry failed", err,
						loggerv2.String("tool", req.Tool))
				}
			} else {
				h.logger.Error("🔧 [BROKEN PIPE] Failed to get fresh connection", freshErr,
					loggerv2.String("server", req.Server))
			}
		}
	}

	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION END - Time: %s, Tool: %s, Server: %s, Duration: %v", time.Now().Format(time.RFC3339), req.Tool, req.Server, time.Since(mcpToolStartTime)))
	if err != nil {
		h.logger.Error("Tool execution failed", err,
			loggerv2.String("tool", req.Tool),
			loggerv2.String("server", req.Server))
		if req.SessionID != "" {
			toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), fmt.Sprintf("Tool execution failed: %v", err), mcpToolStartTime)
		}
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Tool execution failed: %v", err),
		})
		return
	}

	// Convert result to string
	resultStr := ConvertMCPResultToString(result)

	// 🔧 BROKEN PIPE DETECTION IN RESULT CONTENT
	// MCP servers sometimes return broken pipe errors as text content (success=true, err=nil).
	// The err != nil check above won't catch these — check the result text too.
	if err == nil && mcpclient.IsBrokenPipeInContent(resultStr) {
		// Same guards as above: skip retry if context canceled or session stopped
		if ctx.Err() != nil {
			h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Skipping retry — context canceled",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server))
		} else if req.SessionID != "" && mcpclient.GetSessionRegistry().IsSessionStopped(req.SessionID) {
			h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Skipping retry — session stopped",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server))
		} else {
			h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Detected broken pipe in result text, closing and retrying...",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server),
				loggerv2.String("result_snippet", resultStr[:min(len(resultStr), 200)]))

			if client != nil {
				_ = client.Close()
			}
			if req.SessionID != "" {
				registry := mcpclient.GetSessionRegistry()
				connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
				registry.CloseSessionServer(connSessionID, req.Server)
			}

			// Same browser-scoped logic as the main broken pipe handler:
			// use session registry for browser servers to preserve runtime overrides.
			var freshClient mcpclient.ClientInterface
			var freshErr error
			closeFreshClient := false
			if req.SessionID != "" && mcpclient.IsBrowserScopedServer(req.Server) {
				registry := mcpclient.GetSessionRegistry()
				connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
				serverConfig, hasConfig := registry.GetServerConfig(req.SessionID, req.Server)
				if !hasConfig && connSessionID != req.SessionID {
					serverConfig, hasConfig = registry.GetServerConfig(connSessionID, req.Server)
				}
				if hasConfig {
					freshClient, _, freshErr = registry.GetOrCreateConnection(ctx, connSessionID, req.Server, serverConfig, h.logger)
				} else {
					freshErr = fmt.Errorf("no stored config for browser server %s in session %s", req.Server, req.SessionID)
				}
			} else {
				freshClient, freshErr = mcpcache.GetFreshConnection(ctx, req.Server, h.configPath, h.logger)
				closeFreshClient = true // non-registry connections need explicit close
			}
			if freshErr == nil {
				if closeFreshClient {
					defer freshClient.Close() //nolint:errcheck
				}
				h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Retrying with fresh connection...",
					loggerv2.String("tool", req.Tool))
				retryResult, retryErr := freshClient.CallTool(ctx, req.Tool, req.Args)
				if retryErr == nil {
					resultStr = ConvertMCPResultToString(retryResult)
					h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Retry successful",
						loggerv2.String("tool", req.Tool))
				} else {
					h.logger.Error("🔧 [BROKEN PIPE IN CONTENT] Retry failed", retryErr,
						loggerv2.String("tool", req.Tool))
				}
			} else {
				h.logger.Error("🔧 [BROKEN PIPE IN CONTENT] Failed to get fresh connection", freshErr,
					loggerv2.String("server", req.Server))
			}
		}
	}

	h.logger.Info("✅ Tool executed successfully",
		loggerv2.String("tool", req.Tool),
		loggerv2.Int("result_length", len(resultStr)))

	// Record completed call so LLMAgentWrapper can reconstruct history on cancellation.
	if req.SessionID != "" {
		toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), resultStr, mcpToolStartTime)
	}

	// Return success response
	_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
		Success: true,
		Result:  resultStr,
	})
}

// HandleCustomExecute handles the /api/custom/execute endpoint
// POST /api/custom/execute
// Body: {"tool": "read_workspace_file", "args": {...}}
// Response: {"success": true, "result": "..."}
func (h *ExecutorHandlers) HandleCustomExecute(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request
	var req CustomExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Failed to decode custom execute request", loggerv2.Error(err))
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	h.logger.Info("🔧 Custom Execute request",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))

	// Validate request
	if req.Tool == "" {
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "tool parameter is required",
		})
		return
	}

	// Create context with timeout — reuse resolveCustomToolTimeout so delegation
	// custom tools (call_sub_agent, call_generic_agent) get the same 90-min
	// detached context as their MCP-handler counterparts.
	toolTimeout := resolveCustomToolTimeout(req.Tool)
	baseCtx := r.Context()
	if isLongRunningDelegationTool(req.Tool) {
		baseCtx = context.Background()
		h.logger.Info("⏱️ Using detached long-running context for delegation custom tool",
			loggerv2.String("tool", req.Tool),
			loggerv2.String("timeout", toolTimeout.String()))
	}
	ctx, cancel := context.WithTimeout(baseCtx, toolTimeout)
	defer cancel()

	// Execute custom tool using codeexec registry (session-scoped to prevent cross-workflow contamination)
	var argsJSON []byte
	var toolCallID string
	var toolStartedAt time.Time
	if req.SessionID != "" {
		argsJSON, _ = json.Marshal(req.Args)
		toolStartedAt = time.Now()
		toolCallID = toolcalllog.RecordStart(req.SessionID, req.Tool, string(argsJSON))
	}
	h.logger.Info("🚀 Executing custom tool",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))
	toolStartTime := time.Now()
	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION START - Time: %s, Tool: %s", toolStartTime.Format(time.RFC3339), req.Tool))
	result, err := codeexec.CallCustomToolWithSession(ctx, req.SessionID, req.Tool, req.Args)
	toolDuration := time.Since(toolStartTime)
	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION END - Time: %s, Tool: %s, Duration: %v", time.Now().Format(time.RFC3339), req.Tool, toolDuration))
	if err != nil {
		h.logger.Error("Custom tool execution failed", err, loggerv2.String("tool", req.Tool))
		if req.SessionID != "" {
			toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), fmt.Sprintf("Custom tool execution failed: %v", err), toolStartedAt)
		}
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Custom tool execution failed: %v", err),
		})
		return
	}

	h.logger.Info("✅ Custom tool executed successfully",
		loggerv2.String("tool", req.Tool),
		loggerv2.Int("result_length", len(result)))

	// Record completed call so LLMAgentWrapper can reconstruct history on cancellation.
	if req.SessionID != "" {
		toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), result, toolStartedAt)
	}

	// Return success response
	_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
		Success: true,
		Result:  result,
	})
}

// HandleVirtualExecute handles the /api/virtual/execute endpoint
// POST /api/virtual/execute
// Body: {"tool": "discover_code_structure", "args": {...}}
// Response: {"success": true, "result": "..."}
func (h *ExecutorHandlers) HandleVirtualExecute(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request
	var req VirtualExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Failed to decode virtual execute request", loggerv2.Error(err))
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	h.logger.Info("🔧 Virtual Execute request",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))

	// Validate request
	if req.Tool == "" {
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "tool parameter is required",
		})
		return
	}

	// Create context with timeout — reuse resolveCustomToolTimeout so delegation
	// virtual tools (call_sub_agent, call_generic_agent) get the same 90-min
	// detached context as their custom-tool counterparts.
	toolTimeout := resolveCustomToolTimeout(req.Tool)
	baseCtx := r.Context()
	if isLongRunningDelegationTool(req.Tool) {
		baseCtx = context.Background()
		h.logger.Info("⏱️ Using detached long-running context for delegation virtual tool",
			loggerv2.String("tool", req.Tool),
			loggerv2.String("timeout", toolTimeout.String()))
	}
	ctx, cancel := context.WithTimeout(baseCtx, toolTimeout)
	defer cancel()

	// Execute virtual tool using codeexec registry (session-scoped to prevent cross-workflow contamination)
	h.logger.Info("🚀 Executing virtual tool",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))
	result, err := codeexec.CallVirtualToolWithSession(ctx, req.SessionID, req.Tool, req.Args)
	if err != nil {
		h.logger.Error("Virtual tool execution failed", err, loggerv2.String("tool", req.Tool))
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Virtual tool execution failed: %v", err),
		})
		return
	}

	h.logger.Info("✅ Virtual tool executed successfully",
		loggerv2.String("tool", req.Tool),
		loggerv2.Int("result_length", len(result)))

	// Return success response
	_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
		Success: true,
		Result:  result,
	})
}
