package prompt

import (
	"strings"
	"testing"
)

func TestCodeExecutionInstructionsDoNotAddressCustomGroupsAsMCPServers(t *testing.T) {
	instructions := GetCodeExecutionInstructions("")
	for _, want := range []string{
		"$MCP_CUSTOM/{tool_name}",
		"display-only groups, never MCP server names",
		"Only keys listed under `mcp_servers` are valid server path segments",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("missing routing contract %q:\n%s", want, instructions)
		}
	}
}

func TestCodeExecutionInstructionsEncodeQuotedArgumentsBeforeCurl(t *testing.T) {
	instructions := GetCodeExecutionInstructions("")
	for _, want := range []string{
		"do not inline it inside a single-quoted JSON literal",
		`jq -cn --arg sql "$sql"`,
		`json_extract(data, '$.field')`,
		`$MCP_CUSTOM/query_workflow_db`,
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("missing safe bridge-payload contract %q:\n%s", want, instructions)
		}
	}
}

func TestAvailableToolsSectionNamesTheTwoAddressSpaces(t *testing.T) {
	section := BuildAvailableToolsSection(`{"custom_tools":{"groups":{"workflow_db":{"tools":["query_workflow_db"]}}},"mcp_servers":{}}`)
	for _, want := range []string{
		"custom_tools.groups are display-only labels",
		"$MCP_CUSTOM/{tool}",
		"Only keys under mcp_servers use $MCP_MCP/{server}/{tool}",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("missing manifest explanation %q:\n%s", want, section)
		}
	}
}
