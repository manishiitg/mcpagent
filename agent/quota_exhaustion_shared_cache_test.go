package mcpagent

import (
	"sync"
	"testing"
	"time"
)

// The whole point of the shared cache: a model marked exhausted by ONE
// *Agent instance (one turn) must be visible to a completely different
// instance (the next turn), which is what a.quotaExhaustedModels alone
// cannot do since agent_go builds a fresh Agent per request.
func TestSharedQuotaExhaustionCrossesAgentInstances(t *testing.T) {
	key := "cursor-cli/grok-4.6"
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	if _, exhausted := sharedModelQuotaExhaustion(key); exhausted {
		t.Fatal("must start unmarked")
	}

	resetAt := time.Now().Add(time.Hour)
	markModelQuotaExhaustedShared(key, resetAt)

	got, exhausted := sharedModelQuotaExhaustion(key)
	if !exhausted {
		t.Fatal("a later lookup (simulating the next turn's fresh Agent) must see the mark")
	}
	if !got.Equal(resetAt) {
		t.Errorf("resetAt = %v, want %v", got, resetAt)
	}
}

// A zero reset time means "exhausted, no reliable reset stated" (PLAT-101) —
// distinct from not-exhausted, and must never be turned into a guessed time.
func TestSharedQuotaExhaustionWithoutResetTimeStaysExhausted(t *testing.T) {
	key := "claude-code/claude-sonnet-5"
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	markModelQuotaExhaustedShared(key, time.Time{})
	got, exhausted := sharedModelQuotaExhaustion(key)
	if !exhausted || !got.IsZero() {
		t.Fatalf("resetAt=%v exhausted=%v, want zero/true", got, exhausted)
	}
}

// A window that has already reopened must self-clear on read, exactly like
// the per-agent map does, so a later turn retries the model instead of
// treating a stale mark as permanent.
func TestSharedQuotaExhaustionSelfClearsOnceReopened(t *testing.T) {
	key := "codex-cli/gpt-5.6-sol"
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	markModelQuotaExhaustedShared(key, time.Now().Add(-time.Minute))
	if _, exhausted := sharedModelQuotaExhaustion(key); exhausted {
		t.Fatal("a reopened window must not read back as exhausted")
	}
	// The self-clear must also have removed the entry outright.
	sharedQuotaExhaustedMu.RLock()
	_, stillPresent := sharedQuotaExhaustedModels[key]
	sharedQuotaExhaustedMu.RUnlock()
	if stillPresent {
		t.Fatal("a reopened window's entry must be forgotten, not merely read as false")
	}
}

func TestForgetSharedModelQuotaExhaustion(t *testing.T) {
	key := "pi-cli/google/gemini-3.8-flash"
	markModelQuotaExhaustedShared(key, time.Time{})
	forgetSharedModelQuotaExhaustion(key)
	if _, exhausted := sharedModelQuotaExhaustion(key); exhausted {
		t.Fatal("forget must clear the mark")
	}
}

// Concurrent turns across sessions mark and read the same shared cache; run
// under -race to prove the mutex actually protects the map.
func TestSharedQuotaExhaustionConcurrentAccess(t *testing.T) {
	key := "cursor-cli/auto"
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			markModelQuotaExhaustedShared(key, time.Now().Add(time.Hour))
		}()
		go func() {
			defer wg.Done()
			sharedModelQuotaExhaustion(key)
		}()
	}
	wg.Wait()
}
