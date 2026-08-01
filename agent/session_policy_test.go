package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestTurnToolPolicyControlsRequestTimeManifestWithoutMutatingDefinition(t *testing.T) {
	agent := &Agent{
		systemPrompt:         "inspect the workflow",
		UseCodeExecutionMode: true,
		customTools:          make(map[string]CustomTool),
		toolToServer:         make(map[string]string),
		toolFilter:           NewToolFilter(nil, nil, nil, nil, nil),
	}
	registry := newCanonicalToolRegistry()
	for _, name := range []string{"allowed_tool", "blocked_tool"} {
		definition := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}}
		if err := registry.register(registeredTool{
			Name:         name,
			Definition:   definition,
			Kind:         toolImplementationDirect,
			Source:       "direct",
			DisplayGroup: "workflow",
		}); err != nil {
			t.Fatal(err)
		}
	}
	agent.toolRegistry = registry

	policy, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"allowed_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, policy)
	prompt := agent.outgoingSystemPromptForContext(ctx)
	if !strings.Contains(prompt, "allowed_tool") {
		t.Fatalf("authorized tool missing from prompt: %s", prompt)
	}
	if strings.Contains(prompt, "blocked_tool") {
		t.Fatalf("blocked tool leaked into prompt: %s", prompt)
	}
	if _, ok := agent.toolRegistry.lookup("blocked_tool"); !ok {
		t.Fatal("runtime policy mutated the canonical definition")
	}
}

func TestCanonicalToolRegistryRejectsDifferentOwners(t *testing.T) {
	registry := newCanonicalToolRegistry()
	definition := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "query"}}
	if err := registry.register(registeredTool{Name: "query", Definition: definition, Kind: toolImplementationMCP, Source: "database"}); err != nil {
		t.Fatal(err)
	}
	err := registry.register(registeredTool{Name: "query", Definition: definition, Kind: toolImplementationDirect, Source: "direct"})
	if err == nil || !strings.Contains(err.Error(), `owned by mcp "database"`) {
		t.Fatalf("collision error = %v", err)
	}
}

func TestNormalizeToolPolicyRejectsWhitespace(t *testing.T) {
	_, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"query "}})
	if err == nil || !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("normalizeToolPolicy() error = %v", err)
	}
}
