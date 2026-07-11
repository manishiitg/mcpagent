package mcpagent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func isolatedWorkspaceTestAgent() *Agent {
	return &Agent{Logger: loggerv2.NewDefault(), SessionID: "isolated-workspace-test"}
}

// TestEnsureIsolatedWorkspaceDirCreatesTmpDirOnceAndCleansUpOnClose
// covers the lifecycle invariants the workflow-step isolation
// feature depends on:
//
//  1. ensureIsolatedWorkspaceDir creates a tmp dir under the OS
//     tmp location, named "mlp-cli-session-*" so it's recognizable
//     in `ls /tmp`.
//  2. The dir exists between creation and Close.
//  3. Repeated calls return the SAME dir (sync.Once guarantee).
//  4. Agent.Close rm -rf's the dir.
func TestEnsureIsolatedWorkspaceDirCreatesTmpDirOnceAndCleansUpOnClose(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.IsolatedSessionWorkspace = true

	dir1 := a.ensureIsolatedWorkspaceDir()
	if dir1 == "" {
		t.Fatal("ensureIsolatedWorkspaceDir returned empty path on first call; tmp-dir creation failed silently")
	}
	if !strings.Contains(filepath.Base(dir1), "mlp-cli-session-") {
		t.Errorf("tmp dir name must include the mlp-cli-session-* prefix so leaked dirs are recognizable; got %q", filepath.Base(dir1))
	}
	if info, err := os.Stat(dir1); err != nil {
		t.Fatalf("expected tmp dir %q to exist after creation: %v", dir1, err)
	} else if !info.IsDir() {
		t.Fatalf("expected %q to be a directory; got mode %v", dir1, info.Mode())
	}

	// Repeated calls must return the SAME dir. The sync.Once gate
	// prevents per-call dir creation which would leak dirs and break
	// session-scoped state.
	dir2 := a.ensureIsolatedWorkspaceDir()
	if dir2 != dir1 {
		t.Errorf("repeated ensureIsolatedWorkspaceDir must return the same dir; got %q then %q", dir1, dir2)
	}

	a.Close()
	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Errorf("Agent.Close must rm -rf the isolated workspace dir; stat err=%v (dir=%q)", err, dir1)
	}
}

// TestEnsureIsolatedWorkspaceDirRespectsFlag asserts that when
// IsolatedSessionWorkspace is false (the chat-mode default),
// ensureIsolatedWorkspaceDir is never invoked by the option
// appender. Without this guarantee, chat sessions would silently
// get a tmp dir override and lose the "agent edits my files" UX.
func TestEnsureIsolatedWorkspaceDirRespectsFlag(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.IsolatedSessionWorkspace = false
	a.CodingAgentWorkingDir = "/Users/test/workspace"

	opts := a.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli")
	if a.isolatedWorkspacePath != "" {
		t.Errorf("isolated workspace dir must NOT be created when flag is off; got %q", a.isolatedWorkspacePath)
	}
	// The CodingAgentWorkingDir option should still be appended for chat-mode usage.
	got := metadataFromCallOptions(opts)
	wd, _ := got["cursor_working_dir"].(string)
	if wd != "/Users/test/workspace" {
		t.Errorf("chat mode must pass CodingAgentWorkingDir through verbatim; got %q want %q", wd, "/Users/test/workspace")
	}
}

// TestAppendCodingAgentWorkingDirOverridesWithIsolatedTmpDir is the
// observable contract: when the flag is on, the cursor working-dir
// option carries the tmp dir path, NOT the operator-supplied
// CodingAgentWorkingDir. This is what protects the user's actual
// workspace from accidental model writes.
func TestAppendCodingAgentWorkingDirOverridesWithIsolatedTmpDir(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.IsolatedSessionWorkspace = true
	a.CodingAgentWorkingDir = "/Users/test/workspace"
	defer a.Close()

	opts := a.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli")
	got := metadataFromCallOptions(opts)
	wd, _ := got["cursor_working_dir"].(string)
	if wd == "" || wd == "/Users/test/workspace" {
		t.Errorf("isolation must override CodingAgentWorkingDir with a tmp path; got %q", wd)
	}
	if !strings.Contains(filepath.Base(wd), "mlp-cli-session-") {
		t.Errorf("isolation override must use mlp-cli-session-* tmp dir; got %q", wd)
	}
	if wd != a.isolatedWorkspacePath {
		t.Errorf("option value must match the Agent's stored isolated path; option=%q field=%q", wd, a.isolatedWorkspacePath)
	}
}

// TestWithIsolatedSessionWorkspaceOptionThreadsThroughField asserts
// the public AgentOption wires the bool onto the Agent struct
// correctly. Belt-and-suspenders against future field renames.
func TestWithIsolatedSessionWorkspaceOptionThreadsThroughField(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	WithIsolatedSessionWorkspace(true)(a)
	if !a.IsolatedSessionWorkspace {
		t.Error("WithIsolatedSessionWorkspace(true) must set IsolatedSessionWorkspace=true")
	}
	WithIsolatedSessionWorkspace(false)(a)
	if a.IsolatedSessionWorkspace {
		t.Error("WithIsolatedSessionWorkspace(false) must set IsolatedSessionWorkspace=false")
	}
}

// TestIsolatedWorkspaceDirConcurrencyCreatesOnlyOneDir guards the
// sync.Once contract under concurrent option-appending. If the
// sync.Once were dropped (e.g. replaced with a plain bool check),
// concurrent goroutines could each create their own tmp dir,
// leak all but one, and the cleanup would only rm -rf the last
// one assigned.
func TestIsolatedWorkspaceDirConcurrencyCreatesOnlyOneDir(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.IsolatedSessionWorkspace = true
	defer a.Close()

	const goroutines = 32
	var wg sync.WaitGroup
	results := make([]string, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = a.ensureIsolatedWorkspaceDir()
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == "" {
		t.Fatal("concurrent calls all returned empty; tmp-dir creation failed")
	}
	for i, got := range results {
		if got != first {
			t.Errorf("concurrent call %d returned different dir than first; got %q want %q", i, got, first)
		}
	}
}

// metadataFromCallOptions is defined in coding_agent_options_test.go;
// we reuse it via this package-level shared helper.
