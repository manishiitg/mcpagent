package mcpagent

import (
	"strings"
	"testing"
)

// TestSetSystemPromptReAppendsSupplementaryPrompts verifies that calling
// SetSystemPrompt (overwrite) does NOT lose prompts added via AppendSystemPrompt.
// This is the core contract that execution-only agents rely on: supplementary
// prompts (CDP, browser, skills, secrets) are appended once during setup, then
// SetSystemPrompt is called with a new base prompt during Execute().
func TestSetSystemPromptReAppendsSupplementaryPrompts(t *testing.T) {
	a := &Agent{}

	// Step 1: Initial system prompt set during agent initialization
	a.SetSystemPrompt("Initial MCP base prompt")

	// Step 2: Supplementary prompts appended after setup (simulates appendSupplementaryPrompts)
	a.AppendSystemPrompt("## Skills\nYou have access to agent-browser skill")
	a.AppendSystemPrompt("## Browser Mode: CDP\nuse host.docker.internal:9222")
	a.AppendSystemPrompt("## Secrets\nAPI_KEY=***")

	// Verify all appended prompts are present
	prompt := a.GetSystemPrompt()
	for _, expected := range []string{"Skills", "CDP", "Secrets"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("after AppendSystemPrompt, expected prompt to contain %q, got:\n%s", expected, prompt)
		}
	}

	// Step 3: Execute() calls SetSystemPrompt with a completely new base prompt (overwrite=true)
	a.SetSystemPrompt("# Execution-Only Agent\n## Code Execution Mode\nCODE EXECUTION MODE")

	// The new base prompt must be present
	final := a.GetSystemPrompt()
	if !strings.Contains(final, "Execution-Only Agent") {
		t.Fatal("new base prompt not found after SetSystemPrompt overwrite")
	}

	// The old base prompt must be gone
	if strings.Contains(final, "Initial MCP base prompt") {
		t.Fatal("old base prompt should have been replaced")
	}

	// All supplementary prompts must survive the overwrite
	for _, expected := range []string{
		"## Skills\nYou have access to agent-browser skill",
		"## Browser Mode: CDP\nuse host.docker.internal:9222",
		"## Secrets\nAPI_KEY=***",
	} {
		if !strings.Contains(final, expected) {
			t.Fatalf("supplementary prompt lost after SetSystemPrompt overwrite.\nExpected to contain: %q\nGot:\n%s", expected, final)
		}
	}
}

// TestGetAppendedSystemPromptsReturnsAllAppended verifies that GetAppendedSystemPrompts
// returns exactly the prompts added via AppendSystemPrompt, unaffected by SetSystemPrompt.
// This is used by the prompts.json debug file fix to reconstruct the full prompt.
func TestGetAppendedSystemPromptsReturnsAllAppended(t *testing.T) {
	a := &Agent{}
	a.SetSystemPrompt("base")

	a.AppendSystemPrompt("prompt-A")
	a.AppendSystemPrompt("prompt-B")
	a.AppendSystemPrompt("prompt-C")

	appended := a.GetAppendedSystemPrompts()
	if len(appended) != 3 {
		t.Fatalf("expected 3 appended prompts, got %d", len(appended))
	}
	if appended[0] != "prompt-A" || appended[1] != "prompt-B" || appended[2] != "prompt-C" {
		t.Fatalf("appended prompts mismatch: %v", appended)
	}

	// SetSystemPrompt should NOT clear the appended list
	a.SetSystemPrompt("new base")
	appended = a.GetAppendedSystemPrompts()
	if len(appended) != 3 {
		t.Fatalf("SetSystemPrompt cleared appended prompts, expected 3, got %d", len(appended))
	}
}

// TestClearAppendedSystemPrompts verifies that after clearing, SetSystemPrompt
// no longer re-appends anything.
func TestClearAppendedSystemPrompts(t *testing.T) {
	a := &Agent{}
	a.SetSystemPrompt("base")
	a.AppendSystemPrompt("## CDP\nhost.docker.internal:9222")

	a.ClearAppendedSystemPrompts()
	a.SetSystemPrompt("clean base")

	final := a.GetSystemPrompt()
	if strings.Contains(final, "CDP") {
		t.Fatal("cleared prompt should not be re-appended")
	}
	if final != "clean base" {
		t.Fatalf("expected exactly 'clean base', got: %q", final)
	}
}

// TestAppendSystemPromptEmpty verifies that appending an empty string is a no-op.
func TestAppendSystemPromptEmpty(t *testing.T) {
	a := &Agent{}
	a.SetSystemPrompt("base")
	a.AppendSystemPrompt("")

	if len(a.GetAppendedSystemPrompts()) != 0 {
		t.Fatal("empty append should be a no-op")
	}
	if a.GetSystemPrompt() != "base" {
		t.Fatalf("prompt changed after empty append: %q", a.GetSystemPrompt())
	}
}

// TestMultipleSetSystemPromptKeepsAppended verifies that calling SetSystemPrompt
// multiple times (e.g., across retry attempts) always preserves the appended prompts.
func TestMultipleSetSystemPromptKeepsAppended(t *testing.T) {
	a := &Agent{}
	a.SetSystemPrompt("init")
	a.AppendSystemPrompt("## CDP\nport 9222")

	// Simulate multiple execution retries, each calling SetSystemPrompt
	for i := 0; i < 3; i++ {
		a.SetSystemPrompt("execution attempt " + string(rune('1'+i)))
		final := a.GetSystemPrompt()
		if !strings.Contains(final, "## CDP\nport 9222") {
			t.Fatalf("retry %d: CDP prompt lost after SetSystemPrompt", i+1)
		}
	}

	// Appended list should still have exactly 1 entry, not duplicated
	if len(a.GetAppendedSystemPrompts()) != 1 {
		t.Fatalf("expected 1 appended prompt, got %d", len(a.GetAppendedSystemPrompts()))
	}
}
