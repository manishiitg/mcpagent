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

func TestStreamingManagerForwardsContentChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-content-stream-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.StreamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 4),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Hello "}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "world"}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: ""}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 2 {
		t.Fatalf("callback chunks = %d, want 2 (empty content skipped)", len(callbackChunks))
	}
	if callbackChunks[0].Content != "Hello " || callbackChunks[1].Content != "world" {
		t.Fatalf("callback content = %q + %q", callbackChunks[0].Content, callbackChunks[1].Content)
	}

	var chunkEvents int
	for _, e := range listener.events {
		if _, ok := e.Data.(*events.StreamingChunkEvent); ok {
			chunkEvents++
		}
	}
	if chunkEvents != 2 {
		t.Fatalf("StreamingChunkEvent count = %d, want 2", chunkEvents)
	}
	if sm.totalChunks != 2 {
		t.Fatalf("totalChunks = %d, want 2", sm.totalChunks)
	}
	if sm.contentChunkIndex != 2 {
		t.Fatalf("contentChunkIndex = %d, want 2", sm.contentChunkIndex)
	}
}

func TestStreamingManagerForwardsToolCallStartChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-tool-start-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.StreamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeToolCallStart,
		ToolName:   "bash",
		ToolCallID: "call_123",
		ToolArgs:   `{"command":"ls"}`,
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 0 {
		t.Fatalf("callback should NOT receive ToolCallStart, got %d chunks", len(callbackChunks))
	}

	var toolStartEvents int
	for _, e := range listener.events {
		if tse, ok := e.Data.(*events.ToolCallStartEvent); ok {
			toolStartEvents++
			if tse.ToolName != "bash" {
				t.Fatalf("ToolName = %q, want bash", tse.ToolName)
			}
			if tse.ToolCallID != "call_123" {
				t.Fatalf("ToolCallID = %q, want call_123", tse.ToolCallID)
			}
			if tse.ToolParams.Arguments != `{"command":"ls"}` {
				t.Fatalf("ToolParams.Arguments = %q", tse.ToolParams.Arguments)
			}
		}
	}
	if toolStartEvents != 1 {
		t.Fatalf("ToolCallStartEvent count = %d, want 1", toolStartEvents)
	}
}

func TestStreamingManagerForwardsToolCallEndChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-tool-end-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.StreamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:         llmtypes.StreamChunkTypeToolCallEnd,
		ToolName:     "bash",
		ToolCallID:   "call_456",
		ToolResult:   "file1.txt\nfile2.txt",
		ToolDuration: 250 * time.Millisecond,
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 1 {
		t.Fatalf("callback should receive ToolCallEnd, got %d chunks", len(callbackChunks))
	}
	if callbackChunks[0].ToolName != "bash" {
		t.Fatalf("callback ToolName = %q, want bash", callbackChunks[0].ToolName)
	}

	var toolEndEvents int
	for _, e := range listener.events {
		if tee, ok := e.Data.(*events.ToolCallEndEvent); ok {
			toolEndEvents++
			if tee.ToolName != "bash" {
				t.Fatalf("ToolName = %q, want bash", tee.ToolName)
			}
			if tee.ToolCallID != "call_456" {
				t.Fatalf("ToolCallID = %q, want call_456", tee.ToolCallID)
			}
			if tee.Result != "file1.txt\nfile2.txt" {
				t.Fatalf("Result = %q", tee.Result)
			}
		}
	}
	if toolEndEvents != 1 {
		t.Fatalf("ToolCallEndEvent count = %d, want 1", toolEndEvents)
	}

	if len(sm.CLIToolCalls) != 1 {
		t.Fatalf("CLIToolCalls = %d, want 1", len(sm.CLIToolCalls))
	}
	if sm.CLIToolCalls[0].ToolName != "bash" {
		t.Fatalf("CLIToolCalls[0].ToolName = %q, want bash", sm.CLIToolCalls[0].ToolName)
	}
}

