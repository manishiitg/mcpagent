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
		t.Fatalf("non-browser servers should use global session, got %q", got)
	}

	if got := registry.ResolveConnectionSessionID("tool-session", "playwright"); got != "tool-session" {
		t.Fatalf("browser servers should default to the tool session when no override exists, got %q", got)
	}

	registry.RegisterBrowserSessionOverride("tool-session", "browser-session")
	if got := registry.ResolveConnectionSessionID("tool-session", "playwright"); got != "browser-session" {
		t.Fatalf("browser override not applied for playwright: got %q", got)
	}

	registry.ClearBrowserSessionOverride("tool-session")
	if got := registry.ResolveConnectionSessionID("tool-session", "playwright"); got != "tool-session" {
		t.Fatalf("browser override should clear back to tool session, got %q", got)
	}
}
