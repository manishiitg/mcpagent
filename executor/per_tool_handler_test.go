package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		toolName: func(context.Context, map[string]interface{}) (string, error) { return "trusted", nil },
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
	if resp.Result != "trusted" {
		t.Fatalf("result = %q, want trusted", resp.Result)
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