func TestStreamingManagerMixedChunkFlow(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-mixed-flow-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackTypes []llmtypes.StreamChunkType
	agent.StreamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackTypes = append(callbackTypes, chunk.Type)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 8),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "I'll list files."}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "bash", ToolCallID: "c1", ToolArgs: `{"cmd":"ls"}`}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "bash", ToolCallID: "c1", ToolResult: "a.txt", ToolDuration: 100 * time.Millisecond}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Found a.txt."}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "read", ToolCallID: "c2", ToolArgs: `{"path":"a.txt"}`}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "read", ToolCallID: "c2", ToolResult: "contents", ToolDuration: 50 * time.Millisecond}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Done."}
	close(sm.streamChan)
	<-sm.streamingDone

	// Callback receives: Content, ToolCallEnd, Content, ToolCallEnd, Content (no ToolCallStart)
	wantCallbackTypes := []llmtypes.StreamChunkType{
		llmtypes.StreamChunkTypeContent,
		llmtypes.StreamChunkTypeToolCallEnd,
		llmtypes.StreamChunkTypeContent,
		llmtypes.StreamChunkTypeToolCallEnd,
		llmtypes.StreamChunkTypeContent,
	}
	if len(callbackTypes) != len(wantCallbackTypes) {
		t.Fatalf("callback count = %d, want %d: %v", len(callbackTypes), len(wantCallbackTypes), callbackTypes)
	}
	for i, wt := range wantCallbackTypes {
		if callbackTypes[i] != wt {
			t.Fatalf("callback[%d] type = %q, want %q", i, callbackTypes[i], wt)
		}
	}

	// Events: 3 StreamingChunk + 2 ToolCallStart + 2 ToolCallEnd = 7
	var chunkEvents, startEvents, endEvents int
	for _, e := range listener.events {
		switch e.Data.(type) {
		case *events.StreamingChunkEvent:
			chunkEvents++
		case *events.ToolCallStartEvent:
			startEvents++
		case *events.ToolCallEndEvent:
			endEvents++
		}
	}
	if chunkEvents != 3 {
		t.Fatalf("StreamingChunkEvent count = %d, want 3", chunkEvents)
	}
	if startEvents != 2 {
		t.Fatalf("ToolCallStartEvent count = %d, want 2", startEvents)
	}
	if endEvents != 2 {
		t.Fatalf("ToolCallEndEvent count = %d, want 2", endEvents)
	}

	if sm.totalChunks != 3 {
		t.Fatalf("totalChunks = %d, want 3 (content only)", sm.totalChunks)
	}
	if len(sm.CLIToolCalls) != 2 {
		t.Fatalf("CLIToolCalls = %d, want 2", len(sm.CLIToolCalls))
	}
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
		Type:     llmtypes.StreamChunkTypeTerminal,
		Content:  "Codex CLI screen snapshot",
		Metadata: map[string]interface{}{"tmux_session": "codex-pane-1"},
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
	if got := chunkEvent.Metadata["tmux_session"]; got != "codex-pane-1" {
		t.Fatalf("metadata tmux_session = %v, want codex-pane-1", got)
	}
	if sm.totalChunks != 1 {
		t.Fatalf("totalChunks = %d, want 1", sm.totalChunks)
	}
}

func TestStreamingManagerDrainsAfterContextCancel(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-ctx-cancel-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.StreamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 8),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(ctx, agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "before cancel"}
	cancel()
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "after cancel"}
	close(sm.streamChan)

	select {
	case <-sm.streamingDone:
	case <-time.After(2 * time.Second):
		t.Fatal("streamingDone not signalled within 2s after context cancel + channel close")
	}

	if len(callbackChunks) < 1 {
		t.Fatalf("expected at least 1 callback chunk, got %d", len(callbackChunks))
	}
	if callbackChunks[0].Content != "before cancel" {
		t.Fatalf("first chunk = %q, want 'before cancel'", callbackChunks[0].Content)
	}
}

