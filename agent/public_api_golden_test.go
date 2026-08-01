package mcpagent

import (
	"reflect"
	"slices"
	"testing"
)

// This is the migration ratchet for the legacy Agent surface. The list starts
// at 70 methods and must only shrink until the replacement Agent/Session API is
// complete. Any deliberate removal updates this list in the same commit; an
// accidental addition fails here instead of quietly expanding the lifecycle
// callers are allowed to orchestrate.
func TestAgentPublicMethodSurface(t *testing.T) {
	want := []string{
		"AddEventListener",
		"AddInstructions",
		"AddSteerMessage",
		"ApplyAgentSessionHandle",
		"Ask",
		"AskWithHistory",
		"AttachSkill",
		"AttachedSkills",
		"BuildBridgeMCPConfig",
		"BuildLargeOutputFilePath",
		"CheckConnectionHealth",
		"ClearSkills",
		"Close",
		"ContinueAgentSession",
		"ContinueAgentSessionWithHistory",
		"ContinueConversation",
		"CreateLargeOutputVirtualTools",
		"CreateVirtualTools",
		"CurrentAgentSessionHandle",
		"Deliver",
		"DeliverControlKey",
		"DeliverUserMessage",
		"DetachSkill",
		"DrainSteerMessages",
		"EmitTypedEvent",
		"GetConfiguredServerName",
		"GetConnectionStats",
		"GetContext",
		"GetCustomToolCategories",
		"GetCustomToolExecutor",
		"GetCustomTools",
		"GetCustomToolsByCategory",
		"GetDeferredToolCount",
		"GetDiscoveredToolCount",
		"GetEventStream",
		"GetFolderGuardPaths",
		"GetLLMModelConfig",
		"GetMCPConfigJSON",
		"GetPrompts",
		"GetProvider",
		"GetResources",
		"GetSelectedTools",
		"GetServerNames",
		"GetTokenUsage",
		"GetTokenUsageWithPricing",
		"GetToolOutputHandler",
		"GetToolToServer",
		"HandleEvent",
		"HandleLargeOutputVirtualTool",
		"HandleVirtualTool",
		"HasStreamingCapability",
		"Instructions",
		"IsCancelled",
		"RebuildSystemPromptWithFilteredServers",
		"RegisterCustomTool",
		"RegisterCustomToolWithTimeout",
		"RemoveEventListener",
		"ReplaceCustomToolExecutor",
		"ResetInstructions",
		"SetFolderGuardPaths",
		"SetInstructions",
		"SetProvider",
		"SetToolAccess",
		"SetToolArgTransformer",
		"SetToolOutputHandler",
		"StartCodingAgentTmuxSession",
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

	if len(got) != 70 {
		t.Fatalf("exported *Agent method count = %d, want migration baseline 70; methods=%v", len(got), got)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("exported *Agent methods changed\n got: %v\nwant: %v", got, want)
	}
}
