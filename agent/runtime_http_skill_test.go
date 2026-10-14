package mcpagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeHTTPSkillLifecycleAndCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name            string
		code, discovery bool
	}{
		{"progressive", true, true}, {"inventory", true, false}, {"native-api", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
			a, err := NewAgentFromDefinition(context.Background(), AgentDefinition{Instructions: "product policy", Tools: ToolSet{Direct: []ToolDefinition{{Name: "read_fact", Description: "Read one fact.", InputSchema: map[string]interface{}{"type": "object"}, Execute: func(context.Context, map[string]interface{}) (string, error) { return "fact", nil }}}}}, RuntimeConfig{Model: linkedOutputFakeModel{}, MCPConfigPath: config, Tools: ToolRuntimeConfig{CodeExecution: tc.code, Discovery: tc.discovery}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			want := tc.code
			if a.hasRuntimeHTTPSkill() != want {
				t.Fatal("constructor failed to attach transport guidance without changing inventory/discovery mode")
			}
			if want {
				prompt := a.instructions()
				if !strings.Contains(prompt, "product policy") || !strings.Contains(prompt, runtimeHTTPSkillName) || strings.Contains(prompt, "--fail-with-body") {
					t.Fatal("prompt must preserve policy and route to the skill without inlining its curl tutorial")
				}
				if strings.Contains(prompt, "<available_tools>") == tc.discovery || !tc.discovery && !strings.Contains(prompt, "read_fact") {
					t.Fatal("attaching transport guidance changed the tool inventory")
				}
				var body string
				for _, skill := range a.attachedSkills {
					if skill.Name == runtimeHTTPSkillName {
						body = skill.Content
					}
				}
				for _, rule := range []string{"get_api_spec", "MCP_AUTH", "--fail-with-body", "nonzero HTTP-failure", "response envelope"} {
					if !strings.Contains(body, rule) {
						t.Fatalf("transport skill lost %s", rule)
					}
				}
				if strings.Contains(body, "search_tools") != tc.discovery {
					t.Fatal("skill recommends a discovery tool absent from this mode")
				}
				a.detachSkill(runtimeHTTPSkillName)
				if !strings.Contains(a.instructions(), "--fail-with-body") {
					t.Fatal("removing the skill must restore inline mechanics, not leave a dangling pointer")
				}
			}
		})
	}
}
