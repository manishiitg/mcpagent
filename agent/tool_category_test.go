package mcpagent

import (
	"context"
	"testing"
)

func TestCanonicalAppToolCategories(t *testing.T) {
	filter := NewToolFilter(nil, nil, nil, []string{"workspace", "human_tools", "delegation_tools"}, nil)
	for _, category := range []string{"human_tools", "delegation_tools"} {
		if !filter.IsSystemCategory(category) {
			t.Fatalf("%q must be a system category", category)
		}
	}
	if filter.IsSystemCategory("human") {
		t.Fatal("legacy ambiguous category human must not remain a system category")
	}
	if got := GetHumanToolCategory(); got != "human_tools" {
		t.Fatalf("GetHumanToolCategory() = %q, want human_tools", got)
	}
	for input, want := range map[string]string{
		"workspace_tools":  "workspace",
		"human_tools":      "human_tools",
		"delegation_tools": "delegation_tools",
	} {
		if got := filter.GetToolCategory(input); got != want {
			t.Fatalf("GetToolCategory(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAppControlToolsAreImmediatelyAvailableInToolSearchMode(t *testing.T) {
	for _, category := range []string{"human_tools", "delegation_tools"} {
		t.Run(category, func(t *testing.T) {
			agent := &Agent{UseToolSearchMode: true}
			name := category + "_test"
			err := agent.RegisterCustomTool(
				name,
				"test tool",
				map[string]interface{}{"type": "object"},
				func(context.Context, map[string]interface{}) (string, error) { return "ok", nil },
				category,
			)
			if err != nil {
				t.Fatalf("RegisterCustomTool() error = %v", err)
			}
			if _, ok := agent.discoveredTools[name]; !ok {
				t.Fatalf("%q tool was not immediately available", category)
			}
		})
	}
}

func TestLegacyHumanCategoryIsNotTreatedAsHumanTool(t *testing.T) {
	agent := &Agent{UseToolSearchMode: true}
	const name = "legacy_human_test"
	err := agent.RegisterCustomTool(
		name,
		"legacy test tool",
		map[string]interface{}{"type": "object"},
		func(context.Context, map[string]interface{}) (string, error) { return "ok", nil },
		"human",
	)
	if err != nil {
		t.Fatalf("RegisterCustomTool() error = %v", err)
	}
	if _, ok := agent.discoveredTools[name]; ok {
		t.Fatal("legacy ambiguous category human was treated as an immediately available human tool")
	}
}
