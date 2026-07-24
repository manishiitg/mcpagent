package mcpagent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// projectedSkillLocation describes where a provider projects attached skills and
// its managed system-prompt file, so on-close cleanup can remove exactly what
// this session wrote into a REAL workdir. (Isolated workspaces are rm -rf'd
// wholesale by Agent.Close, so this never runs for them.) Skill subdirs mirror
// the per-adapter constants in multi-llm-provider-go
// (claudeCodeSkillsSubdir/cursorSkillsSubdir/codexSkillsSubdir/piSkillsSubdir).
type projectedSkillLocation struct {
	skillsSubdir string // e.g. ".claude/skills"
	promptFile   string // managed system-prompt file; "" when the provider keeps it inside a dir its own adapter teardown already wipes (Cursor: .cursor)
	promptMarker string // substring proving WE wrote promptFile — never delete operator content
}

var projectedSkillLocations = map[llm.Provider]projectedSkillLocation{
	llm.ProviderClaudeCode: {".claude/skills", "CLAUDE.md", "mlp-session-instructions"},
	llm.ProviderCodexCLI:   {".agents/skills", "AGENTS.md", "mlp-session-instructions"},
	llm.ProviderCursorCLI:  {".cursor/skills", "", ""},
	llm.ProviderPiCLI:      {".pi/skills", ".pi/APPEND_SYSTEM.md", "MCP Agent System Instructions"},
}

// cleanupProjectedArtifactsOnClose removes exactly the skills + managed prompt
// this session projected for the active provider into a real (non-isolated)
// workdir: each attached skill's own folder (by name — an operator's
// differently-named skills are untouched) and the managed prompt file (only when
// it still carries our marker, so operator content is never destroyed). Empty
// parent dirs are pruned; the workdir itself is never pruned. No-op for
// non-coding providers and unknown workdirs. Closes the gap where Claude
// (.claude/skills + CLAUDE.md), Codex (.agents/skills), and Pi (.pi/skills +
// .pi/APPEND_SYSTEM.md) left projected artifacts on disk after a real-workdir
// session ended (only Cursor's adapter already wiped its .cursor tree).
func cleanupProjectedArtifactsOnClose(workingDir string, provider llm.Provider, skills []*llmtypes.Skill) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return
	}
	loc, ok := projectedSkillLocations[provider]
	if !ok {
		return
	}

	var pruneDirs []string
	if loc.skillsSubdir != "" && len(skills) > 0 {
		base := filepath.Join(workingDir, loc.skillsSubdir)
		for _, s := range skills {
			if s == nil || strings.TrimSpace(s.Name) == "" {
				continue
			}
			removeManagedDir(filepath.Join(base, s.Name))
		}
		pruneDirs = append(pruneDirs, base, filepath.Dir(base)) // e.g. .claude/skills, then .claude
	}
	if loc.promptFile != "" && loc.promptMarker != "" {
		promptPath := filepath.Join(workingDir, loc.promptFile)
		// #nosec G304 - path is built from the configured coding-agent working directory.
		body, readErr := os.ReadFile(promptPath)
		if readErr == nil && strings.Contains(string(body), loc.promptMarker) {
			_ = os.Remove(promptPath)
		}
		if dir := filepath.Dir(promptPath); dir != workingDir {
			pruneDirs = append(pruneDirs, dir) // e.g. .pi (only if now empty)
		}
	}
	pruneEmptyDirs(pruneDirs...)
}

func cleanupInactiveCodingAgentProjectArtifacts(workingDir string, activeProvider llm.Provider) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return
	}
	active := string(activeProvider)

	if active != string(llm.ProviderClaudeCode) {
		removeManagedInstructionFile(filepath.Join(workingDir, "CLAUDE.md"))
		removeManagedDir(filepath.Join(workingDir, ".claude"))
	}
	if active != string(llm.ProviderCodexCLI) {
		removeManagedInstructionFile(filepath.Join(workingDir, "AGENTS.md"))
		removeManagedDir(filepath.Join(workingDir, ".codex"))
	}
	removeManagedInstructionFile(filepath.Join(workingDir, "GEMINI.md"))
	removeManagedDir(filepath.Join(workingDir, ".gemini"))
	removeManagedDir(filepath.Join(workingDir, ".gemini-main"))
	if active != string(llm.ProviderCursorCLI) {
		removeManagedDir(filepath.Join(workingDir, ".cursor"))
	}
	if active != string(llm.ProviderPiCLI) {
		removeManagedDir(filepath.Join(workingDir, ".pi"))
	}
	if active != string(llm.ProviderCodexCLI) {
		removeManagedDir(filepath.Join(workingDir, ".agents"))
	}
	// Antigravity CLI (Agy) is no longer a supported provider, so its
	// artifacts under .agents/ (which Codex CLI also uses, for
	// .agents/skills/ — the cross-provider skills convention) are always
	// stale and safe to remove unconditionally.
	removeManagedFile(filepath.Join(workingDir, ".agents", "rules", "mlp-system.md"))
	removeManagedFileIfGenerated(filepath.Join(workingDir, ".agents", "mcp_config.json"))
	removeManagedFileIfGenerated(filepath.Join(workingDir, ".agents", "hooks.json"))
	removeManagedFile(filepath.Join(workingDir, ".agents", "mlp-bridge-only-hook.sh"))
	removeManagedFile(filepath.Join(workingDir, ".agents", "mlp-bridge-only-denials.jsonl"))
	pruneEmptyDirs(
		filepath.Join(workingDir, ".agents", "rules"),
		filepath.Join(workingDir, ".agents", "skills"),
		filepath.Join(workingDir, ".agents"),
	)
}

func removeManagedInstructionFile(path string) {
	// #nosec G304 - path is built from the configured coding-agent working directory.
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if strings.Contains(string(body), "mlp-session-instructions") {
		_ = os.Remove(path)
	}
}

func removeManagedFileIfGenerated(path string) {
	// #nosec G304 - path is built from the configured coding-agent working directory.
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(body)
	if strings.Contains(text, "api-bridge") ||
		strings.Contains(text, "MCP_TOOLS") ||
		strings.Contains(text, "mcpbridge") ||
		strings.Contains(text, "mlp-deny-builtin") ||
		strings.Contains(text, "mlp-bridge-only") {
		_ = os.Remove(path)
	}
}

func removeManagedFile(path string) {
	_ = os.Remove(path)
}

func removeManagedDir(path string) {
	_ = os.RemoveAll(path)
}

func pruneEmptyDirs(paths ...string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}
