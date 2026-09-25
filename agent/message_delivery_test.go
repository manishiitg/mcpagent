package mcpagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
)

func TestDeliverUserMessageQueuesForNonCodingProvider(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenAI, modelID: "gpt-5"}

	result, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "session-1",
		Message:   "remember this",
		Intent:    UserMessageDeliveryIntentAuto,
	})
	if err != nil {
		t.Fatalf("DeliverUserMessage() error = %v", err)
	}
	if result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
		t.Fatalf("status = %q, want %q", result.DeliveryStatus, UserMessageDeliveryStatusQueuedForInjection)
	}
	got := agent.drainSteerMessages()
	if len(got) != 1 || got[0] != "remember this" {
		t.Fatalf("queued messages = %#v", got)
	}
}

func TestDeliverUserMessageRejectsEmptyMessage(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenAI, modelID: "gpt-5"}
	_, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "session-1",
		Message:   " ",
		Intent:    UserMessageDeliveryIntentAuto,
	})
	if err == nil {
		t.Fatal("expected empty message error")
	}
	var deliveryErr *CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("expected CodingAgentDeliveryError, got %T: %v", err, err)
	}
	if deliveryErr.Kind != DeliveryErrorKindEmptyMessage {
		t.Fatalf("error kind = %q, want %q", deliveryErr.Kind, DeliveryErrorKindEmptyMessage)
	}
}

func TestDeliverUserMessageQueuesWhenTurnInFlightButNoInteractiveSession(t *testing.T) {
	// API-continuation turn (tmux contract, no pooled TUI): the interactive
	// send misses the adapter pool before any I/O. With a turn running its
	// conversation loop, the message must queue for the next LLM-call
	// boundary instead of failing a send no CLI ever saw.
	agent := &Agent{provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	agent.setTurnInFlight(true)
	result, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "muse-api-turn-no-tui",
		Message:   "steer while API turn runs",
		Intent:    UserMessageDeliveryIntentLiveInput,
	})
	if err != nil {
		t.Fatalf("deliverUserMessage() error = %v, want queue fallback", err)
	}
	if result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
		t.Fatalf("status = %q, want %q", result.DeliveryStatus, UserMessageDeliveryStatusQueuedForInjection)
	}
	got := agent.drainSteerMessages()
	if len(got) != 1 || got[0] != "steer while API turn runs" {
		t.Fatalf("queued messages = %#v", got)
	}
}

func TestDeliverUserMessageBreaksStuckQueueToNewTurn(t *testing.T) {
	// A previous queue that never drained proves its turn is hung or died
	// without cleanup (pool miss already proved its CLI target is gone).
	// Parking another message would strand the chat with a silent accept,
	// so delivery resets the stale flag and reports no-target: the caller
	// starts a fresh turn instead.
	agent := &Agent{provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	agent.setTurnInFlight(true)
	agent.addSteerMessage("first, never drained")
	agent.steerMu.Lock()
	agent.pendingSteerEnqueuedAt[0] = time.Now().Add(-stuckSteerQueueAge - time.Minute)
	agent.steerMu.Unlock()
	_, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "muse-stuck-queue",
		Message:   "second message",
		Intent:    UserMessageDeliveryIntentLiveInput,
	})
	if err == nil {
		t.Fatal("expected no-target error when the steer queue is proven orphaned")
	}
	if !strings.Contains(err.Error(), "interactive session registered") {
		t.Fatalf("error = %v, want pool-miss error", err)
	}
	if agent.isTurnInFlight() {
		t.Fatal("turn flag still set after stuck-queue break")
	}
	if got := agent.drainSteerMessages(); len(got) != 0 {
		t.Fatalf("queued messages = %#v, want orphaned queue dropped", got)
	}
}

func TestDeliverUserMessageStillErrorsWithoutTurnInFlight(t *testing.T) {
	// No running turn means nothing drains the steer queue, so a pool miss
	// must keep erroring (the backend starts a new turn instead of stalling
	// an accepted message). This also pins the fixture onto the interactive
	// branch: only it can produce the pool-miss error.
	agent := &Agent{provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	_, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "muse-idle-no-tui",
		Message:   "nobody drains",
		Intent:    UserMessageDeliveryIntentLiveInput,
	})
	if err == nil {
		t.Fatal("expected not-registered error when no turn drains the steer queue")
	}
	if !strings.Contains(err.Error(), "interactive session registered") {
		t.Fatalf("error = %v, want pool-miss error", err)
	}
	if got := agent.drainSteerMessages(); len(got) != 0 {
		t.Fatalf("queued messages = %#v, want none", got)
	}
}

func TestDeliverUserMessageReportsActualStructuredTransport(t *testing.T) {
	agent := &Agent{
		provider:             llm.ProviderCodexCLI,
		modelID:              "gpt-5.6-sol",
		codingAgentTransport: llm.CodingAgentTransportStructured,
	}
	result, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "structured-session",
		Message:   "continue",
	})
	if err != nil {
		t.Fatalf("deliverUserMessage() error = %v", err)
	}
	if result.Transport != llm.CodingAgentTransportStructured {
		t.Fatalf("transport = %q, want structured", result.Transport)
	}
	if result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
		t.Fatalf("status = %q, want queued_for_injection", result.DeliveryStatus)
	}
}
