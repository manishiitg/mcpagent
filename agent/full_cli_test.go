package mcpagent

import (
	"runtime"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// There are two modes. Full CLI applies only when the CLI is confined (or
// explicitly unconfined); a "full" request that is not confined runs as
// mcp_only, never with native tools. A stored "hybrid" reads as "full".
func TestFullCLIRequiresConfinement(t *testing.T) {
	a := &Agent{codingAgentToolsMode: codingAgentToolsFull, provider: "claude-code"}
	if a.fullCLIEnabled() || a.nativeCodingToolsEnabled() {
		t.Fatal("native tools applied without a confining policy")
	}
	a.cliSecurityPolicy = &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, LandlockRunner: "/bin/true", PrivateHome: t.TempDir()}
	want := runtime.GOOS == "linux"
	if a.fullCLIEnabled() != want || a.nativeCodingToolsEnabled() != want {
		t.Fatalf("confined Full CLI on %s: enabled=%v native=%v", runtime.GOOS, a.fullCLIEnabled(), a.nativeCodingToolsEnabled())
	}
	if !(&Agent{codingAgentToolsMode: codingAgentToolsFull, provider: "claude-code", cliSecurityPolicy: confinedTestPolicy()}).nativeCodingToolsEnabled() {
		t.Fatal("confined full must run with native tools")
	}
}

// confinedTestPolicy confines a CLI on the test's host: Seatbelt on a Mac,
// the Landlock launcher on Linux.
func confinedTestPolicy() *llmtypes.CLISecurityPolicy {
	return &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Seatbelt: true, LandlockRunner: "/bin/true", PrivateHome: "/tmp/agentworks-test-cli-home"}
}

func TestRetiredHybridModeReadsAsFull(t *testing.T) {
	a := &Agent{}
	for _, retired := range []string{" Hybrid ", "full_unconfined"} {
		withCodingAgentToolsMode(retired)(a)
		if a.codingAgentToolsMode != codingAgentToolsFull {
			t.Fatalf("%q = %q, want full", retired, a.codingAgentToolsMode)
		}
	}
}

func TestAgyFullCLIMode(t *testing.T) {
	for mode, want := range map[string]string{
		codingAgentToolsMCPOnly: codingAgentToolsMCPOnly,
		codingAgentToolsFull:    codingAgentToolsMCPOnly, // not confined
	} {
		if got := (&Agent{provider: "agy-cli", codingAgentToolsMode: mode}).agyNativeToolsMode(); got != want {
			t.Fatalf("mode %s = %s, want %s", mode, got, want)
		}
	}
	if got := (&Agent{provider: "agy-cli", codingAgentToolsMode: codingAgentToolsFull, cliSecurityPolicy: confinedTestPolicy()}).agyNativeToolsMode(); got != codingAgentToolsFull {
		t.Fatalf("confined full = %s", got)
	}
}

func TestAgyFullCLIEmittedOptions(t *testing.T) {
	for _, mode := range []string{codingAgentToolsMCPOnly, codingAgentToolsFull} {
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
