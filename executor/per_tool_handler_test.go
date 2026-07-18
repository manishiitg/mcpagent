package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestPerToolSessionIDHeaderOverridesBodyAndStripsArg(t *testing.T) {
	args := map[string]interface{}{
		"session_id": "body-session",
		"query":      "keep",
	}
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/test", nil)
	req.Header.Set("X-Session-ID", "header-session")

	got := perToolSessionIDFromRequest(req, args)
	if got != "header-session" {
		t.Fatalf("session id = %q, want header-session", got)
	}
	if _, exists := args["session_id"]; exists {
		t.Fatal("session_id arg was not stripped")
	}
	if args["query"] != "keep" {
		t.Fatalf("non-session arg changed: %v", args)
	}
}

func TestPerToolSessionIDBodyUsedOnlyWithoutHeader(t *testing.T) {
	args := map[string]interface{}{"session_id": "body-session"}
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/test", nil)

	got := perToolSessionIDFromRequest(req, args)
	if got != "body-session" {
		t.Fatalf("session id = %q, want body-session", got)
	}
}

func TestPerToolCustomUsesTrustedHeaderSession(t *testing.T) {
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	t.Cleanup(func() {
		codeexec.CleanupSession("trusted-session")
		codeexec.CleanupSession("body-session")
	})

	toolName := "session_probe_custom"
	codeexec.InitRegistryForSession("trusted-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return SessionIDFromContext(ctx), nil
		},
	}, logger)
	codeexec.InitRegistryForSession("body-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) { return "body", nil },
	}, logger)

	handler := NewExecutorHandlers("", logger)
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{"session_id":"body-session"}`))
	req.Header.Set("X-Session-ID", "trusted-session")
	rr := httptest.NewRecorder()

	handler.HandlePerToolCustomRequest(rr, req, toolName)

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response failed: %s", resp.Error)
	}
	if resp.Result != "trusted-session" {
		t.Fatalf("trusted context session = %q, want trusted-session", resp.Result)
	}
}

func TestPerToolCustomReturnsStructuredExecutionID(t *testing.T) {
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	const sessionID = "execution-id-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	toolName := "execute_step"
	codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) {
			return "Step started in background.\nexecution_id: \"exec-step-grow-123\"", nil
		},
	}, logger)

	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{"step_id":"step-grow"}`))
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	NewExecutorHandlers("", logger).HandlePerToolCustomRequest(rr, req, toolName)

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response failed: %s", resp.Error)
	}
	if got := resp.Data["execution_id"]; got != "exec-step-grow-123" {
		t.Fatalf("data.execution_id = %q, want exec-step-grow-123", got)
	}
}

func TestPerToolVirtualUsesTrustedHeaderSession(t *testing.T) {
	logger := loggerv2.NewNoop()
	codeexec.InitRegistryWithVirtualTools(nil, nil, nil, nil, logger)
	t.Cleanup(func() {
		codeexec.CleanupSession("trusted-session")
		codeexec.CleanupSession("body-session")
	})

	toolName := "session_probe_virtual"
	codeexec.InitRegistryVirtualToolsForSession("trusted-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) { return "trusted", nil },
	}, logger)
	codeexec.InitRegistryVirtualToolsForSession("body-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) { return "body", nil },
	}, logger)

	handler := NewExecutorHandlers("", logger)
	req := httptest.NewRequest(http.MethodPost, "/tools/virtual/"+toolName, strings.NewReader(`{"session_id":"body-session"}`))
	req.Header.Set("X-Session-ID", "trusted-session")
	rr := httptest.NewRecorder()

	handler.HandlePerToolVirtualRequest(rr, req, toolName)

	var resp VirtualExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response failed: %s", resp.Error)
	}
	if resp.Result != "trusted" {
		t.Fatalf("result = %q, want trusted", resp.Result)
	}
}

func TestPerToolDelegationCancellationReachesCustomTool(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "")
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	const sessionID = "cancel-custom-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	started := make(chan struct{})
	toolName := "call_sub_agent"
	codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewExecutorHandlers("", logger).HandlePerToolCustomRequest(rr, req, toolName)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("custom tool did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("custom tool remained detached after request cancellation")
	}

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success || !strings.Contains(resp.Error, "layer=custom_tool_handler") || !strings.Contains(resp.Error, "canceled") {
		t.Fatalf("response = %+v, want attributed cancellation", resp)
	}
}

func TestPerToolDelegationCancellationReachesVirtualTool(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "")
	logger := loggerv2.NewNoop()
	codeexec.InitRegistryWithVirtualTools(nil, nil, nil, nil, logger)
	const sessionID = "cancel-virtual-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	started := make(chan struct{})
	toolName := "call_generic_agent"
	codeexec.InitRegistryVirtualToolsForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/tools/virtual/"+toolName, strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewExecutorHandlers("", logger).HandlePerToolVirtualRequest(rr, req, toolName)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("virtual tool did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("virtual tool remained detached after request cancellation")
	}

	var resp VirtualExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success || !strings.Contains(resp.Error, "layer=virtual_tool_handler") || !strings.Contains(resp.Error, "canceled") {
		t.Fatalf("response = %+v, want attributed cancellation", resp)
	}
}

func TestPerToolCustomTimeoutIdentifiesLayerAndTool(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "20ms")
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	const sessionID = "timeout-custom-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	toolName := "silent_tool"
	codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}, logger)

	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{}`))
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	NewExecutorHandlers("", logger).HandlePerToolCustomRequest(rr, req, toolName)

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{"timed out", "layer=custom_tool_handler", "tool=" + toolName, "session=" + sessionID, "timeout=20ms"} {
		if !strings.Contains(resp.Error, want) {
			t.Fatalf("error %q missing %q", resp.Error, want)
		}
	}
}
