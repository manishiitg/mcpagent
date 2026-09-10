package mcpagent

import (
	"context"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Retained sends bypass GenerateContent and its stream channel. Publish their
// committed narration through the same transcript events used by normal turns.
// The final-response reader remains the only authority for completion.
func (s *Session) emitRetainedProgress(lifecycle *canonicalTurnLifecycle, seq uint64, provider llm.Provider, reader func(llm.Provider, string) []llmtypes.MessageContent, chunkIndex *int) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	current := !s.closed && s.retainedActive && s.retainedSeq == seq
	s.stateMu.Unlock()
	if !current {
		return
	}
	// Read only after checking the watcher under sendMu. The provider shares
	// an incremental cursor with the normal stream, so stale reads lose data.
	for _, message := range reader(provider, s.agent.sessionID) {
		if message.Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		for _, part := range message.Parts {
			var content string
			switch text := part.(type) {
			case llmtypes.TextContent:
				content = text.Text
			case *llmtypes.TextContent:
				if text != nil {
					content = text.Text
				}
			}
			content = strings.TrimSpace(content)
			if content == "" {
				continue
			}
			*chunkIndex++
			s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), &events.StreamingChunkEvent{
				BaseEventData: events.BaseEventData{Timestamp: time.Now()},
				Content:       content,
				ChunkIndex:    *chunkIndex,
				Source:        "transcript",
			})
		}
	}
}
