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
// Applied via AddInstructions for every coding-agent CLI we
// front (cursor / claude-code / codex / gemini)
// so the routing instruction is delivered the same way across
// providers.
func bridgeRoutingExplicitInstructions() string {
	return "IMPORTANT — bridge tool routing (use these EXACT names when your built-ins are denied):\n" +
		"  • api-bridge.execute_shell_command / api_bridge_execute_shell_command(command, timeout?) — shell (cat, ls, jq, python3, curl, any *nix command). USE INSTEAD OF: built-in Shell / Bash / run_command / view_file.\n" +
		"  • api-bridge.get_api_spec / api_bridge_get_api_spec(tool_name, server_name?) — fetch the OpenAPI spec by tool name. Omit server_name normally; use it only to disambiguate a real MCP-server collision. Then call the tool via execute_shell_command + curl / python3. For file edits, use execute_shell_command with the declared workspace paths.\n" +
		"  • In Pi CLI, use mcp({ search: \"tool words\" }), mcp({ describe: \"api_bridge_execute_shell_command\" }), then mcp({ tool: \"api_bridge_execute_shell_command\", args: \"{...json...}\" }) for the documented bridge tools when direct api_bridge_* names are not visible.\n" +
		"  • Custom tools can also be called through execute_shell_command + curl using $MCP_CUSTOM and $MCP_AUTH. For LLM/provider configuration, use $MCP_CUSTOM/list_published_llms, $MCP_CUSTOM/list_provider_models, $MCP_CUSTOM/test_llm, $MCP_CUSTOM/save_published_llm, and $MCP_CUSTOM/set_provider_auth. Do not read or edit config/ files for LLM/provider configuration.\n" +
		"  • Compact HTTP form (use this instead of verbose curl): curl --fail-with-body -sS --json '<payload>' -H \"$MCP_AUTH\" \"$MCP_CUSTOM/<tool>\" (or $MCP_MCP/<server>/<tool>). MCP_AUTH is already the complete `Authorization: Bearer ...` header; never prepend another header or Bearer prefix. --json already selects POST and Content-Type, so do not add -X POST, a Content-Type header, or --data. Do not pipe through jq unless you explicitly preserve curl's nonzero status.\n" +
		"  • BLOCKING HUMAN FEEDBACK: When human_feedback is available only through the custom HTTP route, call $MCP_CUSTOM/human_feedback with curl in the FOREGROUND and wait for that same curl call to return the user's answer. Never use nohup, append &, launch it through run_in_background, write its result to a temporary file, poll for completion, or ask the user to send another message after responding. Do not set execute_shell_command's timeout shorter than human_feedback.timeout_seconds; omitting the shell timeout is safe. The open curl request is the wait mechanism, and its returned body resumes your turn automatically. Cursor CLI abandons silent MCP calls at about 60 seconds, so when running in Cursor set human_feedback.timeout_seconds to at most 45 seconds; if it expires, explain that clearly and retry only if the input is still required.\n" +
		"Your environment carries valid MCP_API_URL + MCP_API_TOKEN — the bridge IS configured and ready. DO NOT report 'no MCP server configuration' or 'no API tokens available'. If a built-in fails, pick the corresponding api-bridge/custom-tool route above and proceed. Only stop if the bridge route also fails and explain the specific failure."
}
