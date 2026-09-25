package mcpagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/llm"
)

// stuckSteerQueueAge bounds how long a queued steer message may wait for a
// drain. Past it, the queue is proven orphaned: no running turn will pick
// it up (a pooled miss already proved the CLI target is gone, and drains
// happen inside the turn loop). Delivery then breaks the stuck turn —
// resetting its flag and reporting no-target so the caller starts a fresh
// turn — instead of parking another message forever. Five minutes sits
// beyond the adapter durable-ack budgets while boot races resolve in
// seconds, so legitimate queueing never trips it.
const stuckSteerQueueAge = 5 * time.Minute

// CodingAgentDeliveryErrorKind classifies why a DeliverUserMessage call failed.
// This is distinct from CodingAgentContinuationError, which covers provider-level
// session resume failures.
type CodingAgentDeliveryErrorKind string

const (
	DeliveryErrorKindEmptyMessage CodingAgentDeliveryErrorKind = "empty_message"
	DeliveryErrorKindNotSupported CodingAgentDeliveryErrorKind = "not_supported"
	DeliveryErrorKindNoSession    CodingAgentDeliveryErrorKind = "no_active_session"
	DeliveryErrorKindTimeout      CodingAgentDeliveryErrorKind = "delivery_timed_out"
)

// CodingAgentDeliveryError is returned when DeliverUserMessage cannot route a
// message to the running agent, independently of any continuation/resume logic.
type CodingAgentDeliveryError struct {
	Kind     CodingAgentDeliveryErrorKind
	Provider llm.Provider
	Reason   string
}

func (e *CodingAgentDeliveryError) Error() string {
	return fmt.Sprintf("coding agent delivery error (%s, %s): %s", e.Provider, e.Kind, e.Reason)
}

// isInteractiveSessionNotRegistered reports whether err is a provider
// adapter's pre-I/O pool miss ("no active X interactive session registered
// for owner session ..."). Every tmux adapter emits that shape from its
// pooled-session lookup before touching tmux, so it proves nothing was sent
// — unlike mid-send failures, which keep their own messages and stay
// uncertain.
func isInteractiveSessionNotRegistered(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "interactive session registered")
}

type UserMessageDeliveryIntent string

const (
	UserMessageDeliveryIntentAuto      UserMessageDeliveryIntent = "auto"
	UserMessageDeliveryIntentLiveInput UserMessageDeliveryIntent = "live_input"
)

type UserMessageDeliveryStatus string

const (
	UserMessageDeliveryStatusSentToCLI          UserMessageDeliveryStatus = "sent_to_cli"
	UserMessageDeliveryStatusQueuedForInjection UserMessageDeliveryStatus = "queued_for_injection"
)

type UserMessageDeliveryRequest struct {
	SessionID string
	Message   string
	Intent    UserMessageDeliveryIntent
}

type UserMessageDeliveryResult struct {
	Provider       llm.Provider
	DeliveryStatus UserMessageDeliveryStatus
	Transport      llm.CodingAgentTransport
}

// ControlKeyDeliveryRequest carries a tmux control key (e.g. "Escape") for
// injection into a currently running coding-agent session.
type ControlKeyDeliveryRequest struct {
	SessionID string
	Key       string
}

// ControlKeyDeliveryResult reports how a control key was routed.
type ControlKeyDeliveryResult struct {
	Provider  llm.Provider
	Transport llm.CodingAgentTransport
}

// DeliverControlKey injects a tmux control key into the agent's currently
// running coding-agent session. Returns DeliveryErrorKindNotSupported for
// non-tmux providers so callers can fall back to context cancellation.
func (a *Agent) deliverControlKey(ctx context.Context, req ControlKeyDeliveryRequest) (ControlKeyDeliveryResult, error) {
	provider := a.getProvider()
	result := ControlKeyDeliveryResult{Provider: provider}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindEmptyMessage,
			Provider: provider,
			Reason:   "control key is empty",
		}
	}
	if !llm.IsAllowedCodingAgentControlKey(key) {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindNotSupported,
			Provider: provider,
			Reason:   fmt.Sprintf("control key %q is not allowed", key),
		}
	}

	contract, isCodingAgent := llm.GetCodingAgentProviderContract(provider, a.modelID)
	if !isCodingAgent || !contract.SupportsLiveInput {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindNotSupported,
			Provider: provider,
			Reason:   "provider transport does not support live tmux control keys",
		}
	}
	result.Transport = contract.Transport

	if err := llm.SendCodingAgentControlKey(ctx, provider, a.modelID, req.SessionID, key); err != nil {
		return result, err
	}
	return result, nil
}

