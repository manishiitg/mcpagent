package mcpagent

import (
	"context"
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

func TestDeliverUserMessageQueuesForStructuredCodingProvider(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenCodeCLI, ModelID: "opencode"}

	result, err := agent.DeliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "session-1",
		Message:   "structured follow-up",
		Intent:    UserMessageDeliveryIntentAuto,
	})
	if err != nil {
		t.Fatalf("DeliverUserMessage() error = %v", err)
	}
	if result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
		t.Fatalf("status = %q, want %q", result.DeliveryStatus, UserMessageDeliveryStatusQueuedForInjection)
	}
	if result.Transport != llm.CodingAgentTransportStructured {
		t.Fatalf("transport = %q, want %q", result.Transport, llm.CodingAgentTransportStructured)
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
	if !llm.IsCodingAgentContinuationError(err, llm.CodingAgentContinuationErrorNonContinuable) {
		t.Fatalf("expected non-continuable error, got %T: %v", err, err)
	}
}
