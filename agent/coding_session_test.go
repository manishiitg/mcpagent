package mcpagent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestDecideDelivery pins the pure steer-vs-query routing truth table — the
// decision the whole abstraction hangs on — with no live session involved.
func TestDecideDelivery(t *testing.T) {
	cases := []struct {
		name             string
		turnInFlight     bool
		supportsSteering bool
		want             deliveryDecision
	}{
		{"idle steerable -> start turn", false, true, decideStartTurn},
		{"idle query-only -> start turn", false, false, decideStartTurn},
		{"busy steerable -> steer", true, true, decideSteer},
		{"busy query-only -> queue", true, false, decideQueue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideDelivery(tc.turnInFlight, tc.supportsSteering); got != tc.want {
				t.Fatalf("decideDelivery(busy=%v, steerable=%v) = %q; want %q", tc.turnInFlight, tc.supportsSteering, got, tc.want)
			}
		})
	}
}

// TestMemoryCodingSessionStore covers the process-local store: cold load, round
// trip, isolation from later mutation, and delete.
func TestMemoryCodingSessionStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCodingSessionStore()

	if h, err := store.Load(ctx, "conv-1"); err != nil || h != nil {
		t.Fatalf("cold Load = (%v, %v); want (nil, nil)", h, err)
	}

	handle := &AgentSessionHandle{
		SessionID: "conv-1",
		Scope:     "coding_agent",
		Provider:  llmtypes.CodingProviderSessionHandle{Provider: "claude_code", NativeSessionID: "native-abc"},
	}
	if err := store.Save(ctx, "conv-1", handle); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Mutating the original after Save must not affect the stored copy.
	handle.Provider.NativeSessionID = "MUTATED"

	got, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil || got.Provider.NativeSessionID != "native-abc" {
		t.Fatalf("Load = %+v; want stored copy with native-abc (store aliased the caller's handle)", got)
	}

	if err := store.Delete(ctx, "conv-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if h, err := store.Load(ctx, "conv-1"); err != nil || h != nil {
		t.Fatalf("post-Delete Load = (%v, %v); want (nil, nil)", h, err)
	}
}

// TestFileCodingSessionStore covers the durable store, including that a FRESH
// store instance (simulating a process restart) reads back a handle written by
// an earlier one — the property that makes --resume-after-restart work.
func TestFileCodingSessionStore(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "sessions")

	store := NewFileCodingSessionStore(dir)
	if h, err := store.Load(ctx, "chat/../weird id"); err != nil || h != nil {
		t.Fatalf("cold Load = (%v, %v); want (nil, nil)", h, err)
	}

	handle := &AgentSessionHandle{
		SessionID: "chat/../weird id",
		Provider:  llmtypes.CodingProviderSessionHandle{Provider: "codex_cli", NativeSessionID: "thread-42", WorkingDir: "/work"},
	}
	if err := store.Save(ctx, "chat/../weird id", handle); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate a process restart: a brand-new store over the same dir must read
	// back the handle the old instance persisted.
	restarted := NewFileCodingSessionStore(dir)
	got, err := restarted.Load(ctx, "chat/../weird id")
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	if got == nil || got.Provider.NativeSessionID != "thread-42" || got.Provider.WorkingDir != "/work" {
		t.Fatalf("Load after restart = %+v; want thread-42 / /work", got)
	}

	// Distinct ids must not collide (hash suffix), even with the same sanitized prefix.
	other := &AgentSessionHandle{SessionID: "chat/../weird_id", Provider: llmtypes.CodingProviderSessionHandle{Provider: "codex_cli", NativeSessionID: "thread-99"}}
	if err := store.Save(ctx, "chat/../weird_id", other); err != nil {
		t.Fatalf("Save other: %v", err)
	}
	if got, _ := restarted.Load(ctx, "chat/../weird id"); got == nil || got.Provider.NativeSessionID != "thread-42" {
		t.Fatalf("collision: first id's handle was overwritten by a look-alike id: %+v", got)
	}

	if err := restarted.Delete(ctx, "chat/../weird id"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if h, err := store.Load(ctx, "chat/../weird id"); err != nil || h != nil {
		t.Fatalf("post-Delete Load = (%v, %v); want (nil, nil)", h, err)
	}
}

