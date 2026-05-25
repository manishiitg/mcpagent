package mcpagent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
)

// codingAgentProviderArtifacts maps each coding-agent provider to its
// workspace projection roots. When a session starts for one provider,
// stale projections from the OTHER providers are wiped so the workflow
// folder doesn't accumulate cross-provider rule files / config / hook
// scripts from earlier runs.
//
// Keep these in sync with each adapter's prepare*ProjectFiles function
// in multi-llm-provider-go. Paths are workspace-relative; the cleanup
// helper joins them onto the working directory before deleting.
//
// Conservative scope: only directories / files our adapters are known
// to write. AGENTS.md is shared between codex and opencode contracts,
// and we project it for both — the activeProvider check below ensures
// we don't wipe AGENTS.md when codex is active just to satisfy an
// opencode-leftover sweep (and vice versa).
var codingAgentProviderArtifacts = map[llm.Provider][]string{
	llm.ProviderClaudeCode:  {".claude", ".mcp.json"},
	llm.ProviderCodexCLI:    {".codex", "AGENTS.md"},
	llm.ProviderGeminiCLI:   {".gemini", "GEMINI.md"},
	llm.ProviderCursorCLI:   {".cursor"},
	llm.ProviderAgyCLI:      {".agents"},
	llm.ProviderOpenCodeCLI: {".opencode", "opencode.jsonc", "AGENTS.md"},
}

// CleanupStaleCodingAgentArtifacts removes workspace projection files
// from inactive coding-agent providers. Called by mcpagent at the
// start of each coding-agent integration setup so the workflow
// folder stays clean across provider switches (e.g., user moves
// from claude to cursor in the same workflow folder — the previous
// .claude/ projection stops shadowing the active cursor session).
//
// Important: this does NOT touch the active provider's own
// projection. Per-session rule files (mlp-system-<hex>.{mdc,md})
// are managed by the adapter itself — it writes them in
// prepareXxxProjectFiles on first turn (new session) and reuses
// them across subsequent turns (existing session reuse path).
// Sweeping them from here at the top of every turn would wipe the
// file mid-conversation because the adapter does not re-project on
// turn-N for an already-acquired tmux session.
//
// Best-effort: errors are swallowed because cleanup must not block
// session startup. A failed deletion just means one more stale file
// the next sweep will retry.
func CleanupStaleCodingAgentArtifacts(workingDir string, activeProvider llm.Provider) {
	if strings.TrimSpace(workingDir) == "" {
		return
	}
	for provider, paths := range codingAgentProviderArtifacts {
		if provider == activeProvider {
			continue
		}
		for _, rel := range paths {
			full := filepath.Join(workingDir, rel)
			_ = os.RemoveAll(full)
		}
	}
}
