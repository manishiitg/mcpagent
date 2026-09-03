package mcpagent

import (
	"os"
	"strconv"
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
// (from the provider's own typed error) lets a later turn retry the model
// once its window reopens, and that value is never invented here -- it is
// returned to callers exactly as given, including into the workflow
// capacity-wait suspend path, which must not be told a guessed time.
//
// Cursor never states a reset time at all (confirmed live 2026-09-03: its
// CLI error is plain text -- "Connection lost, reconnecting..." then
// "RetriableError: [resource_exhausted]" -- no timestamp, no retry-after).
// So every Cursor exhaustion takes the zero/unknown branch. Before this
// cache existed that was harmless: a fresh Agent retried for real on every
// turn anyway (slow, but self-healing). Backing an unknown-reset mark with
// *no* expiry at all would make it permanent for the life of the process --
// worse than before, since the same live incident recovered in about 13
// minutes with nothing telling us so. entry.learnedAt + unknownResetCooldown
// is a purely internal "worth trying again" throttle, never surfaced as a
// resetAt to any caller, so it cannot masquerade as a provider-stated fact.
var (
	sharedQuotaExhaustedMu     sync.RWMutex
	sharedQuotaExhaustedModels = map[string]sharedQuotaExhaustionEntry{}
)

type sharedQuotaExhaustionEntry struct {
	resetAt   time.Time // provider-stated reset time; zero = unknown, never guessed
	learnedAt time.Time // when this process last confirmed the model was exhausted
}

// unknownResetCooldown bounds how long an unknown-reset mark is trusted
// before the next turn is allowed a real attempt. Override with
// QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS for tests or a different deployment's
// observed recovery time.
func unknownResetCooldown() time.Duration {
	if raw := os.Getenv("QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 3 * time.Minute
}

// markModelQuotaExhaustedShared records (or refreshes) a model's exhaustion
// across the process, so every subsequent turn -- regardless of which Agent
// instance handles it -- skips the model immediately instead of relearning
// it from a slow provider round trip.
func markModelQuotaExhaustedShared(key string, resetAt time.Time) {
	sharedQuotaExhaustedMu.Lock()
	defer sharedQuotaExhaustedMu.Unlock()
	sharedQuotaExhaustedModels[key] = sharedQuotaExhaustionEntry{resetAt: resetAt, learnedAt: time.Now()}
}

// sharedModelQuotaExhaustion reports whether a model is currently known
// exhausted process-wide, and its stated reset time (zero = unknown reset --
// never a guess). A reopened stated window (resetAt in the past) is treated
// as not exhausted and forgotten, mirroring the per-agent reopened-window
// handling. An unknown-reset mark additionally expires after
// unknownResetCooldown so the model gets a real retry periodically instead
// of being benched for the rest of the process's life.
func sharedModelQuotaExhaustion(key string) (resetAt time.Time, exhausted bool) {
	sharedQuotaExhaustedMu.RLock()
	entry, exhausted := sharedQuotaExhaustedModels[key]
	sharedQuotaExhaustedMu.RUnlock()
	if !exhausted {
		return time.Time{}, false
	}
	now := time.Now()
	expired := false
	if !entry.resetAt.IsZero() {
		expired = !entry.resetAt.After(now)
	} else {
		expired = now.Sub(entry.learnedAt) >= unknownResetCooldown()
	}
	if expired {
		sharedQuotaExhaustedMu.Lock()
		delete(sharedQuotaExhaustedModels, key)
		sharedQuotaExhaustedMu.Unlock()
		return time.Time{}, false
	}
	return entry.resetAt, true
}

// forgetSharedModelQuotaExhaustion clears a model's shared exhaustion record.
// Used by tests to avoid cross-test leakage of the package-level cache.
func forgetSharedModelQuotaExhaustion(key string) {
	sharedQuotaExhaustedMu.Lock()
	delete(sharedQuotaExhaustedModels, key)
	sharedQuotaExhaustedMu.Unlock()
}
