package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCloneAndValidateAgentDefinitionRejectsDuplicateIdentityEntries(t *testing.T) {
	tests := []struct {
		name       string
		definition AgentDefinition
		want       string
	}{
		{
			name: "direct tool",
			definition: AgentDefinition{Tools: ToolSet{Direct: []ToolDefinition{
				{Name: "query", Execute: definitionNoopTool},
				{Name: "query", Execute: definitionNoopTool},
			}}},
			want: `duplicate direct tool name "query"`,
		},
		{
			name: "MCP source",
			definition: AgentDefinition{Tools: ToolSet{MCP: []MCPToolSource{
				{Name: "workflow"},
				{Name: "workflow"},
			}}},
			want: `duplicate MCP tool source "workflow"`,
		},
		{
			name: "skill",
			definition: AgentDefinition{Skills: []*llmtypes.Skill{
				{Name: "workflow"},
				{Name: "workflow"},
			}},
			want: `duplicate skill name "workflow"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := cloneAndValidateAgentDefinition(test.definition)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("cloneAndValidateAgentDefinition() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCloneAndValidateAgentDefinitionOwnsMutableInputs(t *testing.T) {
	skill := &llmtypes.Skill{
		Name:            "workflow",
		Paths:           []string{"planning/**"},
		Metadata:        map[string]string{"owner": "builder"},
		SupportingFiles: []llmtypes.SkillFile{{RelPath: "reference.md", Content: []byte("original")}},
	}
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"ids": map[string]interface{}{"type": "array", "required": []string{"id"}},
		},
	}
	input := AgentDefinition{
		Instructions: "inspect",
		Skills:       []*llmtypes.Skill{skill},
		Tools: ToolSet{Direct: []ToolDefinition{{
			Name:        "query",
			InputSchema: schema,
			Execute:     definitionNoopTool,
		}}},
	}

	got, err := cloneAndValidateAgentDefinition(input)
	if err != nil {
		t.Fatal(err)
	}
	skill.Paths[0] = "changed"
	skill.Metadata["owner"] = "changed"
	skill.SupportingFiles[0].Content[0] = 'X'
	schema["type"] = "changed"
	schema["properties"].(map[string]interface{})["ids"].(map[string]interface{})["required"].([]string)[0] = "changed"

	if got.Skills[0].Paths[0] != "planning/**" || got.Skills[0].Metadata["owner"] != "builder" {
		t.Fatalf("skill clone changed with caller input: %#v", got.Skills[0])
	}
	if string(got.Skills[0].SupportingFiles[0].Content) != "original" {
		t.Fatalf("supporting file clone changed: %q", got.Skills[0].SupportingFiles[0].Content)
	}
	if got.Tools.Direct[0].InputSchema["type"] != "object" {
		t.Fatalf("schema clone changed: %#v", got.Tools.Direct[0].InputSchema)
	}
	required := got.Tools.Direct[0].InputSchema["properties"].(map[string]interface{})["ids"].(map[string]interface{})["required"].([]string)
	if required[0] != "id" {
		t.Fatalf("nested schema clone changed: %#v", required)
	}
}

func TestNewAgentFromDefinitionRejectsInvalidDefinitionBeforeRuntime(t *testing.T) {
	_, err := NewAgentFromDefinition(context.Background(), AgentDefinition{
		Tools: ToolSet{Direct: []ToolDefinition{{Name: "missing_executor"}}},
	}, RuntimeConfig{Model: definitionFakeModel{}})
	if err == nil || !strings.Contains(err.Error(), `direct tool "missing_executor" has no executor`) {
		t.Fatalf("NewAgentFromDefinition() error = %v", err)
	}
}

type definitionFakeModel struct{ llmtypes.Model }

func definitionNoopTool(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}