// TestSupportsSteeringMatchesContract proves SupportsSteering is driven by the
// provider contract (all tmux CLI providers steerable today; a non-coding
// provider is not).
func TestSupportsSteeringMatchesContract(t *testing.T) {
	for _, provider := range []llm.Provider{
		llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderAgyCLI, llm.ProviderPiCLI,
	} {
		a := &Agent{provider: provider}
		contract, ok := llm.GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Fatalf("%s: expected a coding-agent contract", provider)
		}
		if got := a.SupportsSteering(); got != contract.SupportsLiveInput {
			t.Fatalf("%s: SupportsSteering()=%v; contract.SupportsLiveInput=%v", provider, got, contract.SupportsLiveInput)
		}
	}
	// A non-coding provider has no contract, so steering is unsupported.
	if (&Agent{provider: llm.ProviderOpenAI}).SupportsSteering() {
		t.Fatalf("non-coding provider must not report steering support")
	}
}

// TestEnablePersistentInteractiveForProvider proves each coding-CLI provider's
// keep-alive flag is the one flipped on (and a non-CLI provider flips none).
func TestEnablePersistentInteractiveForProvider(t *testing.T) {
	cases := []struct {
		provider llm.Provider
		get      func(*Agent) bool
	}{
		{llm.ProviderClaudeCode, func(a *Agent) bool { return a.ClaudeCodePersistentInteractiveSession }},
		{llm.ProviderCodexCLI, func(a *Agent) bool { return a.CodexPersistentInteractiveSession }},
		{llm.ProviderCursorCLI, func(a *Agent) bool { return a.CursorPersistentInteractiveSession }},
		{llm.ProviderAgyCLI, func(a *Agent) bool { return a.AgyPersistentInteractiveSession }},
		{llm.ProviderPiCLI, func(a *Agent) bool { return a.PiPersistentInteractiveSession }},
	}
	for _, tc := range cases {
		a := &Agent{provider: tc.provider}
		a.enablePersistentInteractiveForProvider()
		if !tc.get(a) {
			t.Fatalf("%s: keep-alive flag not enabled", tc.provider)
		}
		// No other provider's flag should have been touched.
		for _, other := range cases {
			if other.provider == tc.provider {
				continue
			}
			if other.get(a) {
				t.Fatalf("%s: enabling keep-alive wrongly set %s's flag too", tc.provider, other.provider)
			}
		}
	}
}

// TestDeliverQueuesWhenBusyAndNotSteerable exercises the real Deliver path for
// the query-only branch end-to-end (no live session needed): a non-steerable
// agent with a turn in flight must QUEUE the message via the steer buffer.
func TestDeliverQueuesWhenBusyAndNotSteerable(t *testing.T) {
	a := &Agent{provider: llm.ProviderOpenAI} // no coding contract => not steerable
	a.setTurnInFlight(true)

	got, err := a.Deliver(context.Background(), "conv-x", "please also add tests", nil)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got.Mode != DeliveryModeQueued {
		t.Fatalf("Deliver mode = %q; want %q", got.Mode, DeliveryModeQueued)
	}
	drained := a.DrainSteerMessages()
	if len(drained) != 1 || drained[0] != "please also add tests" {
		t.Fatalf("queued messages = %v; want the delivered message", drained)
	}
}

// TestDeliverEmptyMessageRejected guards the empty-message contract.
func TestDeliverEmptyMessageRejected(t *testing.T) {
	a := &Agent{provider: llm.ProviderOpenAI}
	if _, err := a.Deliver(context.Background(), "conv-x", "   ", nil); err == nil {
		t.Fatalf("expected an error for an empty message")
	}
}
