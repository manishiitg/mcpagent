package mcpagent

import (
	"encoding/json"
	"testing"
)

func TestGetCodeExecutionAPIBaseURLAddsSessionPrefix(t *testing.T) {
	agent := &Agent{
		apiBaseURL: "http://host.docker.internal:8000",
		sessionID:  "session-123",
	}

	got := agent.getCodeExecutionAPIBaseURL()
	want := "http://host.docker.internal:8000/s/session-123"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestGetCodeExecutionAPIBaseURLKeepsExistingSessionPrefix(t *testing.T) {
	agent := &Agent{
		apiBaseURL: "http://host.docker.internal:8000/s/session-123",
		sessionID:  "session-123",
	}

	got := agent.getCodeExecutionAPIBaseURL()
	want := "http://host.docker.internal:8000/s/session-123"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildToolIndexSeparatesCustomGroupsFromRealMCPServers(t *testing.T) {
	registry := directToolRegistry(customToolFixture("query_workflow_db", "workflow_db"))
	if err := registry.register(registeredTool{
		Name:       "search_issues",
		Definition: toolFixture("search_issues"),
		Kind:       toolImplementationMCP,
		Source:     "github-server",
	}); err != nil {
		t.Fatalf("register MCP fixture: %v", err)
	}
	agent := &Agent{
		toolRegistry: registry,
		toolFilter: NewToolFilter(
			nil,
			[]string{"github-server"},
			nil,
			[]string{"workflow_db"},
			nil,
		),
	}

	raw, err := agent.buildToolIndex()
	if err != nil {
		t.Fatalf("build tool index: %v", err)
	}
	var index struct {
		CustomTools struct {
			Endpoint string `json:"endpoint"`
			Groups   map[string]struct {
				Endpoint string   `json:"endpoint"`
				Tools    []string `json:"tools"`
			} `json:"groups"`
		} `json:"custom_tools"`
		MCPServers map[string]struct {
			Endpoint string   `json:"endpoint"`
			Tools    []string `json:"tools"`
		} `json:"mcp_servers"`
	}
	if err := json.Unmarshal([]byte(raw), &index); err != nil {
		t.Fatalf("decode tool index: %v\n%s", err, raw)
	}

	workflowDB := index.CustomTools.Groups["workflow_db"]
	if index.CustomTools.Endpoint != "$MCP_CUSTOM/{tool}" || workflowDB.Endpoint != "$MCP_CUSTOM/{tool}" {
		t.Fatalf("custom DB tools have the wrong address: %+v", index.CustomTools)
	}
	if len(workflowDB.Tools) != 1 || workflowDB.Tools[0] != "query_workflow_db" {
		t.Fatalf("workflow_db group = %+v", workflowDB)
	}
	if _, leaked := index.MCPServers["workflow_db"]; leaked {
		t.Fatalf("custom group workflow_db leaked into real MCP servers: %s", raw)
	}
	github := index.MCPServers["github_server"]
	if github.Endpoint != "$MCP_MCP/github_server/{tool}" || len(github.Tools) != 1 || github.Tools[0] != "search_issues" {
		t.Fatalf("real MCP server route = %+v", github)
	}
}
