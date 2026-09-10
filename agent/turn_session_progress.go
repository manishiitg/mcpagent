package mcpagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Retained sends bypass GenerateContent and its stream channel. Publish their
// committed narration through the same transcript events used by normal turns.
// The final-response reader remains the only authority for completion.
func (s *Session) emitRetainedProgress(lifecycle *canonicalTurnLifecycle, seq uint64, messages []llmtypes.MessageContent, seen map[string]bool) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	current := !s.closed && s.retainedActive && s.retainedSeq == seq
	s.stateMu.Unlock()
	if !current {
		return
	}
	for i, message := range messages {
		if message.Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		for j, part := range message.Parts {
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
			key := fmt.Sprintf("%d:%d:%s", i, j, content)
			if content == "" || seen[key] {
				continue
			}
			seen[key] = true
			s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), &events.StreamingChunkEvent{
				BaseEventData: events.BaseEventData{Timestamp: time.Now()},
				Content:       content,
				ChunkIndex:    len(seen),
				Source:        "transcript",
			})
		}
	}
}
