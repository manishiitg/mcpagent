package prompt

import (
	"fmt"
	"strings"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/mark3labs/mcp-go/mcp"
)

// GetToolSearchInstructions returns the tool search mode instructions section
// This provides guidance on how to use the search_tools virtual tool to discover tools
func GetToolSearchInstructions() string {
	return `**Tool Discovery:**

You have access to a large catalog of tools, but they are not loaded by default. Use these to discover and load them:
- **show_all_tools**() — list all available tools across all servers
- **search_tools**(query="pattern") — find tools by name/description (supports regex, falls back to fuzzy search)
- **add_tool**(tool_names=["tool1", "tool2"], server="optional") — load tools for use
- **remove_tool**(tool_names=["tool1"]) — unload tools (optional, useful for long-running tasks)

Search or list first, add what you need, then use the tools. If the same tool exists on multiple servers, specify the server parameter in add_tool.`
}

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
1. See available servers and tools in the JSON block above. Call get_api_spec(server_name="...", tool_name="...") to get the full API spec for any tool
2. Use execute_shell_command to write and run code
3. MCP_API_URL and MCP_API_TOKEN env vars are pre-set — use them as-is

**Environment — what's pre-set for you:**
- ` + "`" + `$MCP_API_URL` + "`" + ` + ` + "`" + `$MCP_API_TOKEN` + "`" + ` — HTTP endpoint + bearer for invoking MCP tools (see example below).
- ` + "`" + `$STEP_OUTPUT_DIR` + "`" + ` — write all primary outputs here. The folder exists; do not mkdir.
- ` + "`" + `$STEP_EXECUTION_DIR` + "`" + ` — parent of STEP_OUTPUT_DIR. Use only when reaching a sibling step's folder and sys.argv wasn't used.
- ` + "`" + `$VAR_<NAME>` + "`" + ` — workflow config values (e.g. ` + "`" + `$VAR_PAN` + "`" + `, ` + "`" + `$VAR_SHEET_URL` + "`" + `). Reference always; never hardcode the value.
- ` + "`" + `$SECRET_<NAME>` + "`" + ` — credentials (e.g. ` + "`" + `$SECRET_API_KEY` + "`" + `). Never echo to stdout, never write to files.
- ` + "`" + `$VAR_GROUP_NAME` + "`" + ` — current group (may be empty string when no group is active). The only var where an empty/absent value is acceptable.
- Accessing missing vars must fail loudly. In bash use ` + "`" + `"${VAR_PAN:?missing}"` + "`" + ` or ` + "`" + `set -u` + "`" + `; in python use ` + "`" + `os.environ['VAR_PAN']` + "`" + ` (not ` + "`" + `.get()` + "`" + ` with a default).

**Example — calling an MCP tool:**
MCP tools are reachable at ` + "`" + `$MCP_API_URL/tools/mcp/{server}/{tool}` + "`" + ` via authenticated HTTP POST. Any shell tool works — curl, jq, node, python, whatever fits the task. Code execution is shell-first; Python is optional.
` + "```" + `bash
curl -sS -X POST "$MCP_API_URL/tools/mcp/{server_name}/{tool_name}" \
  -H "Authorization: Bearer $MCP_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"arg1":"value1"}' | jq
