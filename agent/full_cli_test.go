package mcpagent

import (
	"runtime"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Full CLI adds Claude's own shell and file edits only when the CLI starts
// under the Landlock launcher; an unconfined CLI runs as hybrid.
func TestFullCLIRequiresLandlock(t *testing.T) {
	a := &Agent{codingAgentToolsMode: codingAgentToolsFull}
	if a.fullCLIEnabled() || strings.Contains(a.claudeNativeTools(), "Bash") {
		t.Fatal("Full CLI applied without a confining policy")
	}
	if !a.nativeCodingToolsEnabled() {
		t.Fatal("full mode must keep hybrid's native tools")
	}
	a.cliSecurityPolicy = &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, LandlockRunner: "/bin/true", PrivateHome: t.TempDir()}
	want := runtime.GOOS == "linux"
	if a.fullCLIEnabled() != want || strings.Contains(a.claudeNativeTools(), "Bash") != want {
		t.Fatalf("confined Full CLI on %s: enabled=%v tools=%s", runtime.GOOS, a.fullCLIEnabled(), a.claudeNativeTools())
	}
	a.codingAgentToolsMode = codingAgentToolsHybrid
	if a.fullCLIEnabled() {
		t.Fatal("hybrid must not grant Full CLI")
	}
}

// full_unconfined is Full CLI without the lock, asked for only by the server on a person's own
// single-user machine: Claude gets its own shell and file edits, and nothing else changes.
func TestFullCLIUnconfinedNeedsNoLock(t *testing.T) {
	a := &Agent{codingAgentToolsMode: codingAgentToolsFullUnconfined}
	if !a.fullCLIEnabled() || !strings.Contains(a.claudeNativeTools(), "Bash") || !strings.Contains(a.claudeNativeTools(), "Write") {
		t.Fatalf("unconfined Full CLI: enabled=%v tools=%s", a.fullCLIEnabled(), a.claudeNativeTools())
	}
	if !a.nativeCodingToolsEnabled() {
		t.Fatal("full_unconfined must keep hybrid's native tools")
	}
	a.codingAgentToolsMode = codingAgentToolsFull
	if a.fullCLIEnabled() {
		t.Fatal("plain full must still need the lock")
	}
}
