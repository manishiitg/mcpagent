package mcpagent

import "strings"

// Runtime mechanics only. Product procedures belong to caller-owned skills.
// Names come from the same admission predicate as the direct bridge manifest.
func bridgeRoutingExplicitInstructions(admits func(name string) bool, additionalBridgeTools ...string) string {
	allow := func(name string) bool { return admits == nil || admits(name) }
	names := []string{"search_tools", "get_api_spec"}
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "agent_browser"} {
		if allow(name) {
			names = append(names, name)
		}
	}
	for _, name := range additionalBridgeTools {
		if (name == "read_image" || name == "read_skill") && allow(name) {
			names = append(names, name)
		}
	}
	text := "IMPORTANT — bridge tool routing\nDirect runtime tools: " + strings.Join(names, ", ") + ". Use the exact identifiers in your provider's declared tool list.\n"
	for _, name := range names {
		switch name {
		case "read_skill":
			text += "read_skill is intrinsic; never call it over HTTP.\n"
		case "read_image":
			text += "Use read_image for platform image analysis.\n"
		}
	}
	text += "Find other tools with search_tools; get_api_spec(tool_name=...) returns their schema and route. If no match, broaden the query or enumerate a group/server. Only declared runtime tools are direct calls; other tools use the returned HTTP route, never a direct tool call by their bare name. Discovery is live and does not grant permission.\n"
	if allow("execute_shell_command") {
		text += "Call HTTP tools through execute_shell_command: curl --fail-with-body -sS --json '<payload>' -H \"$MCP_AUTH\" \"$MCP_CUSTOM/<tool>\" (real MCP: $MCP_MCP/<server>/<tool>). Custom groups are labels, not URL segments. MCP_AUTH is already the complete `Authorization: Bearer ...` header. --json already selects POST and Content-Type. Keep curl unpiped to preserve its nonzero HTTP-failure status. Encode complex payloads with a JSON encoder rather than hand-quoting. Check the response envelope's success/error before using result. The bridge environment is configured; report actual tool failures.\n"
	}
	return text
}
