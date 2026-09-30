package prompt

import (
	"strings"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// GetCodeExecutionInstructions returns the code execution mode instructions section.
// workspacePath is retained for compatibility. Product environment and file rules
// belong to caller instructions; current routing is composed by the Agent.
func GetCodeExecutionInstructions(workspacePath string) string {
	return "## Code execution\n\n{{TOOL_STRUCTURE}}\n\nUse this session's declared tools and runtime routing instructions. Discover exact tool contracts before invoking HTTP tools."
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
			"Group keys are display labels. Use the runtime routing instructions and get_api_spec for call contracts.\n"
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
	prompt = strings.ReplaceAll(prompt, VirtualToolsSectionPlaceholder, "")
	prompt = strings.ReplaceAll(prompt, LargeOutputHandlingPlaceholder, largeOutputHandlingSection)
	prompt = strings.ReplaceAll(prompt, CurrentDatePlaceholder, currentDate)
	prompt = strings.ReplaceAll(prompt, CurrentTimePlaceholder, currentTime)

	return prompt
}
