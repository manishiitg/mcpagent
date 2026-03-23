package mcpagent

import "testing"

func TestGetCodeExecutionAPIBaseURLAddsSessionPrefix(t *testing.T) {
	agent := &Agent{
		APIBaseURL: "http://host.docker.internal:8000",
		SessionID:  "session-123",
	}

	got := agent.getCodeExecutionAPIBaseURL()
	want := "http://host.docker.internal:8000/s/session-123"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestGetCodeExecutionAPIBaseURLKeepsExistingSessionPrefix(t *testing.T) {
	agent := &Agent{
		APIBaseURL: "http://host.docker.internal:8000/s/session-123",
		SessionID:  "session-123",
	}

	got := agent.getCodeExecutionAPIBaseURL()
	want := "http://host.docker.internal:8000/s/session-123"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
