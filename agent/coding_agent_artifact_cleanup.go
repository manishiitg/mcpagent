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
// from inactive coding-agent providers, plus sweeps stale per-session
// rule files (mlp-system-*.mdc / .md) under the active provider's
// rules dir. Called by mcpagent at the start of each coding-agent
// integration setup so the workflow folder stays clean across:
//
//   - provider switches (e.g., user moves from claude to cursor in
//     the same workflow folder — the previous .claude/ projection
//     stops shadowing the active cursor session)
//   - crashed/force-killed sessions that left mlp-system-<hex>.{mdc,md}
//     files unreaped (cleanup callbacks only run on graceful exit;
//     SIGKILL or process crash leaves them on disk forever)
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
	sweepActiveProviderStaleRules(workingDir, activeProvider)
}

// sweepActiveProviderStaleRules removes mlp-system-*.{mdc,md} files
// under the active provider's rules dir. The current session will
// immediately re-project its own version, so this is effectively a
// "drop everything orphaned and start fresh" sweep. Within a single
// session lifecycle, the adapter re-creates the file as the first
// step of prepareXxxProjectFiles; the brief window between sweep and
// re-create is irrelevant because the CLI hasn't been launched yet.
func sweepActiveProviderStaleRules(workingDir string, activeProvider llm.Provider) {
	var pattern string
	switch activeProvider {
	case llm.ProviderCursorCLI:
		pattern = filepath.Join(workingDir, ".cursor", "rules", "mlp-system-*.mdc")
	case llm.ProviderAgyCLI:
		pattern = filepath.Join(workingDir, ".agents", "rules", "mlp-system-*.md")
	default:
		return
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
