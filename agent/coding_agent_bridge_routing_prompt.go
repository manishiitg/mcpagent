package mcpagent

// bridgeRoutingExplicitInstructions returns a system-prompt block
// that explicitly names the api-bridge tools the model should
// substitute when its built-in equivalents are denied. Without
// this preamble, the model sees a bare "denied" verdict (cursor's
// .cursor/cli.json short-circuits the hook system so the hook's
// rich agent_message never reaches the model) and falls back to
// "I have no MCP server configuration / no API tokens" rather than
// calling the bridge.
//
// Spelled out verbatim with signatures so the model has no excuse
// to give up — the bridge IS configured, the env IS wired, the
// tools ARE registered, and these names are exact.
//
// Applied via AppendSystemPrompt for every coding-agent CLI we
// front (cursor / agy / claude-code / codex / gemini)
// so the routing instruction is delivered the same way across
// providers.
func bridgeRoutingExplicitInstructions() string {
	return "IMPORTANT — bridge tool routing (use these EXACT names when your built-ins are denied):\n" +
		"  • api-bridge.execute_shell_command / api_bridge_execute_shell_command(command, timeout?) — shell (cat, ls, jq, python3, curl, any *nix command). USE INSTEAD OF: built-in Shell / Bash / run_command / view_file.\n" +
		"  • api-bridge.diff_patch_workspace_file / api_bridge_diff_patch_workspace_file(filepath, diff) — apply a unified diff to a workspace file. USE INSTEAD OF: built-in Edit / Write / write_to_file / replace_file_content / multi_replace_file_content.\n" +
		"  • api-bridge.get_api_spec / api_bridge_get_api_spec(server_name, tool_name) — fetch the OpenAPI spec for tools on other MCP servers or custom categories (e.g. google_sheets, playwright, sub_agent_tools). Use this to discover server-specific tools, then call them via execute_shell_command + curl / python3.\n" +
		"  • In Pi CLI, use mcp({ search: \"tool words\" }), mcp({ describe: \"api_bridge_execute_shell_command\" }), then mcp({ tool: \"api_bridge_execute_shell_command\", args: \"{...json...}\" }) for the documented bridge tools when direct api_bridge_* names are not visible.\n" +
		"  • Custom tools can also be called through execute_shell_command + curl using $MCP_CUSTOM and $MCP_AUTH. For LLM/provider configuration, use $MCP_CUSTOM/list_published_llms, $MCP_CUSTOM/list_provider_models, $MCP_CUSTOM/test_llm, $MCP_CUSTOM/save_published_llm, and $MCP_CUSTOM/set_provider_auth. Do not read or edit config/ files for LLM/provider configuration.\n" +
		"Your environment carries valid MCP_API_URL + MCP_API_TOKEN — the bridge IS configured and ready. DO NOT report 'no MCP server configuration' or 'no API tokens available'. If a built-in fails, pick the corresponding api-bridge/custom-tool route above and proceed. Only stop if the bridge route also fails and explain the specific failure."
}
