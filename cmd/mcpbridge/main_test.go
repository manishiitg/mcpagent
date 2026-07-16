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
