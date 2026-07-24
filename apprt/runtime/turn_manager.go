package runtime

import (
	"context"
	"strings"
	"sync"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// TurnManager serializes agent turns and routes mid-turn user messages. It is the
// reusable "steer vs wait" layer for apps built on this runtime.
//
// The runtime shares process-global MCP bridge env, so turns MUST NOT overlap:
// TurnManager runs them one at a time (Run holds a global gate). While a turn is
// in flight it is registered per conversation, so a second message that arrives
// for the SAME conversation can be STEERED straight into the running turn — on
// providers that accept live input — instead of waiting for the gate. A message
// for a DIFFERENT conversation, or when the running turn is not steerable, falls
// through to a normal serialized turn (the pre-existing wait behavior).
//
// Typical use in a message handler:
//
//	if out, _ := tm.TrySteer(ctx, convID, text); out == runtime.Steered {
//	    return ackResponse // folded into the running turn; combined reply arrives on that turn
//	}
//	reply, err := tm.Run(convID,
//	    func() (*runtime.Session, error) { return runtime.New(ctx, cfg) },
//	    func(s *runtime.Session) (string, error) { return s.Ask(ctx, history) })
type TurnManager struct {
	mu     sync.Mutex
	active *activeTurn // the single in-flight turn (turnManagerGate ⇒ at most one)
}

// turnManagerGate is the actual "global gate" Run's doc comment promises:
// process-global bridge env means turns must not overlap NO MATTER HOW MANY
// *TurnManager instances exist in this process. A struct-instance field here
// would only serialize turns WITHIN one instance — nothing stops a caller
// from constructing a second *TurnManager (there's no enforced-singleton
// mechanism), and two instances' turns would then run concurrently against
// the same shared bridge env, exactly the failure this type exists to
// prevent. A package-level mutex makes "global" true regardless of instance
// count, matching what the doc comment on Run has always claimed.
var turnManagerGate sync.Mutex

type activeTurn struct {
	conversationID string
	agent          *mcpagent.Agent
	sessionID      string
}

// NewTurnManager returns a ready TurnManager.
func NewTurnManager() *TurnManager { return &TurnManager{} }

// SteerOutcome reports how TrySteer routed a message.
type SteerOutcome int

const (
	// NotSteered: no steerable in-flight turn for this conversation — the caller
	// should run a normal (serialized) turn via Run.
	NotSteered SteerOutcome = iota
	// Steered: the message was delivered as live input into the running turn; the
	// caller should acknowledge and NOT start a new turn.
	Steered
)

// TrySteer attempts to steer message into an in-flight turn for conversationID.
// It returns Steered only when a turn for that exact conversation is running AND
// its provider accepts live input; the message is then injected into that turn's
// live session. Otherwise it returns NotSteered and the caller runs a normal turn
// via Run (which will wait for any in-flight turn to finish — the prior
// wait-and-queue behavior, preserved for the not-steerable / cross-conversation
// cases).
func (tm *TurnManager) TrySteer(ctx context.Context, conversationID, message string) (SteerOutcome, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" || strings.TrimSpace(message) == "" {
		return NotSteered, nil
	}
	tm.mu.Lock()
	active := tm.active
	tm.mu.Unlock()

	if !steerableInto(active, conversationID) {
		return NotSteered, nil
	}
	if _, err := active.agent.DeliverUserMessage(ctx, mcpagent.UserMessageDeliveryRequest{
		SessionID: active.sessionID,
		Message:   message,
		Intent:    mcpagent.UserMessageDeliveryIntentAuto,
	}); err != nil {
		// The live-input send failed (e.g. the session went away mid-race). Fall
		// back to a normal turn rather than silently dropping the message.
		return NotSteered, err
	}
	return Steered, nil
}

// steerableInto is the pure predicate behind TrySteer: a turn is steerable when
// one is in flight for this exact conversation and its provider accepts live
// input. Factored out for deterministic testing.
func steerableInto(active *activeTurn, conversationID string) bool {
	return active != nil &&
		active.conversationID == conversationID &&
		active.agent != nil &&
		active.agent.SupportsSteering()
}

// Run serializes a turn for conversationID: it holds the global gate (so turns
// never overlap the shared bridge env), builds the turn's Session, registers it
// as the in-flight turn (so a concurrent TrySteer can steer into it), runs body
// with the built Session, then clears the registration and closes the Session.
// body returns the turn's reply.
func (tm *TurnManager) Run(
	conversationID string,
	build func() (*Session, error),
	body func(*Session) (string, error),
) (string, error) {
	turnManagerGate.Lock()
	defer turnManagerGate.Unlock()

	sess, err := build()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	tm.setActive(conversationID, sess)
	defer tm.clearActive()

	return body(sess)
}

func (tm *TurnManager) setActive(conversationID string, sess *Session) {
	tm.mu.Lock()
	tm.active = &activeTurn{
		conversationID: strings.TrimSpace(conversationID),
		agent:          sess.Agent(),
		sessionID:      sess.SessionID(),
	}
	tm.mu.Unlock()
}

func (tm *TurnManager) clearActive() {
	tm.mu.Lock()
	tm.active = nil
	tm.mu.Unlock()
}
