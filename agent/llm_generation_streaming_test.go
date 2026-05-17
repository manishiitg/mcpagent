package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type recordingAgentEventListener struct {
	events []*events.AgentEvent
}

func (l *recordingAgentEventListener) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	l.events = append(l.events, event)
	return nil
}

func (l *recordingAgentEventListener) Name() string {
	return "recording-agent-event-listener"
}

func TestStreamingManagerForwardsTerminalChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-terminal-stream-test",
		listeners: []AgentEventListener{
			listener,
		},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.StreamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 1),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:    llmtypes.StreamChunkTypeTerminal,
		Content: "Codex CLI screen snapshot",
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 1 {
		t.Fatalf("callback chunks = %d, want 1", len(callbackChunks))
	}
	if callbackChunks[0].Type != llmtypes.StreamChunkTypeTerminal {
		t.Fatalf("callback chunk type = %q, want terminal", callbackChunks[0].Type)
	}

	if len(listener.events) != 1 {
		t.Fatalf("events = %d, want 1", len(listener.events))
	}
	chunkEvent, ok := listener.events[0].Data.(*events.StreamingChunkEvent)
	if !ok {
		t.Fatalf("event data type = %T, want *events.StreamingChunkEvent", listener.events[0].Data)
	}
	if chunkEvent.Content != "Codex CLI screen snapshot" {
		t.Fatalf("content = %q", chunkEvent.Content)
	}
	if got := chunkEvent.Metadata["kind"]; got != "terminal" {
		t.Fatalf("metadata kind = %v, want terminal", got)
	}
	if sm.totalChunks != 1 {
		t.Fatalf("totalChunks = %d, want 1", sm.totalChunks)
	}
}
