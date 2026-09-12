package mcpagent

import (
	"context"
	"sync/atomic"
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
	for _, provider := range []llm.Provider{llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderMuseCLI, llm.ProviderPiCLI} {
		t.Run(string(provider), func(t *testing.T) { testRetainedProgressStreamsWhileFinalIsPending(t, provider) })
	}
}

func testRetainedProgressStreamsWhileFinalIsPending(t *testing.T, provider llm.Provider) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reads atomic.Int32
	s := &Session{
		agent:                 &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}},
		watchCtx:              ctx,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { return "" },
		retainedProgressMessages: func(llm.Provider, string) []llmtypes.MessageContent {
			if reads.Add(1) > 1 {
				return nil
			}
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
	s.startRetainedCompletionWatch(lifecycle, "test login", provider, llm.CodingAgentTransportTmux)
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
	index := 0
	s.emitRetainedProgress(lifecycle, 1, provider, func(llm.Provider, string) []llmtypes.MessageContent {
		t.Error("stale watcher consumed progress")
		return nil
	}, &index)
	select {
	case e := <-capture.events:
		t.Fatalf("stale progress: %#v", e)
	default:
	}
}

func TestRetainedCompletionFlushesProgressCommittedAfterPoll(t *testing.T) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var finalRead atomic.Bool
	var delivered atomic.Bool
	s := &Session{
		agent: &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}}, watchCtx: ctx,
		retainedProgressMessages: func(llm.Provider, string) []llmtypes.MessageContent {
			if !finalRead.Load() || delivered.Swap(true) {
				return nil
			}
			return []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "Updating the dashboard with a clear What we test tab.")}
		},
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { finalRead.Store(true); return "Dashboard updated." },
	}
	s.startRetainedCompletionWatch(newCanonicalTurnLifecycle(""), "update dashboard", llm.ProviderCursorCLI, llm.CodingAgentTransportTmux)
	for i := 0; i < 2; i++ {
		select {
		case event := <-capture.events:
			if i == 0 {
				if _, ok := event.Data.(*events.StreamingChunkEvent); !ok {
					t.Fatalf("completion preceded progress: %T", event.Data)
				}
			} else {
				if _, ok := event.Data.(*events.UnifiedCompletionEvent); !ok {
					t.Fatalf("expected completion: %T", event.Data)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("missing progress or completion")
		}
	}
}
