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
