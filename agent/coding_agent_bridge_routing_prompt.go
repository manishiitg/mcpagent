package mcpagent

import "strings"

// Runtime mechanics only. Product procedures belong to caller-owned skills.
// Names come from the same admission predicate as the direct bridge manifest.
func bridgeRoutingExplicitInstructions(admits func(name string) bool, additionalBridgeTools ...string) string {
	return bridgeRoutingInstructions(admits, false, additionalBridgeTools...)
}

func bridgeRoutingInstructions(admits func(name string) bool, skillFirst bool, additionalBridgeTools ...string) string {
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
	text += "Find other tools with search_tools; get_api_spec(tool_name=...) returns their schema and route. A tool missing from your own tool list or your runtime's own tool search is not missing: use search_tools. If no match, broaden the query or enumerate a group/server. Only declared runtime tools are direct calls; other tools use the returned HTTP route, never a direct tool call by their bare name. Discovery is live and does not grant permission.\n"
	if allow("execute_shell_command") {
		if skillFirst {
			text += "Before HTTP execution, read the attached runtime-http-tools skill. Use the authorized shell route and current schema; never print credentials or ignore a failed response.\n"
		} else {
			text += runtimeHTTPInstructions
		}
	}
	return text
}
