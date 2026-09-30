package prompt

import (
	"strings"
	"testing"
)

func TestCodeExecutionTutorialDefersToRuntimeContract(t *testing.T) {
	text := GetCodeExecutionInstructions("")
	if !strings.Contains(text, ToolStructurePlaceholder) || !strings.Contains(text, "runtime routing instructions") {
		t.Fatal("missing inventory/runtime contract")
	}
	for _, duplicate := range []string{"curl", "MCP_AUTH", "STEP_OUTPUT_DIR", "VAR_GROUP_NAME", "query_workflow_db"} {
		if strings.Contains(text, duplicate) {
			t.Fatalf("generic tutorial repeated runtime/product instruction %q", duplicate)
		}
	}
}

func TestAvailableToolsSectionPreservesInventoryWithoutASecondTutorial(t *testing.T) {
	inventory := `{"custom_tools":{"groups":{"workflow_db":{"tools":["query_workflow_db"]}}},"mcp_servers":{}}`
	section := BuildAvailableToolsSection(inventory)
	if !strings.Contains(section, inventory) || !strings.Contains(section, "get_api_spec") {
		t.Fatal("lost legacy inventory discovery")
	}
	if strings.Contains(section, "MCP_AUTH") || strings.Contains(section, "agent_browser") {
		t.Fatal("manifest teaches a second transport contract or unadmitted tool")
	}
}
