// Package convrecord provides a shared, pluggable conversation/cost recorder
// for mcpagent consumers. It exists to close a real, observed duplication:
// AgentWorks (agent_go/cmd/server) and sparkquill (agent_go/cmd/family-server,
// same repo, different branch) each independently reimplemented "extract this
// turn's messages/tool-calls/tokens/cost, marshal it, write it somewhere, know
// how to read it back for resume" — in two different, incompatible shapes,
// with sparkquill's version tracking no cost at all.
//
// mcpagent owns the boilerplate (computing a correct, complete TurnRecord)
// and exposes the one thing that's genuinely app-specific as an extension
// point: Sink, where the data goes (a file, SQLite, anything).
//
// TurnRecord deliberately carries only token counts, not a computed USD cost.
// Pricing (which rate table, which provider's number to trust, whether a
// subscription CLI's cost is real or a shadow estimate) is a product/billing
// decision that varies per caller and per provider — not something this
// library can know on its own. A caller that wants a dollar figure computes
// it itself from TokenUsage, using whatever rate table and policy it already
// has (a costledger, a pricing table, provider-reported figures, etc.).
package convrecord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ToolCallRecord is one completed tool call, with timing.
type ToolCallRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ArgsJSON    string    `json:"args_json,omitempty"`
	Result      string    `json:"result,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	DurationMS  int64     `json:"duration_ms"`
}

// TokenUsage is the token breakdown for a single LLM call.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheTokens      int `json:"cache_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// TurnRecord is everything mcpagent knows about one completed LLM call,
// fully computed — a consumer's Sink only has to decide where it goes.
type TurnRecord struct {
	SessionID  string                    `json:"session_id,omitempty"`
	Turn       int                       `json:"turn"`
	Timestamp  time.Time                 `json:"timestamp"`
	Provider   string                    `json:"provider,omitempty"`
	ModelID    string                    `json:"model_id,omitempty"`
	DurationMS int64                     `json:"duration_ms"`
	Messages   []llmtypes.MessageContent `json:"messages,omitempty"`
	ToolCalls  []ToolCallRecord          `json:"tool_calls,omitempty"`
	TokenUsage TokenUsage                `json:"token_usage"`
}

// Sink is the only thing an app needs to implement — where the data goes.
// Deliberately not keyed by a session ID parameter: a Sink implementation
// closes over whatever identity it needs at construction time (a session ID,
// a fixed scope string, a user ID, nothing at all) so it fits both a
// per-session-file model and a single-canonical-conversation model without
// forcing either into the other's shape.
type Sink interface {
	// WriteTurn persists one completed turn. Called once per LLM call.
	WriteTurn(TurnRecord) error
	// LoadHistory returns the conversation history previously persisted by
	// this sink, for a caller that wants to resume. Returns (nil, nil) if
	// there is nothing to load yet.
	LoadHistory() ([]llmtypes.MessageContent, error)
}

// FileJSONSink is the default Sink: a single, rewritten-whole JSON file per
// conversation, mirroring the shape agent_go/cmd/server/chat_history_persistence.go
// already uses in production (conversation_history + a running turns log),
// so a consumer already familiar with that file shape gets the same one for
// free instead of reimplementing it.
type FileJSONSink struct {
	path string
	mu   sync.Mutex
}

// NewFileJSONSink returns a Sink that persists to the given file path,
// creating parent directories as needed. The file is read-modify-written
// whole on every WriteTurn — appropriate for the low-frequency,
// human-readable use case this mirrors (one file per chat session), not a
// high-volume audit log (see the package doc for why cost accounting at
// AgentWorks' scale moved to SQLite instead).
func NewFileJSONSink(path string) *FileJSONSink {
	return &FileJSONSink{path: path}
}

type fileJSONDocument struct {
	UpdatedAt           time.Time                 `json:"updated_at"`
	ConversationHistory []llmtypes.MessageContent `json:"conversation_history"`
	Turns               []TurnRecord              `json:"turns"`
}

func (s *FileJSONSink) WriteTurn(rec TurnRecord) error {
	if s == nil {
		return fmt.Errorf("convrecord: nil FileJSONSink")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readLocked()
	if err != nil {
		return err
	}
	if len(rec.Messages) > 0 {
		doc.ConversationHistory = rec.Messages
	}
	doc.Turns = append(doc.Turns, rec)
	doc.UpdatedAt = time.Now().UTC()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("convrecord: create dir for %s: %w", s.path, err)
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("convrecord: marshal %s: %w", s.path, err)
	}
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		return fmt.Errorf("convrecord: write %s: %w", s.path, err)
	}
	return nil
}

func (s *FileJSONSink) LoadHistory() ([]llmtypes.MessageContent, error) {
	if s == nil {
		return nil, fmt.Errorf("convrecord: nil FileJSONSink")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	return doc.ConversationHistory, nil
}

func (s *FileJSONSink) readLocked() (fileJSONDocument, error) {
	var doc fileJSONDocument
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, fmt.Errorf("convrecord: read %s: %w", s.path, err)
	}
	if len(b) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return doc, fmt.Errorf("convrecord: parse %s: %w", s.path, err)
	}
	return doc, nil
}
