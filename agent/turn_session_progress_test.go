package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type retainedProgressCapture struct{ events chan *events.AgentEvent }

func (c *retainedProgressCapture) Name() string { return "retained-progress" }
func (c *retainedProgressCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	c.events <- event
	return nil
}

func TestRetainedProgressStreamsWhileFinalIsPending(t *testing.T) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{
		agent:                 &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}},
		watchCtx:              ctx,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { return "" },
		retainedProgressMessages: func(llm.Provider, string) []llmtypes.MessageContent {
			return []llmtypes.MessageContent{
				llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "test login"),
				{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "Checking login now."},
					llmtypes.ToolCall{ID: "browser-check"},
				}},
			}
		},
	}
	lifecycle := newCanonicalTurnLifecycle("")
	s.startRetainedCompletionWatch(lifecycle, "test login", llm.ProviderCursorCLI, llm.CodingAgentTransportTmux)
	select {
	case e := <-capture.events:
		chunk, ok := e.Data.(*events.StreamingChunkEvent)
		if !ok || chunk.Content != "Checking login now." || chunk.Source != "transcript" || chunk.IsDelta || e.TurnID != lifecycle.id {
			t.Fatalf("wrong progress event: %#v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("progress was hidden until final completion")
	}
	select {
	case e := <-capture.events:
		t.Fatalf("repeated poll duplicated progress or completed early: %#v", e)
	case <-time.After(600 * time.Millisecond):
	}
	s.stateMu.Lock()
	active := s.retainedActive
	s.stateMu.Unlock()
	if !active {
		t.Fatal("progress settled the turn")
	}
	cancel()
	// A replaced/closed watcher must not publish a late database read.
	s.stateMu.Lock()
	s.closed = true
	s.stateMu.Unlock()
	s.emitRetainedProgress(lifecycle, 1, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "stale")}, map[string]bool{})
	select {
	case e := <-capture.events:
		t.Fatalf("stale progress: %#v", e)
	default:
	}
}