func TestStreamingManagerCLIToolCallsAccumulation(t *testing.T) {
	agent := &Agent{
		SessionID: "session-tool-accumulation-test",
		listeners: []AgentEventListener{&recordingAgentEventListener{}},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 8),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	tools := []struct {
		name   string
		callID string
		result string
	}{
		{"bash", "c1", "output1"},
		{"read", "c2", "file contents"},
		{"bash", "c3", "output2"},
	}

	for _, tc := range tools {
		sm.streamChan <- llmtypes.StreamChunk{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolName:     tc.name,
			ToolCallID:   tc.callID,
			ToolResult:   tc.result,
			ToolDuration: 100 * time.Millisecond,
		}
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(sm.CLIToolCalls) != 3 {
		t.Fatalf("CLIToolCalls = %d, want 3", len(sm.CLIToolCalls))
	}
	for i, tc := range tools {
		if sm.CLIToolCalls[i].ToolName != tc.name {
			t.Fatalf("CLIToolCalls[%d].ToolName = %q, want %q", i, sm.CLIToolCalls[i].ToolName, tc.name)
		}
		if sm.CLIToolCalls[i].ToolCallID != tc.callID {
			t.Fatalf("CLIToolCalls[%d].ToolCallID = %q, want %q", i, sm.CLIToolCalls[i].ToolCallID, tc.callID)
		}
		if sm.CLIToolCalls[i].ToolResult != tc.result {
			t.Fatalf("CLIToolCalls[%d].ToolResult = %q, want %q", i, sm.CLIToolCalls[i].ToolResult, tc.result)
		}
	}
}

func TestStreamingManagerEmptyContentSkipped(t *testing.T) {
	agent := &Agent{
		SessionID: "session-empty-content-test",
		listeners: []AgentEventListener{&recordingAgentEventListener{}},
	}

	var callbackCount int
	agent.StreamingCallback = func(_ llmtypes.StreamChunk) {
		callbackCount++
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 4),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: ""}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeTerminal, Content: ""}
	close(sm.streamChan)
	<-sm.streamingDone

	if callbackCount != 0 {
		t.Fatalf("callbackCount = %d, want 0 (all empty content should be skipped)", callbackCount)
	}
	if sm.totalChunks != 0 {
		t.Fatalf("totalChunks = %d, want 0", sm.totalChunks)
	}
}

func TestFinishStreamingEmitsEndEvent(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-finish-streaming-test",
		ModelID:   "claude-opus-4-6",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 4),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Hello"}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: " world"}

	totalTokens := 42
	cachedTokens := 10
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content:    "Hello world",
				StopReason: "end_turn",
				GenerationInfo: &llmtypes.GenerationInfo{
					TotalTokens:         &totalTokens,
					CachedContentTokens: &cachedTokens,
					Additional: map[string]interface{}{
						"claude_code_model": "claude-opus-4-6",
					},
				},
			},
		},
	}

	agent.finishStreaming(context.Background(), sm, resp)

	var endEvent *events.StreamingEndEvent
	for _, e := range listener.events {
		if ee, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvent = ee
		}
	}
	if endEvent == nil {
		t.Fatal("StreamingEndEvent not emitted by finishStreaming")
	}
	if endEvent.TotalChunks != 2 {
		t.Fatalf("TotalChunks = %d, want 2", endEvent.TotalChunks)
	}
	if endEvent.TotalTokens != 42 {
		t.Fatalf("TotalTokens = %d, want 42", endEvent.TotalTokens)
	}
	if endEvent.FinishReason != "end_turn" {
		t.Fatalf("FinishReason = %q, want end_turn", endEvent.FinishReason)
	}
	if endEvent.ResolvedModel != "claude-opus-4-6" {
		t.Fatalf("ResolvedModel = %q, want claude-opus-4-6", endEvent.ResolvedModel)
	}
	if endEvent.CacheTokens != 10 {
		t.Fatalf("CacheTokens = %d, want 10", endEvent.CacheTokens)
	}
}

