package mcpagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type retainedCompletionCapture struct {
	mu     sync.Mutex
	events []*events.AgentEvent
	ready  chan struct{}
}

func (c *retainedCompletionCapture) Name() string { return "retained-completion-capture" }

func (c *retainedCompletionCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	if event == nil || event.Type != events.EventTypeUnifiedCompletion {
		return nil
	}
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	select {
	case c.ready <- struct{}{}:
	default:
	}
	return nil
}

func TestRetainedSessionOwnsCompletionAfterDirectDelivery(t *testing.T) {
	capture := &retainedCompletionCapture{ready: make(chan struct{}, 1)}
	agent := &Agent{sessionID: "retained-pi-session", provider: llm.ProviderPiCLI, listeners: []AgentEventListener{capture}}
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	session := &Session{
		agent:       agent,
		watchCtx:    watchCtx,
		watchCancel: watchCancel,
		retainedFinalResponse: func(provider llm.Provider, sessionID string, _ time.Time) string {
			if provider != llm.ProviderPiCLI || sessionID != "retained-pi-session" {
				t.Fatalf("reader identity = %s/%s", provider, sessionID)
			}
			return "upgrade complete"
		},
	}

	session.startRetainedCompletionWatch("upgrade the report", llm.ProviderPiCLI, llm.CodingAgentTransportTmux)
	select {
	case <-capture.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("retained turn did not emit canonical completion")
	}

	session.stateMu.Lock()
	active := session.retainedActive
	history := append([]llmtypes.MessageContent(nil), session.history...)
	session.stateMu.Unlock()
	if active {
		t.Fatal("retained turn remained active after final response")
	}
	if len(history) != 2 {
		t.Fatalf("retained history = %d messages, want user + assistant", len(history))
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.events) != 1 {
		t.Fatalf("completion events = %d, want exactly one", len(capture.events))
	}
	completion, ok := capture.events[0].Data.(*events.UnifiedCompletionEvent)
	if !ok {
		t.Fatalf("completion data = %T", capture.events[0].Data)
	}
	if completion.FinalResult != "upgrade complete" || completion.Metadata["source"] != "mcpagent_session" {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestSessionRunMarksAgentTurnInFlightForDirectReceiptSuppression(t *testing.T) {
	agent := &Agent{}
	session := &Session{agent: agent}

	// We only need to prove the state transition used by bridge receipt
	// suppression; invalid policy exits after the turn has been claimed and the
	// deferred cleanup must release it again.
	_, err := session.Run(context.Background(), Turn{
		Input:      "hello",
		ToolPolicy: ToolPolicy{AllowedTools: []string{" invalid "}},
	})
	if err == nil {
		t.Fatal("expected invalid tool policy")
	}
	if agent.isTurnInFlight() {
		t.Fatal("Session.Run left agent turn marked in flight")
	}
}

func TestSessionRunRejectsWhileRetainedTurnIsActive(t *testing.T) {
	agent := &Agent{}
	session := &Session{agent: agent, retainedActive: true}

	_, err := session.Run(context.Background(), Turn{Input: "overlapping turn"})
	if !errors.Is(err, ErrTurnAlreadyInFlight) {
		t.Fatalf("Run error = %v, want ErrTurnAlreadyInFlight", err)
	}
}
