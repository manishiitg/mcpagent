package mcpagent

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

type scopeCloseClient struct {
	mcpclient.ClientInterface
	closed atomic.Int32
}

func (c *scopeCloseClient) Close() error { c.closed.Add(1); return nil }

// PLAT-746-876: two live agents may share a delegation key, but the last
// Agent/Session close must drop its client and pending lazy configs. Ordinary
// global clients and another delegation's authorization scope must survive.
func TestScopedMCPClientsReleasedAfterLastAgentOrSessionCloses(t *testing.T) {
	key := "vault_example__scope_" + strings.Repeat("a", 32)
	otherKey := "vault_example__scope_" + strings.Repeat("b", 32)
	first, second, other := &Agent{}, &Agent{}, &Agent{}
	first.retainScopedMCPClient(key)
	second.retainScopedMCPClient(key)
	other.retainScopedMCPClient(otherKey)
	defer first.Close()
	defer second.Close()
	defer other.Close()
	scoped, ordinary, otherClient := &scopeCloseClient{}, &scopeCloseClient{}, &scopeCloseClient{}
	pool := mcpclient.GetSessionRegistry()
	pool.StoreConnection("global", key, scoped)
	pool.StoreConnection("global", otherKey, otherClient)
	pool.StoreConnection("global", "ordinary-scope-test", ordinary)
	defer pool.CloseSessionServer("global", "ordinary-scope-test")
	pool.StoreServerConfig("lazy-scope-test", key, mcpclient.MCPServerConfig{URL: "https://example.com"})
	codeexec.InitRegistry(map[string]mcpclient.ClientInterface{key: scoped, otherKey: otherClient, "ordinary-scope-test": ordinary}, nil, map[string]string{"example_tool": key}, loggerv2.NewNoop())
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if scoped.closed.Load() != 0 || codeexec.MCPClientForServer(key) == nil {
		t.Fatal("first close removed another live agent's client")
	}
	session := &Session{agent: second}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if scoped.closed.Load() != 1 || codeexec.MCPClientForServer(key) != nil || pool.GetSessionConnections("global")[key] != nil {
		t.Fatal("last close retained scoped client or closed twice")
	}
	if _, exists := pool.GetServerConfig("lazy-scope-test", key); exists {
		t.Fatal("lazy authorization config retained")
	}
	if otherClient.closed.Load() != 0 || ordinary.closed.Load() != 0 {
		t.Fatal("cleanup crossed delegation/global boundary")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if otherClient.closed.Load() != 1 {
		t.Fatal("second delegation retained after close")
	}
	// An old delayed close cannot remove a replacement for the same key.
	replacement := &Agent{}
	replacement.retainScopedMCPClient(key)
	defer replacement.Close()
	live := &scopeCloseClient{}
	pool.StoreConnection("global", key, live)
	codeexec.InitRegistry(map[string]mcpclient.ClientInterface{key: live}, nil, nil, loggerv2.NewNoop())
	_ = first.Close()
	_ = second.Close()
	if live.closed.Load() != 0 || codeexec.MCPClientForServer(key) == nil {
		t.Fatal("old close removed replacement")
	}
	_ = replacement.Close()
	if live.closed.Load() != 1 {
		t.Fatal("replacement leaked")
	}
}
