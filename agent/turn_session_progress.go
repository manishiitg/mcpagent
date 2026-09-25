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
	// Check and read under stateMu, never sendMu. Send holds sendMu while a
	// busy CLI has not yet taken a steered message (Claude queues it until the
	// running tool returns), so waiting on it held narration written before a
	// long tool call until that tool finished. stateMu still orders the read
	// against the watcher replacement in startRetainedCompletionWatch: the
	// provider cursor is keyed by turn start, so a stale watcher must not read
	// after a new one exists. Emit after unlocking; listeners may re-enter.
	s.stateMu.Lock()
	current := !s.closed && s.retainedActive && s.retainedSeq == seq
	var messages []llmtypes.MessageContent
	if current {
		messages = reader(provider, s.agent.sessionID)
	}
	s.stateMu.Unlock()
	for _, message := range messages {
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
			case llmtypes.ThinkingContent:
				s.emitRetainedThinking(lifecycle, text.Thinking)
				continue
			case *llmtypes.ThinkingContent:
				if text != nil {
					s.emitRetainedThinking(lifecycle, text.Thinking)
				}
				continue
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

// emitRetainedThinking sends thinking as the same event the live stream uses
// for reasoning (Cursor, Pi), so the UI folds it as thinking and bots never
// forward it as a reply.
func (s *Session) emitRetainedThinking(lifecycle *canonicalTurnLifecycle, thinking string) {
	thinking = strings.TrimSpace(thinking)
	if thinking == "" {
		return
	}
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), &events.ConversationThinkingEvent{
		BaseEventData: events.BaseEventData{Timestamp: time.Now()},
		Thinking:      thinking,
	})
}
