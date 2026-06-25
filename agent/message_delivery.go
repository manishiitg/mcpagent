package mcpagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
)

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
func (a *Agent) DeliverControlKey(ctx context.Context, req ControlKeyDeliveryRequest) (ControlKeyDeliveryResult, error) {
	provider := a.GetProvider()
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

	contract, isCodingAgent := llm.GetCodingAgentProviderContract(provider, a.ModelID)
	if !isCodingAgent || !contract.SupportsLiveInput {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindNotSupported,
			Provider: provider,
			Reason:   "provider transport does not support live tmux control keys",
		}
	}
	result.Transport = contract.Transport

	if err := llm.SendCodingAgentControlKey(ctx, provider, a.ModelID, req.SessionID, key); err != nil {
		return result, err
	}
	return result, nil
}

// DeliverUserMessage routes a user message through the correct running-turn
// mechanism for this agent. Tmux coding agents get provider-native live input;
// API/structured/non-coding agents fall back to the agent's internal steer queue
// so the message is injected between tool calls and the next LLM call.
func (a *Agent) DeliverUserMessage(ctx context.Context, req UserMessageDeliveryRequest) (UserMessageDeliveryResult, error) {
	provider := a.GetProvider()
	result := UserMessageDeliveryResult{Provider: provider}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindEmptyMessage,
			Provider: provider,
			Reason:   "message is empty",
		}
	}

	contract, isCodingAgent := llm.GetCodingAgentProviderContract(provider, a.ModelID)
	if isCodingAgent {
		result.Transport = contract.Transport
	}

	if isCodingAgent && contract.SupportsLiveInput {
		if err := llm.SendCodingAgentLiveInput(ctx, provider, a.ModelID, req.SessionID, message); err != nil {
			// Live delivery failed (e.g. the foreground turn already exited, or
			// there's no active pane to inject into). Fall back to the steer queue
			// so the message is redelivered by the drain backstop instead of being
			// lost — the caller treats QueuedForInjection as "not definitively
			// delivered" and won't mark the completion notified.
			a.AddSteerMessage(message)
			result.DeliveryStatus = UserMessageDeliveryStatusQueuedForInjection
			return result, nil
		}
		result.DeliveryStatus = UserMessageDeliveryStatusSentToCLI
		return result, nil
	}

	a.AddSteerMessage(message)
	result.DeliveryStatus = UserMessageDeliveryStatusQueuedForInjection
	return result, nil
}
