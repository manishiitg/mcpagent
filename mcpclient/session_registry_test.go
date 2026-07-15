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

func TestDeprecatedBrowserSessionCompatibilityShimsAreNoOps(t *testing.T) {
	registry := &SessionConnectionRegistry{
		connLocks: make(map[string]*sync.Mutex),
	}

	if IsBrowserScopedServer("playwright") {
		t.Fatal("legacy browser server must not regain session-scoped behavior")
	}
	registry.RegisterBrowserSessionOverride("tool-session", "browser-session")
	registry.ClearBrowserSessionOverride("tool-session")
	if got := registry.ResolveConnectionSessionID("tool-session", "playwright"); got != "global" {
		t.Fatalf("compatibility shims must preserve global connection pooling, got %q", got)
	}
}
