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
			return result, err
		}
		result.DeliveryStatus = UserMessageDeliveryStatusSentToCLI
		return result, nil
	}

	a.AddSteerMessage(message)
	result.DeliveryStatus = UserMessageDeliveryStatusQueuedForInjection
	return result, nil
}
