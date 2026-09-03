package mcpagent

import (
	"sync"
	"time"
)

// sharedQuotaExhaustedModels persists quota-exhaustion state across Agent
// instances within this process. Each chat/workflow turn builds a fresh
// *Agent (agent_go's server does this per request), so a.quotaExhaustedModels
// alone only protects retries WITHIN one turn's own fallback loop -- the very
// next turn on the same session (or a different session sharing the same
// account) rediscovers an already-known exhaustion the slow way, waiting
// through the provider CLI's own internal backoff again. Observed live on
// RTS 2026-09-03: cursor-cli/grok-4.6 took 142s to report resource_exhausted,
// and the next chat turn on the same session paid that 142s again rather
// than skipping the model immediately.
//
// Keyed identically to a.quotaExhaustedModels ("provider/model_id"); same
// zero-value-means-unknown-reset semantics (PLAT-101): a stated reset time
// lets a later turn retry the model once its window reopens, an unknown
// reset stays skipped without ever being turned into a guess.
var (
	sharedQuotaExhaustedMu     sync.RWMutex
	sharedQuotaExhaustedModels = map[string]time.Time{}
)

// markModelQuotaExhaustedShared records (or refreshes) a model's exhaustion
// across the process, so every subsequent turn -- regardless of which Agent
// instance handles it -- skips the model immediately instead of relearning
// it from a slow provider round trip.
func markModelQuotaExhaustedShared(key string, resetAt time.Time) {
	sharedQuotaExhaustedMu.Lock()
	defer sharedQuotaExhaustedMu.Unlock()
	sharedQuotaExhaustedModels[key] = resetAt
}

// sharedModelQuotaExhaustion reports whether a model is currently known
// exhausted process-wide, and its stated reset time (zero = unknown reset).
// A reopened window (resetAt in the past) is treated as not exhausted and
// forgotten here, mirroring the per-agent reopened-window handling.
func sharedModelQuotaExhaustion(key string) (resetAt time.Time, exhausted bool) {
	sharedQuotaExhaustedMu.RLock()
	resetAt, exhausted = sharedQuotaExhaustedModels[key]
	sharedQuotaExhaustedMu.RUnlock()
	if !exhausted {
		return time.Time{}, false
	}
	if !resetAt.IsZero() && !resetAt.After(time.Now()) {
		sharedQuotaExhaustedMu.Lock()
		delete(sharedQuotaExhaustedModels, key)
		sharedQuotaExhaustedMu.Unlock()
		return time.Time{}, false
	}
	return resetAt, true
}

// forgetSharedModelQuotaExhaustion clears a model's shared exhaustion record.
// Used by tests to avoid cross-test leakage of the package-level cache.
func forgetSharedModelQuotaExhaustion(key string) {
	sharedQuotaExhaustedMu.Lock()
	delete(sharedQuotaExhaustedModels, key)
	sharedQuotaExhaustedMu.Unlock()
}
