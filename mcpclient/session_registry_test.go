package mcpclient

import (
	"sync"
	"testing"
)

func TestResolveConnectionSessionID(t *testing.T) {
	registry := &SessionConnectionRegistry{
		connLocks: make(map[string]*sync.Mutex),
	}

	if got := registry.ResolveConnectionSessionID("tool-session", "github"); got != "global" {
		t.Fatalf("MCP servers should use the global connection session, got %q", got)
	}
}