func TestFinishStreamingTerminalEndIncludesRetentionMetadata(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-terminal-retention-test",
		provider:  "codex-cli",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeTerminal, Content: "terminal screen"}
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content: "done",
				GenerationInfo: &llmtypes.GenerationInfo{
					Additional: map[string]interface{}{
						"codex_interactive_session":           "tmux-codex-123",
						"terminal_retention_seconds":          300,
						"codex_interactive_retention_seconds": 300,
					},
				},
			},
		},
	}

	agent.finishStreaming(context.Background(), sm, resp)

	var endEvent *events.StreamingEndEvent
	for _, e := range listener.events {
		if ee, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvent = ee
		}
	}
	if endEvent == nil {
		t.Fatal("StreamingEndEvent not emitted")
	}
	if endEvent.Metadata["kind"] != "terminal" {
		t.Fatalf("metadata kind = %v, want terminal", endEvent.Metadata["kind"])
	}
	if endEvent.Metadata["terminal_retention_seconds"] != 300 {
		t.Fatalf("retention metadata = %v, want 300", endEvent.Metadata["terminal_retention_seconds"])
	}
	if endEvent.Metadata["tmux_session"] != "tmux-codex-123" {
		t.Fatalf("tmux_session metadata = %v", endEvent.Metadata["tmux_session"])
	}
}

func TestFinishStreamingSafeDoubleClose(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-double-close-test",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	// Simulate adapter closing the channel (normal flow)
	close(sm.streamChan)

	// finishStreaming does a safe double-close (recovered) then waits for processChunks to exit
	agent.finishStreaming(context.Background(), sm, nil)

	var endEvents int
	for _, e := range listener.events {
		if _, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvents++
		}
	}
	if endEvents != 1 {
		t.Fatalf("StreamingEndEvent count = %d, want 1 (should not panic on double close)", endEvents)
	}
}

func TestFinishStreamingNilManager(t *testing.T) {
	agent := &Agent{
		SessionID: "session-nil-sm-test",
		listeners: []AgentEventListener{&recordingAgentEventListener{}},
	}
	agent.finishStreaming(context.Background(), nil, nil)
}

func TestFinishStreamingGeminiMetadata(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-gemini-meta-test",
		ModelID:   "gemini-3.1-flash-lite",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 1),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	totalTokens := 100
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content:    "Gemini response",
				StopReason: "stop",
				GenerationInfo: &llmtypes.GenerationInfo{
					TotalTokens: &totalTokens,
					Additional: map[string]interface{}{
						"gemini_model":      "gemini-3.1-flash-lite",
						"gemini_tool_calls": 3,
					},
				},
			},
		},
	}

	agent.finishStreaming(context.Background(), sm, resp)

	var endEvent *events.StreamingEndEvent
	for _, e := range listener.events {
		if ee, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvent = ee
		}
	}
	if endEvent == nil {
		t.Fatal("StreamingEndEvent not emitted")
	}
	if endEvent.ResolvedModel != "gemini-3.1-flash-lite" {
		t.Fatalf("ResolvedModel = %q, want gemini-3.1-flash-lite", endEvent.ResolvedModel)
	}
	if endEvent.ToolCalls != 3 {
		t.Fatalf("ToolCalls = %d, want 3", endEvent.ToolCalls)
	}
	if endEvent.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want stop", endEvent.FinishReason)
	}
}

func TestFinishStreamingNilResponse(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		SessionID: "session-nil-resp-test",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 1),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	agent.finishStreaming(context.Background(), sm, nil)

	var endEvent *events.StreamingEndEvent
	for _, e := range listener.events {
		if ee, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvent = ee
		}
	}
	if endEvent == nil {
		t.Fatal("StreamingEndEvent not emitted even with nil response")
	}
	if endEvent.TotalTokens != 0 {
		t.Fatalf("TotalTokens = %d, want 0 (nil resp)", endEvent.TotalTokens)
	}
	if endEvent.ResolvedModel != "" {
		t.Fatalf("ResolvedModel = %q, want empty (nil resp)", endEvent.ResolvedModel)
	}
}
