package prompt

import (
	"fmt"
	"strings"
	"time"

	loggerv2 "mcpagent/logger/v2"

	"github.com/mark3labs/mcp-go/mcp"
)

// GetCodeExecutionInstructions returns the code execution mode instructions section
// This can be reused by agents that need to include code execution guidance in their prompts
func GetCodeExecutionInstructions() string {
	return `**CODE EXECUTION MODE - Access MCP Servers via Go Code:**

{{TOOL_STRUCTURE}}

**📋 Workflow:**
1. Use discover_code_files to get exact function signatures before writing code
2. Write Go code using write_code that calls generated tool functions
3. Execute and get results
4. Recognize completion: If output shows completion, move to next step or provide final answer

**⚠️ CRITICAL Requirements:**
- ✅ **MUST have package main declaration**
- ✅ **Use fmt.Println()/fmt.Printf() to output results**
- ✅ **Import generated packages** (e.g., import "workspace_tools", import "aws") - go.work is set up automatically
- ✅ **ALWAYS use discover_code_files FIRST** to see exact function signatures and parameter names
- ❌ **NEVER use Go standard file I/O** (os.WriteFile, os.ReadFile, etc.) - files go to wrong directory
- ✅ **ALWAYS use workspace_tools package** for file operations (ReadWorkspaceFile, UpdateWorkspaceFile)
- ✅ **CLI parameters**: Use optional 'args' parameter in write_code (accessible via os.Args[1], os.Args[2], etc.)

**⚠️ Error Handling Pattern:**
Functions return only string (no error). Follow this pattern for EVERY tool call:
1. Call: output := toolName(params)
2. Print: fmt.Printf("Tool output: %%s\n", output)
3. Check: Examine output string for error indicators (e.g., strings.HasPrefix(output, "Error:"))
4. Use: Process result if successful

- **API Errors** (network, HTTP): Functions panic - exceptional cases
- **Tool Errors**: Returned in result string - examine output to detect errors
- **✅ ALWAYS print output BEFORE checking errors** - helps discover error patterns

**🔧 Best Practices:**
- **Debugging**: Use fmt.Printf() liberally to trace execution and print variable values
- **Complex problems**: Break down into steps, test each tool individually, build incrementally
- **Multiple tools**: Test tools separately first, then combine once you understand their response patterns
- **Error recovery**: Use discover_code_files to verify parameter names, check imports/types for build errors

**Example - Tool Call with Error Handling:**
  package main
  import ("fmt"; "strings"; "workspace_tools")
  
  func main() {
      // Use discover_code_files to see exact struct definition first!
      params := workspace_tools.ReadWorkspaceFileParams{
          Filepath: "data/file.txt",
      }
      output := workspace_tools.ReadWorkspaceFile(params)
      fmt.Printf("Tool output: %%s\n", output)
      if strings.HasPrefix(output, "Error:") {
          fmt.Printf("❌ Error: %%s\n", output)
          return
      }
      fmt.Printf("✅ Success!\n")
  }

**Example - File Operations (CRITICAL):**
  package main
  import ("fmt"; "strings"; "workspace_tools")
  
  func main() {
      // ✅ CORRECT: Use workspace_tools for file operations
      result := workspace_tools.UpdateWorkspaceFile(workspace_tools.UpdateWorkspaceFileParams{
          Filepath: "data/results.json",
          Content:  "{\"status\": \"success\"}",
      })
      fmt.Printf("Tool output: %%s\n", result)
      if strings.HasPrefix(result, "Error:") {
          fmt.Printf("❌ Error: %%s\n", result)
          return
      }
      
      // ❌ WRONG: NEVER use os.WriteFile, os.ReadFile, etc. - files go to wrong directory!
  }

**🚨 Common Mistakes:**
- ❌ Checking err != nil (functions return string, no error)
- ❌ Not printing output before checking errors
- ❌ Using wrong parameter names - always use discover_code_files first
- ❌ Using standard Go file I/O - must use workspace_tools package
- ❌ Writing placeholder code - always implement actual logic
- ❌ Looping on completion messages - recognize completion and move on`
}

