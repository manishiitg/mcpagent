package codeexec

import (
	"context"
	"fmt"
	"testing"
)

func resetRegistryForTest(t *testing.T) {
	t.Helper()

	registryMu.Lock()
	previous := globalRegistry
	globalRegistry = nil
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		globalRegistry = previous
		registryMu.Unlock()
	})
}

func TestCallVirtualToolWithSessionFallsBackFromStaleEmptyDiscoveryScope(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		staleScope  = baseScope + ":vt:restored-terminal"
		latestScope = baseScope + ":vt:chat-agent"
	)
	staleCalls := 0
	latestCalls := 0

	InitRegistryVirtualToolsForSession(staleScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			staleCalls++
			return "", fmt.Errorf("server \"planner\" is not available. Available servers/categories: []. Use get_api_spec(server_name=\"<server>\", tool_name=\"<tool>\") with one of these server names")
		},
	}, nil)
	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			latestCalls++
			return "latest-spec", nil
		},
	}, nil)

	got, err := CallVirtualToolWithSession(context.Background(), staleScope, "get_api_spec", nil)
	if err != nil {
		t.Fatalf("CallVirtualToolWithSession() error = %v", err)
	}
	if got != "latest-spec" {
		t.Fatalf("CallVirtualToolWithSession() = %q, want latest-spec", got)
	}
	if staleCalls != 1 || latestCalls != 1 {
		t.Fatalf("calls stale=%d latest=%d, want 1/1", staleCalls, latestCalls)
	}
}

func TestCallVirtualToolWithSessionUsesLatestScopeForRawSession(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		latestScope = baseScope + ":vt:chat-agent"
	)

	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			return "latest-spec", nil
		},
	}, nil)

	got, err := CallVirtualToolWithSession(context.Background(), baseScope, "get_api_spec", nil)
	if err != nil {
		t.Fatalf("CallVirtualToolWithSession() error = %v", err)
	}
	if got != "latest-spec" {
		t.Fatalf("CallVirtualToolWithSession() = %q, want latest-spec", got)
	}
}

func TestCallVirtualToolWithSessionKeepsScopedNonEmptyDiscoveryErrors(t *testing.T) {
	resetRegistryForTest(t)

	const (
		baseScope   = "builder-session"
		staleScope  = baseScope + ":vt:child-agent"
		latestScope = baseScope + ":vt:chat-agent"
	)
	latestCalls := 0

	InitRegistryVirtualToolsForSession(staleScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			return "", fmt.Errorf("server \"planner\" is not available. Available servers/categories: [workspace_advanced]")
		},
	}, nil)
	InitRegistryVirtualToolsForSession(latestScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) {
			latestCalls++
			return "latest-spec", nil
		},
	}, nil)

	_, err := CallVirtualToolWithSession(context.Background(), staleScope, "get_api_spec", nil)
	if err == nil {
		t.Fatal("CallVirtualToolWithSession() error = nil, want scoped error")
	}
	if latestCalls != 0 {
		t.Fatalf("latest scope calls = %d, want 0", latestCalls)
	}
}

func TestCallCustomToolWithSessionDoesNotBorrowGlobalExecutor(t *testing.T) {
	resetRegistryForTest(t)

	globalCalls := 0
	InitRegistry(nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"call_generic_agent": func(context.Context, map[string]interface{}) (string, error) {
			globalCalls++
			return "wrong-workflow", nil
		},
	}, nil, nil)
	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ok", nil
		},
	}, nil)

	_, err := CallCustomToolWithSession(context.Background(), "workflow-a", "call_generic_agent", nil)
	if err == nil {
		t.Fatal("CallCustomToolWithSession() error = nil, want missing session tool error")
	}
	if globalCalls != 0 {
		t.Fatalf("global executor calls = %d, want 0", globalCalls)
	}
}

func TestCallCustomToolWithSessionKeepsLegacyFallbackWithoutSessionRegistry(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistry(nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"legacy_tool": func(context.Context, map[string]interface{}) (string, error) {
			return "legacy-result", nil
		},
	}, nil, nil)

	got, err := CallCustomToolWithSession(context.Background(), "uninitialized-session", "legacy_tool", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession() error = %v", err)
	}
	if got != "legacy-result" {
		t.Fatalf("CallCustomToolWithSession() = %q, want legacy-result", got)
	}
}

