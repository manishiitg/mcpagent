package mcpagent

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterCustomToolRejectsMCPNameCollision(t *testing.T) {
	agent := &Agent{
		customTools:  make(map[string]CustomTool),
		toolToServer: map[string]string{"query_records": "database"},
	}

	err := agent.RegisterCustomTool("query_records", "direct", objectSchema(), noopTool, "workflow")
	if err == nil || !strings.Contains(err.Error(), `already registered by MCP server "database"`) {
		t.Fatalf("RegisterCustomTool() error = %v, want MCP collision", err)
	}
	if len(agent.customTools) != 0 {
		t.Fatalf("collision mutated custom registry: %#v", agent.customTools)
	}
	if got := agent.toolToServer["query_records"]; got != "database" {
		t.Fatalf("collision replaced MCP owner with %q", got)
	}
}

func TestRegisterCustomToolRejectsCategoryChangeWithoutReplacingOriginal(t *testing.T) {
	agent := &Agent{
		customTools: map[string]CustomTool{
			"query_records": {Category: "database", Execution: noopTool},
		},
		toolToServer: map[string]string{"query_records": "custom"},
	}

	err := agent.RegisterCustomTool("query_records", "replacement", objectSchema(), noopTool, "workflow")
	if err == nil || !strings.Contains(err.Error(), `already registered in category "database"`) {
		t.Fatalf("RegisterCustomTool() error = %v, want category collision", err)
	}
	if got := agent.customTools["query_records"].Category; got != "database" {
		t.Fatalf("collision replaced original category with %q", got)
	}
}

func objectSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func noopTool(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}
