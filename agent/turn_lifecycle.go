package mcpagent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

type canonicalTurnLifecycleContextKey struct{}

// canonicalTurnLifecycle is the single owner of one accepted message's
// terminal event. Provider adapters translate their native completion signal;
// this lifecycle gives every provider the same stable identity and
// exactly-once outward completion semantics (PLAT-116).
type canonicalTurnLifecycle struct {
	id        string
	startedAt time.Time

	mu       sync.Mutex
	terminal bool
}

func newTurnID() string {
	return "turn_" + events.GenerateEventID()
}

func newCanonicalTurnLifecycle(requestedID string) *canonicalTurnLifecycle {
	id := strings.TrimSpace(requestedID)
	if id == "" {
		id = newTurnID()
	}
	return &canonicalTurnLifecycle{id: id, startedAt: time.Now()}
}

func withCanonicalTurnLifecycle(ctx context.Context, lifecycle *canonicalTurnLifecycle) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, canonicalTurnLifecycleContextKey{}, lifecycle)
}

func canonicalTurnLifecycleFromContext(ctx context.Context) *canonicalTurnLifecycle {
	if ctx == nil {
		return nil
	}
	lifecycle, _ := ctx.Value(canonicalTurnLifecycleContextKey{}).(*canonicalTurnLifecycle)
	return lifecycle
}

// prepareEvent stamps every event in a turn and admits at most one canonical
// completion. Returning false means the event is a duplicate terminal event
// and must not leave mcpagent.
func (l *canonicalTurnLifecycle) prepareEvent(eventData events.EventData) bool {
	if l == nil || eventData == nil {
		return true
	}
	if base, ok := eventData.(interface{ GetBaseEventData() *events.BaseEventData }); ok {
		data := base.GetBaseEventData()
		if data.Metadata == nil {
			data.Metadata = make(map[string]interface{})
		}
		data.Metadata["turn_id"] = l.id
	}
	completion, terminal := eventData.(*events.UnifiedCompletionEvent)
	if !terminal {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal {
		return false
	}
	l.terminal = true
	if completion.Metadata == nil {
		completion.Metadata = make(map[string]interface{})
	}
	completion.Metadata["turn_id"] = l.id
	completion.Metadata["canonical_turn_completion"] = true
	return true
}

func (l *canonicalTurnLifecycle) isTerminal() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.terminal
}
