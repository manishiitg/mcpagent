package mcpagent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRegisterCustomToolRejectsMCPNameCollision(t *testing.T) {
	agent := &Agent{
		toolToServer: map[string]string{"query_records": "database"},
		toolRegistry: directToolRegistry(),
	}

	err := agent.registerCustomTool("query_records", "direct", objectSchema(), noopTool, "workflow")
	if err == nil || !strings.Contains(err.Error(), `already registered by MCP server "database"`) {
		t.Fatalf("RegisterCustomTool() error = %v, want MCP collision", err)
	}
	if got := len(agent.directToolSnapshot()); got != 0 {
		t.Fatalf("collision mutated canonical registry: %d direct tools", got)
	}
	if got := agent.toolToServer["query_records"]; got != "database" {
		t.Fatalf("collision replaced MCP owner with %q", got)
	}
}

func TestRegisterCustomToolRejectsCategoryChangeWithoutReplacingOriginal(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(directToolFixture("query_records", "database")),
		toolToServer: map[string]string{"query_records": "custom"},
	}

	err := agent.registerCustomTool("query_records", "replacement", objectSchema(), noopTool, "workflow")
	if err == nil || !strings.Contains(err.Error(), `already registered in category "database"`) {
		t.Fatalf("RegisterCustomTool() error = %v, want category collision", err)
	}
	registered, ok := agent.lookupDirectTool("query_records")
	if !ok {
		t.Fatal("collision removed original canonical registration")
	}
	if got := registered.DisplayGroup; got != "database" {
		t.Fatalf("collision replaced original category with %q", got)
	}
}

func TestRegisterCustomToolStoresOneCompleteCanonicalRecord(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(),
		toolToServer: make(map[string]string),
	}
	timeout := 45 * time.Second
	if err := agent.registerCustomToolWithTimeout(
		"query_records",
		"query records",
		objectSchema(),
		noopTool,
		timeout,
		"database",
	); err != nil {
		t.Fatal(err)
	}

	registered, ok := agent.lookupDirectTool("query_records")
	if !ok {
		t.Fatal("registered direct tool missing from canonical registry")
	}
	if registered.DisplayGroup != "database" || registered.Timeout != timeout || registered.Executor == nil {
		t.Fatalf("incomplete canonical record: %#v", registered)
	}
	if got := len(agent.directToolSnapshot()); got != 1 {
		t.Fatalf("canonical direct tool count = %d, want 1", got)
	}
	if got := len(agent.directToolExecutors()); got != 1 {
		t.Fatalf("derived executor count = %d, want 1", got)
	}
	if got := agent.toolToServer["query_records"]; got != "custom" {
		t.Fatalf("routing projection = %q, want custom", got)
	}
}

func objectSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func noopTool(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}
