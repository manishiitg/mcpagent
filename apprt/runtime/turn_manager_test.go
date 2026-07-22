package runtime

import (
	"context"
	"testing"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
)

func steerableAgent(t *testing.T) *mcpagent.Agent {
	t.Helper()
	a := &mcpagent.Agent{}
	a.SetProvider(llm.ProviderClaudeCode) // a tmux CLI provider — accepts live input
	if !a.SupportsSteering() {
		t.Fatal("claude-code agent unexpectedly reports no steering support")
	}
	return a
}

func nonSteerableAgent(t *testing.T) *mcpagent.Agent {
	t.Helper()
	a := &mcpagent.Agent{}
	a.SetProvider(llm.ProviderOpenAI) // not a coding-agent transport — no live input
	if a.SupportsSteering() {
		t.Fatal("openai agent unexpectedly reports steering support")
	}
	return a
}

// TestSteerableInto pins the pure decision: steer only into an in-flight turn for
// the SAME conversation whose provider accepts live input.
func TestSteerableInto(t *testing.T) {
	steer := steerableAgent(t)
	cases := []struct {
		name   string
		active *activeTurn
		conv   string
		want   bool
	}{
		{"no active turn", nil, "c1", false},
		{"same conv, steerable", &activeTurn{conversationID: "c1", agent: steer}, "c1", true},
		{"different conv", &activeTurn{conversationID: "c1", agent: steer}, "c2", false},
		{"same conv, not steerable", &activeTurn{conversationID: "c1", agent: nonSteerableAgent(t)}, "c1", false},
		{"nil agent", &activeTurn{conversationID: "c1"}, "c1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := steerableInto(tc.active, tc.conv); got != tc.want {
				t.Fatalf("steerableInto = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestTrySteerRoutesToNotSteered covers every branch where TrySteer must decline
// (so the caller runs a normal turn) WITHOUT attempting a live-input send.
func TestTrySteerRoutesToNotSteered(t *testing.T) {
	ctx := context.Background()

	// No in-flight turn at all.
	tm := NewTurnManager()
	if out, err := tm.TrySteer(ctx, "c1", "hello"); out != NotSteered || err != nil {
		t.Fatalf("no active: got (%v,%v); want (NotSteered,nil)", out, err)
	}

	// In-flight turn, but for a different conversation.
	tm.setActiveTurnForTest("c1", steerableAgent(t), "c1")
	if out, err := tm.TrySteer(ctx, "c2", "hello"); out != NotSteered || err != nil {
		t.Fatalf("different conv: got (%v,%v); want (NotSteered,nil)", out, err)
	}

	// In-flight turn for the same conversation, but not a steerable provider.
	tm.setActiveTurnForTest("c1", nonSteerableAgent(t), "c1")
	if out, err := tm.TrySteer(ctx, "c1", "hello"); out != NotSteered || err != nil {
		t.Fatalf("not steerable: got (%v,%v); want (NotSteered,nil)", out, err)
	}

	// Empty message / conversation are declined.
	tm.setActiveTurnForTest("c1", steerableAgent(t), "c1")
	if out, _ := tm.TrySteer(ctx, "c1", "   "); out != NotSteered {
		t.Fatalf("empty message: got %v; want NotSteered", out)
	}
}

// setActiveTurnForTest injects an in-flight turn without a real Session (test-only).
func (tm *TurnManager) setActiveTurnForTest(conv string, agent *mcpagent.Agent, sessionID string) {
	tm.mu.Lock()
	tm.active = &activeTurn{conversationID: conv, agent: agent, sessionID: sessionID}
	tm.mu.Unlock()
}

// TestRunRegistersActiveDuringBodyAndClearsAfter proves Run makes the turn
// steer-detectable while body runs, returns body's reply, and clears the
// registration afterward.
func TestRunRegistersActiveDuringBodyAndClearsAfter(t *testing.T) {
	tm := NewTurnManager()
	agent := steerableAgent(t)
	var steerableDuringBody bool

	reply, err := tm.Run("conv-1",
		func() (*Session, error) { return &Session{agent: agent, sessionID: "conv-1"}, nil },
		func(*Session) (string, error) {
			tm.mu.Lock()
			a := tm.active
			tm.mu.Unlock()
			steerableDuringBody = steerableInto(a, "conv-1")
			return "the reply", nil
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply != "the reply" {
		t.Fatalf("reply = %q; want %q", reply, "the reply")
	}
	if !steerableDuringBody {
		t.Fatal("turn was not registered as steerable while its body ran")
	}
	tm.mu.Lock()
	after := tm.active
	tm.mu.Unlock()
	if after != nil {
		t.Fatal("active turn was not cleared after Run returned")
	}
}

// TestRunSerializesTurns proves the global gate: a second Run cannot enter its
// body while the first still holds the gate, and proceeds once it is released.
func TestRunSerializesTurns(t *testing.T) {
	tm := NewTurnManager()
	agent := steerableAgent(t)
	build := func() (*Session, error) { return &Session{agent: agent, sessionID: "c"}, nil }

	aInBody := make(chan struct{})
	aRelease := make(chan struct{})
	bStarted := make(chan struct{})

	go func() {
		_, _ = tm.Run("c", build, func(*Session) (string, error) {
			close(aInBody)
			<-aRelease
			return "a", nil
		})
	}()
	<-aInBody // A is inside its body, holding the gate

	go func() {
		_, _ = tm.Run("c", build, func(*Session) (string, error) {
			close(bStarted)
			return "b", nil
		})
	}()

	select {
	case <-bStarted:
		t.Fatal("B entered its body while A still held the gate — turns not serialized")
	case <-time.After(100 * time.Millisecond): // expected: B blocked on the gate
	}

	close(aRelease) // A finishes, releasing the gate
	select {
	case <-bStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("B never ran after A released the gate")
	}
}
