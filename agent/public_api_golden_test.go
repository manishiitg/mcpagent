package mcpagent

import (
	"reflect"
	"slices"
	"testing"
)

// This is the migration ratchet for the legacy Agent surface. The list starts
// at 70 legacy methods. The three target methods are added during the cutover;
// after that, the list must only shrink until the replacement Agent/Session API
// is complete. Any deliberate change updates this list in the same commit.
func TestAgentPublicMethodSurface(t *testing.T) {
	want := []string{
		"AddEventListener",
		"AddInstructions",
		"AddSteerMessage",
		"ApplyAgentSessionHandle",
		"Ask",
		"AskWithHistory",
		"AttachSkill",
		"BuildBridgeMCPConfig",
		"BuildLargeOutputFilePath",
		"Close",
		"ContinueAgentSessionWithHistory",
		"ContinueConversation",
		"CreateLargeOutputVirtualTools",
		"CreateVirtualTools",
		"CurrentAgentSessionHandle",
		"Definition",
		"Deliver",
		"DeliverControlKey",
		"DeliverUserMessage",
		"DrainSteerMessages",
		"EmitTypedEvent",
		"GetConfiguredServerName",
		"GetCustomToolExecutor",
		"GetCustomTools",
		"GetDeferredToolCount",
		"GetDiscoveredToolCount",
		"GetFolderGuardPaths",
		"GetLLMModelConfig",
		"GetProvider",
		"GetSelectedTools",
		"GetServerNames",
		"GetTokenUsage",
		"GetTokenUsageWithPricing",
		"GetToolOutputHandler",
		"GetToolToServer",
		"HandleEvent",
		"HandleLargeOutputVirtualTool",
		"HandleVirtualTool",
		"Instructions",
		"RegisterCustomTool",
		"RegisterCustomToolWithTimeout",
		"RemoveEventListener",
		"ResetInstructions",
		"Run",
		"SetFolderGuardPaths",
		"SetInstructions",
		"SetProvider",
		"SetToolAccess",
		"SetToolOutputHandler",
		"Start",
		"StartCodingAgentTransportSession",
		"SubscribeToEvents",
		"SupportsSteering",
		"TurnInFlight",
	}

	typeOfAgent := reflect.TypeOf((*Agent)(nil))
	got := make([]string, 0, typeOfAgent.NumMethod())
	for i := 0; i < typeOfAgent.NumMethod(); i++ {
		got = append(got, typeOfAgent.Method(i).Name)
	}

	if len(got) != 54 {
		t.Fatalf("exported *Agent method count = %d, want cutover surface 54; methods=%v", len(got), got)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("exported *Agent methods changed\n got: %v\nwant: %v", got, want)
	}
}

func TestSessionPublicMethodSurface(t *testing.T) {
	want := []string{"Close", "Events", "Run", "Send", "Snapshot"}
	typeOfSession := reflect.TypeOf((*Session)(nil))
	got := make([]string, 0, typeOfSession.NumMethod())
	for i := 0; i < typeOfSession.NumMethod(); i++ {
		got = append(got, typeOfSession.Method(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("exported *Session methods changed\n got: %v\nwant: %v", got, want)
	}
}
