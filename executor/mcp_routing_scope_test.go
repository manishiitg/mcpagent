package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/mark3labs/mcp-go/mcp"
)

type routingClient struct {
	mcpclient.ClientInterface
	calls int
	name  string
}

func (c *routingClient) Ping(context.Context) error { return nil }
func (c *routingClient) Close() error               { return nil }
func (c *routingClient) CallTool(context.Context, string, map[string]interface{}) (*mcp.CallToolResult, error) {
	c.calls++
	return mcp.NewToolResultText(c.name), nil
}
func callRoutingTool(t *testing.T, h *ExecutorHandlers, session, server, tool string) MCPExecuteResponse {
	t.Helper()
	req := httptest.NewRequest("POST", "/tools/mcp/"+server+"/"+tool, strings.NewReader(`{}`))
	req.Header.Set("X-Session-ID", session)
	w := httptest.NewRecorder()
	h.HandlePerToolMCPRequest(w, req, server, tool)
	var result MCPExecuteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestMCPRoutingNeverFallsBackToAnotherProvidersTool(t *testing.T) {
	logger := loggerv2.NewNoop()
	a := &routingClient{name: "provider-a"}
	b := &routingClient{name: "provider-b"}
	codeexec.InitRegistry(map[string]mcpclient.ClientInterface{"routing-a": a, "routing-b": b}, nil, map[string]string{"collision_search": "routing-a"}, logger)
	h := NewExecutorHandlers("missing-config", logger)
	result := callRoutingTool(t, h, "", "routing-b", "collision_search")
	if !result.Success || !strings.Contains(result.Result, "provider-b") || a.calls != 0 || b.calls != 1 {
		t.Fatalf("wrong provider: %+v a=%d b=%d", result, a.calls, b.calls)
	}
	result = callRoutingTool(t, h, "routing-session", "routing-missing", "collision_search")
	if result.Success || a.calls != 0 || b.calls != 1 {
		t.Fatalf("unknown provider invoked another client: %+v", result)
	}
}
func TestMCPRoutingRefreshesScopeWithoutNewSession(t *testing.T) {
	logger := loggerv2.NewNoop()
	h := NewExecutorHandlers("missing-config", logger)
	registry := mcpclient.GetSessionRegistry()
	key := "routing-scope-test"
	a := &routingClient{name: "a"}
	b := &routingClient{name: "b"}
	registry.StoreConnection(key, "scope-a", a)
	registry.StoreConnection(key, "scope-b", b)
	t.Cleanup(func() { registry.CloseSession(key) })
	allowed := map[string]bool{"scope-a": true}
	h.SetMCPServerResolver(func(_ context.Context, session, server, tool string) (*ResolvedMCPServer, error) {
		if session != "same-chat" || !allowed[server] {
			return nil, fmt.Errorf("server denied in current scope")
		}
		return &ResolvedMCPServer{Name: server, ConnectionSessionID: key}, nil
	})
	if r := callRoutingTool(t, h, "same-chat", "scope-b", "search"); r.Success {
		t.Fatal("allowed before selection")
	}
	allowed["scope-b"] = true
	if r := callRoutingTool(t, h, "same-chat", "scope-b", "search"); !r.Success || b.calls != 1 {
		t.Fatalf("new selection not usable: %+v", r)
	}
	delete(allowed, "scope-b")
	if r := callRoutingTool(t, h, "same-chat", "scope-b", "search"); r.Success || b.calls != 1 {
		t.Fatal("removed server still callable")
	}
	if r := callRoutingTool(t, h, "another-chat", "scope-a", "search"); r.Success || a.calls != 0 {
		t.Fatal("scope leaked to another chat")
	}
}

func TestMCPRoutingHostResolverReceivesExactProviderName(t *testing.T) {
	h := NewExecutorHandlers("missing-config", loggerv2.NewNoop())
	seen := ""
	h.SetMCPServerResolver(func(_ context.Context, _, server, _ string) (*ResolvedMCPServer, error) {
		seen = server
		return nil, fmt.Errorf("denied")
	})
	callRoutingTool(t, h, "chat", "provider_name", "search")
	if seen != "provider_name" {
		t.Fatalf("rewrote explicit provider as %q", seen)
	}
}
