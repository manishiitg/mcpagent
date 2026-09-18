package prompt

import (
	"strings"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// GetCodeExecutionInstructions returns the code execution mode instructions section.
// workspacePath: the actual workspace path to substitute in examples.
// If workspacePath is empty (chat mode), workspace-related instructions are excluded.
func GetCodeExecutionInstructions(workspacePath string) string {
	return `**CODE EXECUTION MODE — Access MCP Tools via HTTP API:**

{{TOOL_STRUCTURE}}

**Filesystem Access:**
- Do NOT use provider-native or built-in filesystem/shell tools (for example: Bash, Read, Write, read_file, write_file, list_directory, grep_search, glob, read_many_files, replace, run_shell_command)
- For filesystem access, use only the tools declared in this session
- In code execution mode, prefer execute_shell_command for file reads/writes/commands, and use other declared workspace tools only when they are explicitly available

**Workflow:**
1. See available servers and tools in the JSON block above. Call get_api_spec(tool_name="...") to get the full API spec for any tool. Use server_name only to disambiguate a real MCP-server collision
2. Use execute_shell_command to write and run code
3. MCP_API_URL, MCP_API_TOKEN, MCP_AUTH, MCP_MCP, MCP_CUSTOM, and MCP_VIRTUAL env vars are pre-set — use them as-is

**Environment — what's pre-set for you:**
- ` + "`" + `$MCP_MCP` + "`" + `, ` + "`" + `$MCP_CUSTOM` + "`" + `, ` + "`" + `$MCP_VIRTUAL` + "`" + ` — short endpoint bases for MCP, custom, and virtual tools.
- ` + "`" + `$MCP_AUTH` + "`" + ` — Authorization header value (` + "`" + `Authorization: Bearer ...` + "`" + `). Use with ` + "`" + `-H "$MCP_AUTH"` + "`" + `.
- ` + "`" + `$MCP_API_URL` + "`" + ` + ` + "`" + `$MCP_API_TOKEN` + "`" + ` — full bridge endpoint + token fallback if you need custom HTTP code.
- ` + "`" + `$STEP_OUTPUT_DIR` + "`" + ` — write all primary outputs here. The folder exists; do not mkdir.
- ` + "`" + `$STEP_EXECUTION_DIR` + "`" + ` — parent of STEP_OUTPUT_DIR. Use only when reaching a sibling step's folder and sys.argv wasn't used.
- ` + "`" + `$VAR_<NAME>` + "`" + ` — workflow config values (e.g. ` + "`" + `$VAR_PAN` + "`" + `, ` + "`" + `$VAR_SHEET_URL` + "`" + `). Reference always; never hardcode the value.
- ` + "`" + `$SECRET_<NAME>` + "`" + ` — credentials (e.g. ` + "`" + `$SECRET_API_KEY` + "`" + `). Never echo to stdout, never write to files.
- ` + "`" + `$VAR_GROUP_NAME` + "`" + ` — current group (may be empty string when no group is active). The only var where an empty/absent value is acceptable.
- Accessing missing vars must fail loudly. In bash use ` + "`" + `"${VAR_PAN:?missing}"` + "`" + ` or ` + "`" + `set -u` + "`" + `; in python use ` + "`" + `os.environ['VAR_PAN']` + "`" + ` (not ` + "`" + `.get()` + "`" + ` with a default).

**Calling a custom tool (workflow, database, human-input, and other app tools):**
Custom tools are reachable at ` + "`" + `$MCP_CUSTOM/{tool}` + "`" + `. The labels under ` + "`" + `custom_tools.groups` + "`" + ` are display-only groups, never MCP server names and never URL path segments.
` + "```" + `bash
payload='{"arg1":"value1"}'
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/{tool_name}"
# Response envelope: {"success": true|false, "result": ..., "error": "..."}
` + "```" + `

When an argument contains quotes, newlines, SQL, JSON paths, or other shell
punctuation, **do not inline it inside a single-quoted JSON literal**. Shell
single quotes do not nest: a command such as ` + "`" + `payload='{"sql":"SELECT
json_extract(data, '$.field')"}'` + "`" + ` silently removes the quotes around
` + "`" + `$.field` + "`" + ` before the request reaches the tool. Keep the value in its own
shell variable and let ` + "`" + `jq` + "`" + ` encode the JSON instead:
` + "```" + `bash
sql="SELECT json_extract(data, '$.field') FROM events"
payload="$(jq -cn --arg sql "$sql" '{sql:$sql}')"
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/query_workflow_db"
` + "```" + `
Use the same ` + "`" + `jq -n --arg` + "`" + ` pattern for any custom or MCP tool argument
whose contents are not a fixed simple literal.

**Calling a real MCP-server tool:**
Only keys listed under ` + "`" + `mcp_servers` + "`" + ` are valid server path segments. Their tools are reachable at ` + "`" + `$MCP_MCP/{server}/{tool}` + "`" + `.
` + "```" + `bash
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_MCP/{server_name}/{tool_name}"
` + "```" + `
` + "`$MCP_AUTH`" + ` is already the complete ` + "`Authorization: Bearer ...`" + ` header. Never prepend another header or Bearer prefix. ` + "`--json`" + ` already selects POST and Content-Type, so do not add ` + "`-X POST`" + `, another Content-Type header, or ` + "`--data`" + `. Keep the call unpiped so curl's nonzero HTTP-failure status reaches ` + "`execute_shell_command`" + `.
If you need retries, backoff, or structured logging, write a small helper in the language of your choice. For reusable helpers saved to main.py, see the main.py authoring rules below (when in learn-code mode).`
}

// BuildAvailableToolsSection renders the one replaceable, agent-facing tool
// manifest used by both the default prompt builder and request-time composition.
func BuildAvailableToolsSection(toolStructureJSON string) string {
	var inventory string
	if toolStructureJSON == "" {
		inventory = "Tool inventory is unavailable. Do not guess tool names; report that discovery is unavailable.\n"
	} else {
		inventory = "The following custom tools and real MCP servers are accessible via HTTP API.\n" +
			"Call get_api_spec(tool_name=\"...\") to get the full API spec for specific tools.\n\n" +
			"```json\n" + toolStructureJSON + "\n```\n\n" +
			"Keys under custom_tools.groups are display-only labels and use $MCP_CUSTOM/{tool}; they are not MCP servers. Only keys under mcp_servers use $MCP_MCP/{server}/{tool}. System tools (execute_shell_command, agent_browser) are called directly — see your provider's tool list for exact names.\n"
	}

	return "<available_tools>\n" +
		"**AVAILABLE SERVERS AND TOOLS:**\n\n" +
		inventory +
		"</available_tools>"
}

// BuildSystemPromptWithoutTools builds the system prompt without including tool descriptions
// This is useful when tools are passed via llmtypes.WithTools() to avoid prompt length issues
// toolStructureJSON is optional - if provided in code execution mode, it will replace {{TOOL_STRUCTURE}} placeholder
func BuildSystemPromptWithoutTools(mode interface{}, useCodeExecutionMode bool, toolStructureJSON string, logger loggerv2.Logger, enableParallelToolExecution bool) string {
	virtualToolsSection := buildVirtualToolsSection(useCodeExecutionMode)

	// Get current date and time
	now := time.Now()
	currentDate := now.Format("2006-01-02")
	currentTime := now.Format("15:04:05")

	// Build core principles section based on mode
	var corePrinciplesSection string
	autonomousNote := `
**Finish what you start this turn:** Do not stop mid-action — complete all tool calls you have initiated before generating a text response. If you delegated work, ending your turn IS the completion of your action for this turn.`

	if useCodeExecutionMode {
		corePrinciplesSection = `<core_principles>
**Your Goal:** Complete the user's request.

**Operating Rules:**
1. **Be Proactive:** Do not ask for permission to use tools. Just use them.
2. **Chain Actions:** If a tool output leads to a next step, take it immediately.
3. **Solve Fully:** Strive to reach the final answer or state before returning control.
` + autonomousNote + `
</core_principles>`
	} else {
		corePrinciplesSection = `<core_principles>
**Your Goal:** Complete the user's request.

**Operating Rules:**
1. **Be Proactive:** Do not ask for permission to use tools. Just use them.
2. **Chain Actions:** If a tool output leads to a next step, take it immediately. Do not stop to report intermediate progress unless asked.
3. **Solve Fully:** Strive to reach the final answer or state before returning control.
` + autonomousNote + `
</core_principles>`
	}

	// Build tool usage section based on mode
	var toolUsageSection string
	if useCodeExecutionMode {
		codeExecutionInstructions := GetCodeExecutionInstructions("")

		toolStructureSection := BuildAvailableToolsSection(toolStructureJSON)
		codeExecutionInstructions = strings.ReplaceAll(codeExecutionInstructions, ToolStructurePlaceholder, toolStructureSection)

		toolUsageSection = `<code_usage>
` + codeExecutionInstructions + `
</code_usage>`
	} else {
		var parallelToolHint string
		if enableParallelToolExecution {
			parallelToolHint = `

**Parallel Execution:**
- You can call multiple tools in a single response — they will execute concurrently
- Use this to speed up independent operations (e.g., reading multiple files, querying multiple APIs)
- Only parallelize independent calls — if one tool's output is needed as input for another, call them sequentially`
		}
		toolUsageSection = `<tool_usage>
**Guidelines:**
- Use tools when they can help answer the question
- Use tool discovery for detailed API contracts when relevant
- Provide clear responses based on tool results` + parallelToolHint + `

**Best Practices:**
- Use virtual tools to access detailed knowledge when relevant
- **If a tool call fails, retry with different arguments or parameters**
- **Try alternative approaches when tools return errors or unexpected results**
- **Modify search terms, file paths, or query parameters to overcome failures**
</tool_usage>`
	}

	// Build context offloading section (only for simple mode)
	var largeOutputHandlingSection string
	if useCodeExecutionMode {
		largeOutputHandlingSection = "" // Not available in code execution mode
	} else {
		largeOutputHandlingSection = `
CONTEXT OFFLOADING:
Large tool outputs (>1000 chars) are automatically offloaded to filesystem (offload context pattern).
Use 'search_large_output' with operation='read', operation='search', or operation='query' to access them.`
	}

	// Always use Simple system prompt template
	prompt := SystemPromptTemplate

	// Replace all placeholders
	prompt = strings.ReplaceAll(prompt, CorePrinciplesPlaceholder, corePrinciplesSection)
	prompt = strings.ReplaceAll(prompt, ToolUsagePlaceholder, toolUsageSection)
	prompt = strings.ReplaceAll(prompt, VirtualToolsSectionPlaceholder, virtualToolsSection)
	prompt = strings.ReplaceAll(prompt, LargeOutputHandlingPlaceholder, largeOutputHandlingSection)
	prompt = strings.ReplaceAll(prompt, CurrentDatePlaceholder, currentDate)
	prompt = strings.ReplaceAll(prompt, CurrentTimePlaceholder, currentTime)

	return prompt
}

// buildVirtualToolsSection builds the virtual tools section
func buildVirtualToolsSection(useCodeExecutionMode bool) string {
	if useCodeExecutionMode {
		return `AVAILABLE FUNCTIONS:

- **get_api_spec** - Get the full OpenAPI spec for specific tool(s).
  Usage: get_api_spec(tool_name="<tool>")
  Multiple tools: get_api_spec(tool_name=["<tool1>", "<tool2>"])
  Optional disambiguation for a real MCP-server collision: server_name="<server>"`
	}

	return ""
}
