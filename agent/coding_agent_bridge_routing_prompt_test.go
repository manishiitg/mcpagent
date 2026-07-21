package mcpagent

import (
	"strings"
	"testing"
)

const testDefaultPreamble = "DEFAULT PREAMBLE FOR TEST"

func TestAppendBridgeRoutingInstructionsDefaultUnchanged(t *testing.T) {
	a := &Agent{}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.GetSystemPrompt()
	if !strings.Contains(got, testDefaultPreamble) {
		t.Fatalf("expected default preamble in system prompt, got: %s", got)
	}
	if !strings.Contains(got, "IMPORTANT — bridge tool routing") {
		t.Fatalf("expected default bridgeRoutingExplicitInstructions text in system prompt, got: %s", got)
	}
}

func TestAppendBridgeRoutingInstructionsCustomOverride(t *testing.T) {
	custom := "MY CUSTOM ROUTING TEXT"
	a := &Agent{bridgeRoutingInstructionsOverride: &custom}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.GetSystemPrompt()
	if !strings.Contains(got, custom) {
		t.Fatalf("expected custom override text in system prompt, got: %s", got)
	}
	if strings.Contains(got, testDefaultPreamble) {
		t.Fatalf("default preamble should NOT appear when overridden, got: %s", got)
	}
	if strings.Contains(got, "IMPORTANT — bridge tool routing") {
		t.Fatalf("default bridgeRoutingExplicitInstructions text should NOT appear when overridden, got: %s", got)
	}
}

func TestAppendBridgeRoutingInstructionsEmptyOverrideSuppresses(t *testing.T) {
	empty := ""
	a := &Agent{bridgeRoutingInstructionsOverride: &empty}
	a.appendBridgeRoutingInstructions(testDefaultPreamble)

	got := a.GetSystemPrompt()
	if got != "" {
		t.Fatalf("expected empty system prompt when override is \"\" (suppressed), got: %s", got)
	}
}

func TestWithBridgeRoutingInstructionsOptionSetsOverride(t *testing.T) {
	a := &Agent{}
	opt := WithBridgeRoutingInstructions("custom text")
	opt(a)

	if a.bridgeRoutingInstructionsOverride == nil {
		t.Fatal("expected bridgeRoutingInstructionsOverride to be set")
	}
	if *a.bridgeRoutingInstructionsOverride != "custom text" {
		t.Fatalf("expected override value %q, got %q", "custom text", *a.bridgeRoutingInstructionsOverride)
	}
}
