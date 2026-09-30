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

func TestAgyFullCLIModeAndRouting(t *testing.T) {
	for _, mode := range []string{codingAgentToolsMCPOnly, codingAgentToolsHybrid, codingAgentToolsFull, codingAgentToolsFullUnconfined} {
		a := &Agent{provider: "agy-cli", codingAgentToolsMode: mode}
		want := mode
		if mode == codingAgentToolsFull {
			want = codingAgentToolsHybrid
		}
		if got := a.agyNativeToolsMode(); got != want {
			t.Fatalf("mode %s without lock = %s, want %s", mode, got, want)
		}
		prompt := a.codingAgentProviderRoutingPreamble()
		if mode == codingAgentToolsFullUnconfined && (!strings.Contains(prompt, "AGY Full CLI is enabled") || strings.Contains(prompt, "subagents are disabled")) {
			t.Fatalf("Full CLI routing still forbids native tools: %s", prompt)
		}
		a.cliSecurityPolicy = &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, LandlockRunner: "/bin/true", PrivateHome: t.TempDir()}
		if mode == codingAgentToolsFull && runtime.GOOS == "linux" {
			want = codingAgentToolsFull
		}
		if got := a.agyNativeToolsMode(); got != want {
			t.Fatalf("mode %s with lock = %s, want %s", mode, got, want)
		}
	}
}

func TestAgyFullCLIEmittedOptions(t *testing.T) {
	for _, mode := range []string{codingAgentToolsMCPOnly, codingAgentToolsHybrid, codingAgentToolsFullUnconfined} {
		t.Run(mode, func(t *testing.T) {
			a := newPiToolsModeTestAgent(t, mode)
			opts, err := a.appendAgyCLIIntegrationOptions(nil)
			if err != nil {
				t.Fatal(err)
			}
			resolved := &llmtypes.CallOptions{}
			for _, opt := range opts {
				opt(resolved)
			}
			if resolved.Metadata == nil || resolved.Metadata.Custom["agy_native_tools_mode"] != mode {
				t.Fatalf("emitted native mode = %#v", resolved.Metadata)
			}
			if config, _ := resolved.Metadata.Custom["agy_mcp_config"].(string); config == "" {
				t.Fatal("native Full CLI lost the private MCP bridge")
			}
		})
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

// Codex under unconfined Full CLI gets its own workspace-write sandbox; every other mode keeps
// the read-only one.
func TestCodexFullUnconfinedOnlyForFullUnconfined(t *testing.T) {
	for mode, want := range map[string]bool{
		codingAgentToolsMCPOnly: false, codingAgentToolsHybrid: false,
		codingAgentToolsFull: false, codingAgentToolsFullUnconfined: true,
	} {
		if got := (&Agent{codingAgentToolsMode: mode}).codexFullUnconfined(); got != want {
			t.Fatalf("%s: got %v want %v", mode, got, want)
		}
	}
}