// The session tool allow list is how a per-turn ToolPolicy reaches the HTTP
// bridge. Direct tool calls are gated in-process by isToolAllowedForContext,
// but a code-executing agent can also reach a tool over HTTP, and that path is
// gated only here. Without these tests the bridge could silently stop enforcing
// the policy while every other test kept passing.
func TestCallCustomToolWithSessionBlocksToolOutsideAllowList(t *testing.T) {
	resetRegistryForTest(t)

	calls := 0
	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			calls++
			return "should-not-run", nil
		},
	}, nil)
	SetSessionToolAllowList("workflow-a", map[string]bool{"read_skill": true})

	_, err := CallCustomToolWithSession(context.Background(), "workflow-a", "execute_shell_command", nil)
	if err == nil {
		t.Fatal("CallCustomToolWithSession() error = nil, want allow-list rejection")
	}
	if calls != 0 {
		t.Fatalf("blocked executor ran %d times, want 0", calls)
	}
}

func TestCallCustomToolWithSessionPermitsToolInsideAllowList(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ran", nil
		},
	}, nil)
	SetSessionToolAllowList("workflow-a", map[string]bool{"execute_shell_command": true})

	got, err := CallCustomToolWithSession(context.Background(), "workflow-a", "execute_shell_command", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession() error = %v", err)
	}
	if got != "ran" {
		t.Fatalf("CallCustomToolWithSession() = %q, want ran", got)
	}
}

// A nil allow list means "no restriction", not "block everything". Turn.Run
// passes nil whenever ToolPolicy.AllowedTools is empty, so inverting this would
// break every unrestricted turn rather than fail closed on one.
func TestCallCustomToolWithSessionTreatsNilAllowListAsUnrestricted(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ran", nil
		},
	}, nil)
	SetSessionToolAllowList("workflow-a", nil)

	got, err := CallCustomToolWithSession(context.Background(), "workflow-a", "execute_shell_command", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession() error = %v", err)
	}
	if got != "ran" {
		t.Fatalf("CallCustomToolWithSession() = %q, want ran", got)
	}
}

// One session's allow list must not gate another's. Concurrent workflows share
// one process-wide registry, so a leak here would let a restricted workshop
// turn silently constrain an unrelated run.
func TestSessionToolAllowListDoesNotLeakAcrossSessions(t *testing.T) {
	resetRegistryForTest(t)

	executor := map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) {
			return "ran", nil
		},
	}
	InitRegistryForSession("restricted", executor, nil)
	InitRegistryForSession("unrestricted", executor, nil)
	SetSessionToolAllowList("restricted", map[string]bool{"read_skill": true})

	if _, err := CallCustomToolWithSession(context.Background(), "restricted", "execute_shell_command", nil); err == nil {
		t.Fatal("restricted session: error = nil, want allow-list rejection")
	}
	got, err := CallCustomToolWithSession(context.Background(), "unrestricted", "execute_shell_command", nil)
	if err != nil {
		t.Fatalf("unrestricted session: error = %v", err)
	}
	if got != "ran" {
		t.Fatalf("unrestricted session = %q, want ran", got)
	}
}

// A reserved tool such as get_api_spec lives in the session's VIRTUAL partition.
// The model addresses every tool by one name and cannot know which endpoint owns
// it, so a bridge routing get_api_spec to the custom endpoint used to fail with
// "custom tool get_api_spec is not registered for session ..." even though that
// exact session had it registered.
func TestCallCustomToolWithSessionResolvesReservedVirtualTools(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) { return "shell", nil },
	}, nil)
	InitRegistryVirtualToolsForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) { return "spec", nil },
	}, nil)

	got, err := CallCustomToolWithSession(context.Background(), "workflow-a", "get_api_spec", nil)
	if err != nil {
		t.Fatalf("CallCustomToolWithSession(get_api_spec) error = %v", err)
	}
	if got != "spec" {
		t.Fatalf("CallCustomToolWithSession(get_api_spec) = %q, want spec", got)
	}
}

// The virtual fallback is scoped to the same session. It must not become a route
// into another session's tools.
func TestVirtualFallbackDoesNotCrossSessions(t *testing.T) {
	resetRegistryForTest(t)

	InitRegistryForSession("workflow-a", map[string]func(context.Context, map[string]interface{}) (string, error){
		"execute_shell_command": func(context.Context, map[string]interface{}) (string, error) { return "shell", nil },
	}, nil)
	InitRegistryVirtualToolsForSession("workflow-b", map[string]func(context.Context, map[string]interface{}) (string, error){
		"get_api_spec": func(context.Context, map[string]interface{}) (string, error) { return "other-session", nil },
	}, nil)

	if _, err := CallCustomToolWithSession(context.Background(), "workflow-a", "get_api_spec", nil); err == nil {
		t.Fatal("session workflow-a borrowed workflow-b's virtual tool")
	}
}
