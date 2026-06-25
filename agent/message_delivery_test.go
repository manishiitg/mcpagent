package mcpagent

import (
	"context"
	"errors"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

func TestDeliverUserMessageQueuesForNonCodingProvider(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenAI, ModelID: "gpt-5"}

	result, err := agent.DeliverUserMessage(context.Background(), UserMessageDeliveryRequest{
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
	got := agent.DrainSteerMessages()
	if len(got) != 1 || got[0] != "remember this" {
		t.Fatalf("queued messages = %#v", got)
	}
}

func TestDeliverUserMessageRejectsEmptyMessage(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenAI, ModelID: "gpt-5"}
	_, err := agent.DeliverUserMessage(context.Background(), UserMessageDeliveryRequest{
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
