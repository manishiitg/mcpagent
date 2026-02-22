package executor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// RegisterPerToolEndpoints registers per-tool HTTP endpoints that route to existing execution logic.
// This provides clean REST-style URLs that code can call directly:
//   - POST /tools/mcp/{server}/{tool}  -> delegates to HandleMCPExecute logic
//   - POST /tools/custom/{tool}        -> delegates to HandleCustomExecute logic
//
// These endpoints accept the tool arguments directly in the request body (no server/tool wrapper).
func (h *ExecutorHandlers) RegisterPerToolEndpoints(
	mux *http.ServeMux,
	mcpServers map[string][]string, // server name -> tool names
	customToolNames []string,
) {
	// Register MCP server tool endpoints
	for serverName, toolNames := range mcpServers {
		for _, toolName := range toolNames {
			// Capture loop variables for closure
			sn := serverName
			tn := toolName
			pattern := fmt.Sprintf("POST /tools/mcp/%s/%s", sanitizeURLSegment(sn), sanitizeURLSegment(tn))
			mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				h.handlePerToolMCP(w, r, sn, tn)
			})
		}
	}

	// Register custom tool endpoints
	for _, toolName := range customToolNames {
		tn := toolName
		pattern := fmt.Sprintf("POST /tools/custom/%s", sanitizeURLSegment(tn))
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			h.handlePerToolCustom(w, r, tn)
		})
	}

	h.logger.Info("Registered per-tool endpoints",
		loggerv2.Int("mcp_servers", len(mcpServers)),
		loggerv2.Int("custom_tools", len(customToolNames)))
}

// HandlePerToolMCPRequest is the public entry point for per-tool MCP requests.
// It handles requests to /tools/mcp/{server}/{tool}, extracting args from the body
// and delegating to the standard MCP execution logic.
func (h *ExecutorHandlers) HandlePerToolMCPRequest(w http.ResponseWriter, r *http.Request, server, tool string) {
	h.handlePerToolMCP(w, r, server, tool)
}

// HandlePerToolCustomRequest is the public entry point for per-tool custom requests.
// It handles requests to /tools/custom/{tool}, extracting args from the body
// and delegating to the standard custom tool execution logic.
func (h *ExecutorHandlers) HandlePerToolCustomRequest(w http.ResponseWriter, r *http.Request, tool string) {
	h.handlePerToolCustom(w, r, tool)
}

// handlePerToolMCP handles requests to /tools/mcp/{server}/{tool}.
// It extracts the tool arguments from the request body and delegates to the standard MCP execution logic.
func (h *ExecutorHandlers) handlePerToolMCP(w http.ResponseWriter, r *http.Request, server, tool string) {
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request body as tool arguments
	var args map[string]interface{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			h.logger.Warn("Failed to decode per-tool MCP request body", loggerv2.Error(err))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	// Extract optional session_id from args (and remove it so it's not passed as tool arg)
	sessionID := ""
	if sid, ok := args["session_id"].(string); ok {
		sessionID = sid
		delete(args, "session_id")
	}

	h.logger.Info("Per-tool MCP request",
		loggerv2.String("server", server),
		loggerv2.String("tool", tool),
		loggerv2.String("session_id", sessionID))

	// Build the standard MCPExecuteRequest and reuse the full handler logic
	// We rewrite the request to use the standard HandleMCPExecute
	wrappedBody := MCPExecuteRequest{
		Server:    server,
		Tool:      tool,
		Args:      args,
		SessionID: sessionID,
	}

	bodyBytes, err := json.Marshal(wrappedBody)
	if err != nil {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}

	// Create a new request with the wrapped body
	newReq, err := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), strings.NewReader(string(bodyBytes)))
	if err != nil {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}
	newReq.Header.Set("Content-Type", "application/json")

	// Delegate to existing handler
	h.HandleMCPExecute(w, newReq)
}

// handlePerToolCustom handles requests to /tools/custom/{tool}.
func (h *ExecutorHandlers) handlePerToolCustom(w http.ResponseWriter, r *http.Request, tool string) {
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request body as tool arguments
	var args map[string]interface{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			h.logger.Warn("Failed to decode per-tool custom request body", loggerv2.Error(err))
			_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	// Extract optional session_id
	sessionID := ""
	if sid, ok := args["session_id"].(string); ok {
		sessionID = sid
		delete(args, "session_id")
	}

	h.logger.Info("Per-tool custom request",
		loggerv2.String("tool", tool),
		loggerv2.String("session_id", sessionID))

	// Build the standard CustomExecuteRequest and reuse the full handler logic
	wrappedBody := CustomExecuteRequest{
		Tool:      tool,
		Args:      args,
		SessionID: sessionID,
	}

	bodyBytes, err := json.Marshal(wrappedBody)
	if err != nil {
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}

	newReq, err := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), strings.NewReader(string(bodyBytes)))
	if err != nil {
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}
	newReq.Header.Set("Content-Type", "application/json")

	h.HandleCustomExecute(w, newReq)
}

// sanitizeURLSegment makes a name safe for use in URL paths.
// Replaces hyphens with underscores and lowercases.
func sanitizeURLSegment(name string) string {
	result := strings.ReplaceAll(name, "-", "_")
	return strings.ToLower(result)
}
