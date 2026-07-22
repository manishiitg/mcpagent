package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
)

// firstToolCallSignal is a thread-safe listener that closes a channel the moment
// the first tool-call starts — the point at which the turn is provably in flight
// and blocked, so a steered message lands mid-turn.
type firstToolCallSignal struct {
	once sync.Once
	ch   chan struct{}
}

func newFirstToolCallSignal() *firstToolCallSignal {
	return &firstToolCallSignal{ch: make(chan struct{})}
}

func (l *firstToolCallSignal) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	if _, ok := event.Data.(*events.ToolCallStartEvent); ok {
		l.once.Do(func() { close(l.ch) })
	}
	return nil
}

func (l *firstToolCallSignal) Name() string { return "first-tool-call-signal" }

// TestCodingSessionDeliverSteerMidTurnClaude proves Deliver's steer path through
// the REAL bridge: while a turn is in flight (blocked in a long tool call),
// Deliver injects a live instruction into the RUNNING turn, and the model obeys
// it in the same turn's final reply.
//
// The turn is pinned open by a `sleep` tool call so the steer provably arrives
// mid-turn (Deliver sees TurnInFlight()==true and routes to live input). The
// steered secret word can ONLY appear in the reply if the mid-turn message
// actually reached the model.
//
// OPT-IN (RUN_MCPAGENT_STEER_E2E), separate from the main real-bridge gate,
// because live-model mid-turn timing is intermittently flaky: an observed run
// had claude hang after the steer and never finish the turn. The steer-vs-query
// ROUTING decision is covered deterministically by TestDecideDelivery and the
// underlying send-keys live-input mechanism is certified upstream
// (CertLiveInput/CertBusyLiveInput), so this e2e is a strong-but-manual
// integration proof, not an always-on gate.
func TestCodingSessionDeliverSteerMidTurnClaude(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_STEER_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_STEER_E2E=1 to run the (flaky, live-model) real-bridge steer e2e")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("authenticated `claude` CLI required")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))
	t.Setenv("CLAUDE_CODE_STREAM_TRANSCRIPT", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	workDir := t.TempDir()
	convID := "steer-" + realBridgeRandHex(4)
	steerWord := "PURPLE_" + realBridgeRandHex(6)

	agent, cleanup, err := buildRealBridgeClaudeAgent(ctx, t.TempDir(), workDir, convID, true)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	defer cleanup()

	signal := newFirstToolCallSignal()
	agent.AddEventListener(signal)
	store := NewMemoryCodingSessionStore()

	// Start the turn: a long tool call holds it open long enough to steer into.
	type turnResult struct {
		answer string
		err    error
	}
	done := make(chan turnResult, 1)
	go func() {
		ans, aerr := agent.ContinueConversation(ctx, convID,
			"Run exactly this one shell command and wait for it: sleep 25 && echo COMMAND_DONE. "+
				"When it finishes, report the word it printed, then stop.", store)
		done <- turnResult{answer: ans, err: aerr}
	}()

	// Wait until the turn is provably mid-tool, then give it a moment to settle
	// into the blocking sleep before steering.
	select {
	case <-signal.ch:
	case <-time.After(3 * time.Minute):
		t.Fatalf("timed out waiting for the first tool call to start the turn")
	}
	if !agent.TurnInFlight() {
		t.Fatalf("turn is not marked in flight at the tool-call boundary")
	}
	time.Sleep(2 * time.Second)

	// Steer a new instruction INTO the running turn via the library entry point.
	delivered, err := agent.Deliver(ctx, convID,
		fmt.Sprintf("Additional instruction: in your final reply, also include the exact word %s on its own line.", steerWord),
		store)
	if err != nil {
		t.Fatalf("Deliver (steer mid-turn): %v", err)
	}
	if delivered.Mode != DeliveryModeSteered {
		t.Fatalf("Deliver routed to %q; want %q (a live turn on a steerable transport)", delivered.Mode, DeliveryModeSteered)
	}

	var res turnResult
	select {
	case res = <-done:
	case <-time.After(8 * time.Minute):
		t.Fatalf("turn did not complete after steering (live-model mid-turn hang — see the opt-in note on this test)")
	}
	if res.err != nil {
		t.Fatalf("steered turn errored: %v", res.err)
	}
	answer := strings.TrimSpace(res.answer)
	t.Logf("steered turn answer: %q", answer)

	// The tool ran (COMMAND_DONE) AND the mid-turn steer landed (steerWord).
	if !strings.Contains(answer, "COMMAND_DONE") {
		t.Fatalf("the long tool call did not complete in-turn; answer=%q", answer)
	}
	if !strings.Contains(answer, steerWord) {
		t.Fatalf("steer did NOT reach the running turn: answer %q missing the steered word %q", answer, steerWord)
	}

	rec := agentreview.Write(t, "TestCodingSessionDeliverSteerMidTurnClaude",
		"Deliver steers a live instruction into a RUNNING turn (pinned open by a sleep tool): the model obeys the mid-turn word in the same turn's reply",
		map[string]any{
			"conversation_id":     convID,
			"delivery_mode":       string(delivered.Mode),
			"steered_word":        steerWord,
			"turn_answer":         answer,
			"tool_completed":      strings.Contains(answer, "COMMAND_DONE"),
			"steer_word_obeyed":   strings.Contains(answer, steerWord),
			"steer_reached_model": "steered word only appears if the mid-turn live input reached the model",
		},
		map[string]any{"steered_mid_turn": strings.Contains(answer, steerWord), "tool_ran": strings.Contains(answer, "COMMAND_DONE")},
	)
	agentreview.RequireReviewed(t, rec)
}