// BuildSystemPromptWithoutTools builds the system prompt without including tool descriptions
// This is useful when tools are passed via llmtypes.WithTools() to avoid prompt length issues
// toolStructureJSON is optional - if provided in code execution mode, it will replace {{TOOL_STRUCTURE}} placeholder
func BuildSystemPromptWithoutTools(prompts map[string][]mcp.Prompt, resources map[string][]mcp.Resource, mode interface{}, discoverResource bool, discoverPrompt bool, useCodeExecutionMode bool, toolStructureJSON string, logger loggerv2.Logger) string {
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
	virtualToolsSection := buildVirtualToolsSection(useCodeExecutionMode, prompts, resources)

	// Get current date and time
	now := time.Now()
	currentDate := now.Format("2006-01-02")
	currentTime := now.Format("15:04:05")

	// Build core principles section based on mode
	var corePrinciplesSection string
	if useCodeExecutionMode {
		corePrinciplesSection = `<core_principles>
When answering questions:
1. **Think** about what information/actions are needed
2. **Write code** to gather information and perform actions
3. **Provide helpful responses** based on execution results
</core_principles>`
	} else {
		corePrinciplesSection = `<core_principles>
**Your Goal:** Complete the user's request autonomously.

**Operating Rules:**
1. **Be Proactive:** Do not ask for permission to use tools. Just use them.
2. **Chain Actions:** If a tool output leads to a next step, take it immediately. Do not stop to report intermediate progress unless asked.
3. **Solve Fully:** strive to reach the final answer or state before returning control to the user.
</core_principles>`
	}

	// Build tool usage section based on mode
	var toolUsageSection string
	if useCodeExecutionMode {
		// Get code execution instructions and replace {{TOOL_STRUCTURE}} placeholder
		codeExecutionInstructions := GetCodeExecutionInstructions()

		// Replace {{TOOL_STRUCTURE}} placeholder with actual tool structure
		if toolStructureJSON != "" {
			toolStructureSection := "\n\n<available_code>\n" +
				"**AVAILABLE CODE FILES AND FUNCTIONS:**\n\n" +
				"The following code files and functions are available for use in your Go code. This structure shows all servers, custom tools, and their functions:\n\n" +
				"```json\n" +
				toolStructureJSON + "\n" +
				"```\n\n" +
				"**How to use:**\n" +
				"- The JSON structure shows package names as keys (e.g., \"google_sheets_tools\", \"workspace_tools\")\n" +
				"- Each package contains a \"tools\" array with available function names (e.g., \"GetDocument\", \"ListSpreadsheets\")\n" +
				"- Use the package name as \"server_name\" in discover_code_files (e.g., discover_code_files(server_name=\"google_sheets_tools\", tool_names=[\"GetDocument\"]))\n" +
				"- Import the package and call the function in your Go code (e.g., import \"google_sheets_tools\")\n" +
				"- Use 'discover_code_files' tool to get exact function signatures before writing code\n" +
				"</available_code>\n"
			codeExecutionInstructions = strings.ReplaceAll(codeExecutionInstructions, ToolStructurePlaceholder, toolStructureSection)
		} else {
			// Provide helpful message when tool structure is not available
			toolStructureSection := "\n\n<available_code>\n" +
				"**AVAILABLE CODE FILES AND FUNCTIONS:**\n\n" +
				"Tool structure discovery is in progress or unavailable. Use the 'discover_code_files' tool to explore available code:\n\n" +
				"- **discover_code_files(server_name, tool_name)**: Get exact function signatures for any tool\n" +
				"- Example: discover_code_files(server_name=\"aws\", tool_name=\"GetDocument\")\n" +
				"- This will show you the exact Go function signature, parameters, and usage\n\n" +
				"**How to discover tools:**\n" +
				"1. Use discover_code_files to find available servers and tools\n" +
				"2. Get the exact function signature for the tool you want to use\n" +
				"3. Import the package (e.g., import \"aws\") and call the function in your Go code\n" +
				"</available_code>\n"
			codeExecutionInstructions = strings.ReplaceAll(codeExecutionInstructions, ToolStructurePlaceholder, toolStructureSection)
		}

		toolUsageSection = `<code_usage>
` + codeExecutionInstructions + `
</code_usage>`
	} else {
		toolUsageSection = `<tool_usage>
**Guidelines:**
- Use tools when they can help answer the question
- Execute tools one at a time, waiting for results
- Use virtual tools for detailed prompts/resources when relevant
- Provide clear responses based on tool results

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
func buildVirtualToolsSection(useCodeExecutionMode bool, prompts map[string][]mcp.Prompt, resources map[string][]mcp.Resource) string {
	if useCodeExecutionMode {
		// Code execution mode: Show simplified virtual tools section
		return `🔧 AVAILABLE FUNCTIONS:

- **discover_code_files** - Get Go source code for a specific function
  Usage: discover_code_files(server_name="aws", tool_name="GetDocument")

- **write_code** - Write and execute Go code
  Code runs as separate process via 'go run'
  Use fmt.Println() to output results
  Optional 'args' parameter: Array of strings passed as CLI arguments (accessible via os.Args[1], os.Args[2], etc.)`
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
