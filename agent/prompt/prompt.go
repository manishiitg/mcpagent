package prompt

import "strings"

// SystemPromptTemplate is the complete system prompt template with placeholders
const SystemPromptTemplate = `<session_info>
**Date**: {{CURRENT_DATE}} | **Time**: {{CURRENT_TIME}}
</session_info>

{{CORE_PRINCIPLES}}

{{TOOL_USAGE}}

{{PROMPTS_SECTION}}

{{RESOURCES_SECTION}}

<virtual_tools>
{{VIRTUAL_TOOLS_SECTION}}
{{LARGE_OUTPUT_HANDLING}}
</virtual_tools>`

// PromptsSectionTemplate is the template for the prompts section with purpose instructions
const PromptsSectionTemplate = `
<prompts_section>
## 📚 KNOWLEDGE RESOURCES (PROMPTS)

These are prompts which mcp servers have which you get access to know how to use a mcp server better.

{{PROMPTS_LIST}}

**IMPORTANT**: Before using any MCP server, read its prompts using 'get_prompt' to understand how to use it effectively and avoid errors.
</prompts_section>`

// ResourcesSectionTemplate is the template for the resources section with purpose instructions
const ResourcesSectionTemplate = `
<resources_section>
## 📁 EXTERNAL RESOURCES

{{RESOURCES_LIST}}

Use 'get_resource' tool to access content when needed.
</resources_section>`

// VirtualToolsSectionTemplate is the template for virtual tool instructions
const VirtualToolsSectionTemplate = `
🔧 VIRTUAL TOOLS:

- **get_prompt**: Fetch full prompt content (server + name) from an mcp server
- **get_resource**: Fetch resource content (server + uri) from an mcp server

These are internal tools - just specify server and identifier.`

// Placeholder constants for easy replacement
const (
	ToolsPlaceholder               = "{{TOOLS}}"
	PromptsSectionPlaceholder      = "{{PROMPTS_SECTION}}"
	ResourcesSectionPlaceholder    = "{{RESOURCES_SECTION}}"
	VirtualToolsSectionPlaceholder = "{{VIRTUAL_TOOLS_SECTION}}"
	PromptsListPlaceholder         = "{{PROMPTS_LIST}}"
	ResourcesListPlaceholder       = "{{RESOURCES_LIST}}"
	CurrentDatePlaceholder         = "{{CURRENT_DATE}}"
	CurrentTimePlaceholder         = "{{CURRENT_TIME}}"
	ToolStructurePlaceholder       = "{{TOOL_STRUCTURE}}"
	CorePrinciplesPlaceholder      = "{{CORE_PRINCIPLES}}"
	ToolUsagePlaceholder           = "{{TOOL_USAGE}}"
	LargeOutputHandlingPlaceholder = "{{LARGE_OUTPUT_HANDLING}}"
)

// NormalizeForAppend tidies a system prompt before another block is appended
// to it: collapses the blank-line runs that concatenation tends to leave
// behind and trims surrounding whitespace.
//
// This replaced RemoveAIStaffEngineerText, which stripped a "# AI Staff
// Engineer" persona header that SystemPromptTemplate no longer emits. The
// system prompt now describes the product context and leaves the role to
// whatever the caller sets, so there is no persona to strip — but callers
// still relied on the trimming this did as a side effect.
func NormalizeForAppend(prompt string) string {
	prompt = strings.ReplaceAll(prompt, "\n\n\n", "\n\n")
	return strings.TrimSpace(prompt)
}
