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

// StartedCall records a single tool call when it starts.
type StartedCall struct {
	ID        string
	Name      string
	ArgsJSON  string
	StartedAt time.Time
}

// CompletedCall records a single tool call that finished.
type CompletedCall struct {
	ID          string // synthetic ID — consistent between ToolUse and ToolResult messages
	Name        string
	ArgsJSON    string // raw JSON string of the arguments
	Result      string
	StartedAt   time.Time
	CompletedAt time.Time
}

// Hook receives real-time notifications for HTTP-level tool calls.
type Hook struct {
	OnStart func(StartedCall)
	OnEnd   func(CompletedCall)
}

var (
	mu       sync.Mutex
	registry = make(map[string][]CompletedCall) // sessionID → calls
	hooks    = make(map[string]Hook)            // sessionID → real-time event hook
	idSeq    uint64
)

// RegisterHook installs a session-scoped hook and returns an unregister function.
func RegisterHook(sessionID string, hook Hook) func() {
	mu.Lock()
	hooks[sessionID] = hook
	mu.Unlock()
	return func() {
		mu.Lock()
		if current, ok := hooks[sessionID]; ok {
			if fmt.Sprintf("%p", current.OnStart) == fmt.Sprintf("%p", hook.OnStart) &&
				fmt.Sprintf("%p", current.OnEnd) == fmt.Sprintf("%p", hook.OnEnd) {
				delete(hooks, sessionID)
			}
		}
		mu.Unlock()
	}
}

// RecordStart records a tool start and notifies any session hook.
func RecordStart(sessionID, name, argsJSON string) string {
	id := fmt.Sprintf("toolu_%d", atomic.AddUint64(&idSeq, 1))
	start := StartedCall{
		ID:        id,
		Name:      name,
		ArgsJSON:  argsJSON,
		StartedAt: time.Now(),
	}
	mu.Lock()
	hook := hooks[sessionID]
	mu.Unlock()
	if hook.OnStart != nil {
		hook.OnStart(start)
	}
	return id
}

// Record appends a completed tool call for the given session.
// Returns the generated ID (used to match ToolUse ↔ ToolResult in history).
func Record(sessionID, name, argsJSON, result string) string {
	id := fmt.Sprintf("toolu_%d", atomic.AddUint64(&idSeq, 1))
	RecordEnd(sessionID, id, name, argsJSON, result, time.Time{})
	return id
}

// RecordEnd records a completed tool call and notifies any session hook.
func RecordEnd(sessionID, id, name, argsJSON, result string, startedAt time.Time) {
	completed := CompletedCall{
		ID:          id,
		Name:        name,
		ArgsJSON:    argsJSON,
		Result:      result,
		StartedAt:   startedAt,
		CompletedAt: time.Now(),
	}
	mu.Lock()
	registry[sessionID] = append(registry[sessionID], completed)
	hook := hooks[sessionID]
	mu.Unlock()
	if hook.OnEnd != nil {
		hook.OnEnd(completed)
	}
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
