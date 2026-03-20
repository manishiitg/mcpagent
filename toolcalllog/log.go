// Package toolcalllog provides a session-scoped registry of completed tool calls.
// It is written to by the HTTP tool execution layer (executor/handlers.go) and
// read by LLMAgentWrapper on cancellation to reconstruct conversation history.
package toolcalllog

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// CompletedCall records a single tool call that finished successfully.
type CompletedCall struct {
	ID        string // synthetic ID — consistent between ToolUse and ToolResult messages
	Name      string
	ArgsJSON  string // raw JSON string of the arguments
	Result    string
	CompletedAt time.Time
}

var (
	mu       sync.Mutex
	registry = make(map[string][]CompletedCall) // sessionID → calls
	idSeq    uint64
)

// Record appends a completed tool call for the given session.
// Returns the generated ID (used to match ToolUse ↔ ToolResult in history).
func Record(sessionID, name, argsJSON, result string) string {
	id := fmt.Sprintf("toolu_%d", atomic.AddUint64(&idSeq, 1))
	mu.Lock()
	registry[sessionID] = append(registry[sessionID], CompletedCall{
		ID:          id,
		Name:        name,
		ArgsJSON:    argsJSON,
		Result:      result,
		CompletedAt: time.Now(),
	})
	mu.Unlock()
	return id
}

// GetAndClear returns all recorded calls for the session and removes them.
func GetAndClear(sessionID string) []CompletedCall {
	mu.Lock()
	calls := registry[sessionID]
	delete(registry, sessionID)
	mu.Unlock()
	return calls
}

// Clear discards all recorded calls for the session (call at start of a new run).
func Clear(sessionID string) {
	mu.Lock()
	delete(registry, sessionID)
	mu.Unlock()
}