# Response envelope: {"success": true|false, "result": ..., "error": "..."}
` + "```" + `
If you need retries, backoff, or structured logging, write a small helper in the language of your choice. For reusable helpers saved to main.py, see the main.py authoring rules below (when in learn-code mode).`
}

// BuildSystemPromptWithoutTools builds the system prompt without including tool descriptions
// This is useful when tools are passed via llmtypes.WithTools() to avoid prompt length issues
// toolStructureJSON is optional - if provided in code execution mode, it will replace {{TOOL_STRUCTURE}} placeholder
// preDiscoveredToolSpecs is optional - pre-generated compact specs for pre-discovered tools (inline in prompt)
// useToolSearchMode enables tool search mode instructions when true
// toolCategories is optional list of tool categories for tool search mode
func BuildSystemPromptWithoutTools(prompts map[string][]mcp.Prompt, resources map[string][]mcp.Resource, mode interface{}, discoverResource bool, discoverPrompt bool, useCodeExecutionMode bool, toolStructureJSON string, preDiscoveredToolSpecs string, useToolSearchMode bool, toolCategories []string, logger loggerv2.Logger, enableParallelToolExecution bool) string {
	// Build prompts section with previews (only if discoverPrompt is true and NOT in code execution mode)
	// In code execution mode, prompts/resources are not accessible via get_prompt/get_resource
	var promptsSection string
	if discoverPrompt && !useCodeExecutionMode {
		promptsSection = buildPromptsSectionWithPreviews(prompts, logger)
	} else {
		promptsSection = "" // Empty prompts section when discovery is disabled or in code execution mode
	}

	// Build resources section (only if discoverResource is true and NOT in code execution mode)
	// In code execution mode, resources are not accessible via get_resource
	var resourcesSection string
	if discoverResource && !useCodeExecutionMode {
		resourcesSection = buildResourcesSection(resources)
	} else {
		resourcesSection = "" // Empty resources section when discovery is disabled or in code execution mode
	}

	// Build virtual tools section (only mention tools that are actually available)
	virtualToolsSection := buildVirtualToolsSection(useCodeExecutionMode, useToolSearchMode, prompts, resources)

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
	} else if useToolSearchMode {
		corePrinciplesSection = `<core_principles>
**Your Goal:** Complete the user's request using discovered tools.

