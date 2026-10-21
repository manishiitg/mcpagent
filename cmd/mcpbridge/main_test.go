package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBridgeRequestErrorIdentifiesTimeoutLayer(t *testing.T) {
	got := bridgeRequestError("custom", "execute_shell_command", "session-1", 90*time.Minute, context.DeadlineExceeded)
	for _, want := range []string{"TIMEOUT", "layer=mcpbridge_http", "tool=execute_shell_command", "session=session-1", "timeout=1h30m0s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q missing %q", got, want)
		}
	}
}

func TestBridgeRequestErrorIdentifiesCancellation(t *testing.T) {
	got := bridgeRequestError("virtual", "call_generic_agent", "session-2", time.Minute, context.Canceled)
	for _, want := range []string{"CANCELED", "layer=mcpbridge_http", "tool=call_generic_agent", "session=session-2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q missing %q", got, want)
		}
	}
}

func TestBridgeMaxCallParsesOnlyPositiveSeconds(t *testing.T) {
	for raw, want := range map[string]time.Duration{"270": 270 * time.Second, " 30 ": 30 * time.Second, "": 0, "0": 0, "-5": 0, "abc": 0} {
		if got := bridgeMaxCall(raw); got != want {
			t.Fatalf("bridgeMaxCall(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestClarificationUsesLongRunningBridgeTimeout(t *testing.T) {
	if !isLongRunningDelegationTool("custom", "request_clarification") {
		t.Fatal("human clarification must not expire at the ordinary tool timeout")
	}
	if isLongRunningDelegationTool("mcp", "request_clarification") {
		t.Fatal("an external MCP tool must not inherit the platform clarification timeout")
	}
}