// DeliverUserMessage routes a user message through the correct running-turn
// mechanism for this agent. Tmux coding agents get provider-native live input;
// a failed tmux submission is returned to the caller instead of being hidden in
// the internal steer queue. API/structured/non-coding agents still use that
// queue between tool calls and the next LLM call.
func (a *Agent) deliverUserMessage(ctx context.Context, req UserMessageDeliveryRequest) (UserMessageDeliveryResult, error) {
	provider := a.getProvider()
	result := UserMessageDeliveryResult{Provider: provider}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindEmptyMessage,
			Provider: provider,
			Reason:   "message is empty",
		}
	}

	contract, isCodingAgent := llm.GetCodingAgentProviderContract(provider, a.modelID)
	if isCodingAgent {
		result.Transport = contract.Transport
		if a.usesStructuredTransport() {
			result.Transport = llm.CodingAgentTransportStructured
		}
	}

	// Transport-aware, not just contract-aware: the same provider is steerable on
	// tmux but query-only on the structured/JSON transport, which has no live pane
	// to send-keys into. usesStructuredTransport() gates the tmux live-input path
	// off for a structured run so it correctly falls through to the steer queue —
	// mirrors SupportsSteering() (coding_session.go). Without this a structured
	// coding-agent turn would try to tmux-inject into a one-shot process.
	if isCodingAgent && contract.SupportsLiveInput && !a.usesStructuredTransport() {
		var err error
		if req.Intent == UserMessageDeliveryIntentLiveInput {
			err = llm.SendCodingAgentLiveInput(ctx, provider, a.modelID, req.SessionID, message)
		} else {
			err = llm.SendCodingAgentRetainedInput(ctx, provider, a.modelID, req.SessionID, message)
		}
		if err != nil {
			if isInteractiveSessionNotRegistered(err) && a.isTurnInFlight() {
				// No pooled TUI while a turn runs: an API-continuation turn,
				// or a TUI turn whose pane is still booting. Either way the
				// outer conversation loop is running, so queue for its next
				// boundary (AskWithHistory drains after tool execution and
				// after the final response) instead of failing a send no CLI
				// ever saw. Without a running turn nothing drains the queue,
				// so idle sessions keep the error and the caller starts a
				// new turn.
				if age, orphaned := a.oldestQueuedSteerAge(); orphaned && age >= stuckSteerQueueAge {
					// The previous queue never drained: its turn is hung or
					// died without cleanup, and this send's pool miss proves
					// its CLI target is gone too. Parking another message
					// would strand the chat with a silent accept, so break
					// the stuck turn — reset the stale flag, drop the
					// orphaned queue — and report no-target: the caller
					// starts a fresh turn that re-resolves the current
					// provider instead.
					dropped := a.resetStuckTurnState()
					if a.logger != nil {
						a.logger.Warn(fmt.Sprintf("stuck steer queue: session=%s provider=%s orphaned=%s dropped=%d; turn flag reset, caller starts a new turn", req.SessionID, provider, age.Round(time.Second), dropped))
					}
					return result, fmt.Errorf("failed to submit live input to %s: %w", provider, err)
				}
				a.addSteerMessage(message)
				if a.logger != nil {
					a.logger.Warn(fmt.Sprintf("steer message queued for injection: session=%s provider=%s", req.SessionID, provider))
				}
				result.DeliveryStatus = UserMessageDeliveryStatusQueuedForInjection
				return result, nil
			}
			return result, fmt.Errorf("failed to submit live input to %s: %w", provider, err)
		}
		result.DeliveryStatus = UserMessageDeliveryStatusSentToCLI
		return result, nil
	}

	// The steer queue is drained only inside a running turn. Accepting a
	// message into it while idle strands it: the caller reports success and no
	// turn ever reads it (RTS 2026-09-25, a structured agent left behind by a
	// scheduled turn swallowed the user's next messages). Refuse instead so
	// the caller starts a new turn with the message.
	if !a.isTurnInFlight() {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindNoSession,
			Provider: provider,
			Reason:   "no turn is running to take the message; start a new turn",
		}
	}
	a.addSteerMessage(message)
	result.DeliveryStatus = UserMessageDeliveryStatusQueuedForInjection
	return result, nil
}
