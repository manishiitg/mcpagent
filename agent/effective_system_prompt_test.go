package mcpagent

import (
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/agent/prompt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func promptManifestTool(name, category string) CustomTool {
	return CustomTool{
		Category: category,
		Definition: llmtypes.Tool{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        name,
				Description: name + " description",
			},
		},
	}
}

func codeExecutionPromptAgent() *Agent {
	return &Agent{
		UseCodeExecutionMode: true,
		customTools:          make(map[string]CustomTool),
		toolToServer:         make(map[string]string),
		toolFilter:           NewToolFilter(nil, nil, nil, nil, nil),
	}
}

func TestEffectiveSystemPromptReflectsToolsRegisteredAfterSet(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("operator policy")
	a.customTools["query_records"] = promptManifestTool("query_records", "database")

	got := a.instructions()
	if !strings.Contains(got, "query_records") {
		t.Fatalf("request-time prompt omitted a tool registered after SetInstructions:\n%s", got)
	}
	if a.systemPrompt != "operator policy" {
		t.Fatalf("rendering mutated the stored prompt: %q", a.systemPrompt)
	}
}

func TestEffectiveSystemPromptTracksAllowListChanges(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.customTools["query_records"] = promptManifestTool("query_records", "database")
	a.customTools["mutate_records"] = promptManifestTool("mutate_records", "database")
	a.setInstructions("operator policy")

	a.SetToolAccess([]string{"query_records"})
	first := a.instructions()
	if !strings.Contains(first, "query_records") || strings.Contains(first, "mutate_records") {
		t.Fatalf("first allow-list was not reflected:\n%s", first)
	}

	a.SetToolAccess([]string{"mutate_records"})
	second := a.instructions()
	if strings.Contains(second, "query_records") || !strings.Contains(second, "mutate_records") {
		t.Fatalf("changed allow-list was not reflected on the next read:\n%s", second)
	}
}

func TestEffectiveSystemPromptIsBalancedAndIdempotent(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.customTools["query_records"] = promptManifestTool("query_records", "database")
	a.setInstructions("before\n" + prompt.ToolStructurePlaceholder + "\nafter")

	first := a.instructions()
	second := a.instructions()
	if first != second {
		t.Fatalf("repeated reads changed the effective prompt")
	}
	if strings.Count(first, availableToolsOpenTag) != 1 || strings.Count(first, availableToolsCloseTag) != 1 {
		t.Fatalf("tool manifest tags are not balanced and singular:\n%s", first)
	}
	if strings.Contains(first, prompt.ToolStructurePlaceholder) {
		t.Fatalf("tool placeholder leaked into the effective prompt")
	}
	if !strings.HasPrefix(first, "before\n") || !strings.HasSuffix(first, "\nafter") {
		t.Fatalf("placeholder position was not preserved:\n%s", first)
	}
}

func TestEnsureSystemPromptUsesCurrentAuthorizedManifest(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("operator policy")
	a.customTools["query_records"] = promptManifestTool("query_records", "database")
	a.customTools["mutate_records"] = promptManifestTool("mutate_records", "database")
	a.SetToolAccess([]string{"query_records"})

	messages := ensureSystemPrompt(a, nil)
	if len(messages) != 1 || len(messages[0].Parts) != 1 {
		t.Fatalf("unexpected system message shape: %#v", messages)
	}
	textPart, ok := messages[0].Parts[0].(llmtypes.TextContent)
	if !ok {
		t.Fatalf("system message part is %T, want TextContent", messages[0].Parts[0])
	}
	if !strings.Contains(textPart.Text, "query_records") || strings.Contains(textPart.Text, "mutate_records") {
		t.Fatalf("actual conversation system message has a stale/unauthorized manifest:\n%s", textPart.Text)
	}
}

func TestPreDiscoveredSpecsRespectAllowList(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.customTools["query_records"] = promptManifestTool("query_records", "database")
	a.customTools["mutate_records"] = promptManifestTool("mutate_records", "database")
	a.preDiscoveredTools = []string{"query_records", "mutate_records"}
	a.SetToolAccess([]string{"query_records"})
	a.setInstructions("operator policy")

	got := a.instructions()
	if strings.Contains(got, "mutate_records") {
		t.Fatalf("pre-discovered specs disclosed a denied tool:\n%s", got)
	}
	if !strings.Contains(got, "query_records") {
		t.Fatalf("allowed pre-discovered tool is missing:\n%s", got)
	}
}

func TestNonCodeExecutionPromptDoesNotGainManifest(t *testing.T) {
	a := &Agent{}
	a.setInstructions("before " + prompt.ToolStructurePlaceholder + " after")

	if got := a.instructions(); got != "before  after" {
		t.Fatalf("non-code prompt should only strip the placeholder, got %q", got)
	}
}