**Operating Rules:**
1. **Search First:** Use search_tools to find relevant tools before attempting to use them.
2. **Be Proactive:** Once tools are discovered, use them without asking for permission.
3. **Chain Actions:** If a tool output leads to a next step, take it immediately.
4. **Search Again:** If you need additional capabilities, search for more tools.
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

		// Replace {{TOOL_STRUCTURE}} placeholder with the tool index
		if toolStructureJSON != "" {
			var getApiSpecNote string
			if preDiscoveredToolSpecs != "" {
				getApiSpecNote = "Pre-loaded tool specs are provided below. Use get_api_spec only for tools NOT listed in the pre-loaded specs.\n"
			} else {
				getApiSpecNote = "Call get_api_spec(server_name=\"...\", tool_name=\"...\") to get the full API spec for specific tools.\n"
			}
			toolStructureSection := "\n\n<available_tools>\n" +
				"**AVAILABLE SERVERS AND TOOLS:**\n\n" +
				"The following MCP servers and their tools are accessible via HTTP API.\n" +
				getApiSpecNote + "\n" +
				"```json\n" +
				toolStructureJSON + "\n" +
				"```\n\n" +
				"Domain tools (MCP and custom) are called via HTTP API. System tools (execute_shell_command, agent_browser) are called directly — see your provider's tool list for exact names.\n" +
				"Do not conclude a workspace tool is unavailable just because it is absent from your provider's native tool list. First inspect this tool index or call get_api_spec.\n" +
				"Workspace media, search, and analysis tools live under server_name=\"workspace_advanced\". Examples include generate_music, text_to_speech, speech_to_text, generate_video, search_web_llm, image_gen, image_edit, read_image, and read_video.\n" +
				"For those tools, call get_api_spec(server_name=\"workspace_advanced\", tool_name=\"<tool>\") before reporting that the capability is unavailable.\n" +
				"</available_tools>\n" +
				preDiscoveredToolSpecs
			codeExecutionInstructions = strings.ReplaceAll(codeExecutionInstructions, ToolStructurePlaceholder, toolStructureSection)
		} else {
			toolStructureSection := "\n\n<available_tools>\n" +
				"**AVAILABLE SERVERS AND TOOLS:**\n\n" +
				"Tool index is being built. Use get_api_spec(server_name=\"...\", tool_name=\"...\") to discover endpoints.\n" +
				"Workspace media, search, and analysis tools are normally discoverable under server_name=\"workspace_advanced\"; check there before reporting that a capability such as generate_music is unavailable.\n" +
				"</available_tools>\n"
			codeExecutionInstructions = strings.ReplaceAll(codeExecutionInstructions, ToolStructurePlaceholder, toolStructureSection)
		}

		toolUsageSection = `<code_usage>
` + codeExecutionInstructions + `
</code_usage>`
	} else if useToolSearchMode {
		// Get tool search instructions
		toolSearchInstructions := GetToolSearchInstructions()

		toolUsageSection = `<tool_search>
` + toolSearchInstructions + `
</tool_search>`
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
- Use virtual tools for detailed prompts/resources when relevant
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
Large tool outputs (>1000 chars) are automatically offloaded to filesystem (offload context pattern). Use virtual tools to access them:
- 'read_large_output': Read specific characters from offloaded files
- 'search_large_output': Search for patterns in offloaded files  
- 'query_large_output': Execute jq queries on offloaded JSON files`
	}

	// Always use Simple system prompt template
	prompt := SystemPromptTemplate

	// Replace all placeholders
	prompt = strings.ReplaceAll(prompt, CorePrinciplesPlaceholder, corePrinciplesSection)
	prompt = strings.ReplaceAll(prompt, ToolUsagePlaceholder, toolUsageSection)
	prompt = strings.ReplaceAll(prompt, PromptsSectionPlaceholder, promptsSection)
	prompt = strings.ReplaceAll(prompt, ResourcesSectionPlaceholder, resourcesSection)
	prompt = strings.ReplaceAll(prompt, VirtualToolsSectionPlaceholder, virtualToolsSection)
	prompt = strings.ReplaceAll(prompt, LargeOutputHandlingPlaceholder, largeOutputHandlingSection)
	prompt = strings.ReplaceAll(prompt, CurrentDatePlaceholder, currentDate)
	prompt = strings.ReplaceAll(prompt, CurrentTimePlaceholder, currentTime)

	return prompt
}

// buildPromptsSectionWithPreviews builds the prompts section with previews
func buildPromptsSectionWithPreviews(prompts map[string][]mcp.Prompt, logger loggerv2.Logger) string {

	// Count total prompts across all servers
	totalPrompts := 0
	for _, serverPrompts := range prompts {
		totalPrompts += len(serverPrompts)
	}

	if totalPrompts == 0 {
		logger.Debug("No prompts found for preview generation - skipping prompts section")
		return ""
	}

	logger.Debug("Building prompts section with previews",
		loggerv2.Int("server_count", len(prompts)),
		loggerv2.Int("total_prompts", totalPrompts))

	var promptsList []string
	for serverName, serverPrompts := range prompts {
		if len(serverPrompts) == 0 {
			// Skip servers with no prompts
			continue
		}

		logger.Debug("Processing server prompts",
			loggerv2.String("server_name", serverName),
			loggerv2.Int("prompt_count", len(serverPrompts)))

		promptsList = append(promptsList, fmt.Sprintf("%s:", serverName))
		for _, prompt_item := range serverPrompts {
			name := prompt_item.Name
			description := prompt_item.Description

			logger.Debug("Processing prompt",
				loggerv2.String("server_name", serverName),
				loggerv2.String("prompt_name", name),
				loggerv2.Int("description_length", len(description)))

			// Extract preview (first 10 lines) from the description
			preview := extractPromptPreview(description)

			// Format as preview with name and first few lines
			promptsList = append(promptsList, fmt.Sprintf("  - %s: %s", name, preview))
		}
	}

	// Double-check: if no prompts were actually added, return empty
	if len(promptsList) == 0 {
		logger.Debug("No actual prompts found after processing - skipping prompts section")
		return ""
	}

	promptsText := strings.Join(promptsList, "\n")
	logger.Debug("Prompts section built",
		loggerv2.Int("total_length", len(promptsText)),
		loggerv2.Int("prompt_lines", len(promptsList)))

	return strings.ReplaceAll(PromptsSectionTemplate, PromptsListPlaceholder, promptsText)
}

// extractPromptPreview extracts the first 10 lines from prompt content
func extractPromptPreview(description string) string {
	// If description contains "Content:", extract the content part (legacy format)
	if strings.Contains(description, "\n\nContent:\n") {
		parts := strings.Split(description, "\n\nContent:\n")
		if len(parts) > 1 {
			content := parts[1]

			// Split into lines and take first 10 lines
			lines := strings.Split(content, "\n")
			previewLines := lines
			if len(lines) > 10 {
				previewLines = lines[:10]
			}

			preview := strings.Join(previewLines, "\n")
			if len(lines) > 10 {
				preview += "\n... (use 'get_prompt' tool for full content)"
			}

			return preview
		}
	}

	// If description contains full content (new format), extract preview
	if len(description) > 100 && !strings.Contains(description, "Prompt loaded from") {
		// Split into lines and take first 10 lines
		lines := strings.Split(description, "\n")
		previewLines := lines
		if len(lines) > 10 {
			previewLines = lines[:10]
		}

		preview := strings.Join(previewLines, "\n")
		if len(lines) > 10 {
			preview += "\n... (use 'get_prompt' tool for full content)"
		}

		return preview
	}

	// If no content section or short description, return the description as is
	return description
}

// buildResourcesSection builds the resources section
func buildResourcesSection(resources map[string][]mcp.Resource) string {
	if len(resources) == 0 {
		return ""
	}

	var resourcesList []string
	for serverName, serverResources := range resources {
		resourcesList = append(resourcesList, fmt.Sprintf("%s:", serverName))
		for _, resource := range serverResources {
			name := resource.Name
			uri := resource.URI
			description := resource.Description
			resourcesList = append(resourcesList, fmt.Sprintf("  - %s (%s): %s", name, uri, description))
		}
	}

	resourcesText := strings.Join(resourcesList, "\n")
	return strings.ReplaceAll(ResourcesSectionTemplate, ResourcesListPlaceholder, resourcesText)
}

// buildVirtualToolsSection builds the virtual tools section
// Only mentions tools that are actually available (prompts/resources must exist)
func buildVirtualToolsSection(useCodeExecutionMode bool, useToolSearchMode bool, prompts map[string][]mcp.Prompt, resources map[string][]mcp.Resource) string {
	if useCodeExecutionMode {
		return `AVAILABLE FUNCTIONS:

- **get_api_spec** - Get the full OpenAPI spec for specific tool(s). Skip this for tools whose specs are already pre-loaded in the system prompt.
  Usage: get_api_spec(server_name="<server>", tool_name="<tool>")
  Multiple tools: get_api_spec(server_name="<server>", tool_name=["<tool1>", "<tool2>"])`
	}

	if useToolSearchMode {
		// Tool search mode: Show search_tools as the primary discovery mechanism
		return `🔧 TOOL DISCOVERY:

- **search_tools** - Search for available tools by name or description
  Usage: search_tools(query="regex_pattern")
  Returns matching tools with server names (you must use add_tool to load them)
  Supports regex patterns and fuzzy matching

- **add_tool** - Add one or more tools to your toolkit
  Usage: add_tool(tool_names=["name1", "name2"])
  Optional: add_tool(tool_names=["name1"], server="server_name") to pick a specific server when duplicates exist

- **remove_tool** - Remove tools you no longer need from your active toolkit
  Usage: remove_tool(tool_names=["name1", "name2"])

Once you discover tools using search_tools, add them using add_tool, then they will be available for you to call directly.
When you finish a phase of work, use remove_tool to unload tools you no longer need — this keeps your toolkit focused and reduces noise.`
	}

	// Check if prompts actually exist
	hasPrompts := false
	if prompts != nil {
		totalPrompts := 0
		for _, serverPrompts := range prompts {
			totalPrompts += len(serverPrompts)
		}
		hasPrompts = totalPrompts > 0
	}

	// Check if resources actually exist
	hasResources := false
	if resources != nil {
		totalResources := 0
		for _, serverResources := range resources {
			totalResources += len(serverResources)
		}
		hasResources = totalResources > 0
	}

	// Build virtual tools list based on what's actually available
	var toolsList []string
	if hasPrompts {
		toolsList = append(toolsList, "- **get_prompt**: Fetch full prompt content (server + name) from an mcp server")
	}
	if hasResources {
		toolsList = append(toolsList, "- **get_resource**: Fetch resource content (server + uri) from an mcp server")
	}

	// If no tools are available, return empty string (section will be empty)
	if len(toolsList) == 0 {
		return ""
	}

	// Build the section with only available tools
	toolsText := strings.Join(toolsList, "\n")
	return `🔧 VIRTUAL TOOLS:

` + toolsText + `

These are internal tools - just specify server and identifier.`
}
